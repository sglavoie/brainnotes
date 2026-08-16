package noteops

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// FrontmatterOp identifies a single key-level mutation.
type FrontmatterOp int

const (
	FrontmatterSet FrontmatterOp = iota
	FrontmatterListAdd
	FrontmatterListRemove
)

// FrontmatterMutation is a parsed, typed frontmatter mutation.
type FrontmatterMutation struct {
	Key   string
	Op    FrontmatterOp
	Value any
}

// FrontmatterResult describes a completed note frontmatter update.
type FrontmatterResult struct {
	Source  PathResolution
	Journal *journal.Entry
	Changed bool
}

// fileRequirement is a file a write depends on but never changes, such as the
// configuration whose declarations decided the value's shape.
type fileRequirement struct {
	path   string
	expect vault.FileExpectation
}

type frontmatterOptions struct {
	command        string
	journalOptions []journal.RecorderOption
	requires       []fileRequirement
}

// FrontmatterOption customizes SetFrontmatter and UnsetFrontmatter.
type FrontmatterOption func(*frontmatterOptions)

// WithFrontmatterCommand sets the command stored in the undo journal entry.
func WithFrontmatterCommand(command string) FrontmatterOption {
	return func(o *frontmatterOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithFrontmatterRequire pins a file the mutation depends on but never writes.
// The transaction verifies it at the commit boundary, so a write validated
// against one revision of that file is refused if another revision has replaced
// it by the time the note is saved.
func WithFrontmatterRequire(path string, expect vault.FileExpectation) FrontmatterOption {
	return func(o *frontmatterOptions) {
		if strings.TrimSpace(path) == "" {
			return
		}
		o.requires = append(o.requires, fileRequirement{path: path, expect: expect})
	}
}

// WithFrontmatterJournalOptions passes recorder options through to the undo journal.
func WithFrontmatterJournalOptions(opts ...journal.RecorderOption) FrontmatterOption {
	return func(o *frontmatterOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

type captureJournal interface {
	Capture(path string) error
}

type snapshot struct {
	value   any
	present bool
}

var frontmatterKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidFrontmatterKey reports whether key is accepted by set-style mutations.
func ValidFrontmatterKey(key string) bool {
	return frontmatterKeyRe.MatchString(key)
}

// ParseFrontmatterMutations converts raw key/operator/value strings into typed
// mutations using the same coercion and config rules as `bn set`.
func ParseFrontmatterMutations(pairs []string, typeFlag string, cfg *config.Config) ([]FrontmatterMutation, error) {
	if cfg == nil {
		return nil, Errorf(CodeInvalidRequest, "set: nil config")
	}
	out := make([]FrontmatterMutation, 0, len(pairs))
	for _, rawPair := range pairs {
		pair, err := ParseFrontmatterMutation(rawPair, typeFlag, &cfg.Frontmatter)
		if err != nil {
			return nil, err
		}
		out = append(out, pair)
	}
	return out, nil
}

// ParseFrontmatterMutation parses one raw key/operator/value string.
func ParseFrontmatterMutation(rawPair, typeFlag string, fm *config.FrontmatterConfig) (FrontmatterMutation, error) {
	key, opStr, rawVal, ok := SplitFrontmatterPairOp(rawPair)
	if !ok || key == "" {
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: invalid pair %q (expected key=value, key+=value, or key-=value)", rawPair)
	}
	return NewFrontmatterMutation(key, opStr, rawVal, typeFlag, fm)
}

// NewFrontmatterMutation builds a typed mutation from request fields.
func NewFrontmatterMutation(key, opStr, rawVal, typeFlag string, fm *config.FrontmatterConfig) (FrontmatterMutation, error) {
	if !ValidFrontmatterKey(key) {
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: invalid key %q (allowed: letters, digits, '_', '-')", key)
	}
	if fm == nil {
		fm = &config.FrontmatterConfig{}
	}

	def, defined := fm.Definition(key)
	isList := defined && def.IsList()
	isWikilink := defined && def.IsWikilink()
	effectiveType := effectiveSetType(typeFlag, def, defined)

	switch opStr {
	case "=":
		return parseSetAssignment(key, rawVal, effectiveType, isList)
	case "+=", "-=":
		return parseSetListMutation(key, opStr, rawVal, effectiveType, isList, isWikilink)
	default:
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: invalid operator %q (expected =, +=, or -=)", opStr)
	}
}

// SplitFrontmatterPairOp finds the leftmost operator ('=', '+=', or '-=') in p.
func SplitFrontmatterPairOp(p string) (key, op, val string, ok bool) {
	for i := 0; i < len(p); i++ {
		if i+1 < len(p) && p[i+1] == '=' && (p[i] == '+' || p[i] == '-') {
			return p[:i], p[i : i+2], p[i+2:], true
		}
		if p[i] == '=' {
			return p[:i], "=", p[i+1:], true
		}
	}
	return "", "", "", false
}

// effectiveSetType lets a configured property definition drive coercion when
// the caller did not force a type, so `bn set` and the web APIs store the kind
// the property is declared to hold instead of re-guessing from the literal.
func effectiveSetType(typeFlag string, def config.PropertyDefinition, defined bool) string {
	if !defined || (typeFlag != "" && typeFlag != "auto") {
		return typeFlag
	}
	switch def.ScalarKind() {
	case config.KindString, config.KindWikilink:
		return "raw"
	case config.KindDate:
		return "date"
	case config.KindDatetime:
		return "datetime"
	case config.KindInt:
		return "int"
	case config.KindFloat:
		return "float"
	case config.KindBool:
		return "bool"
	}
	return typeFlag
}

func parseSetAssignment(key, rawVal, typeFlag string, isList bool) (FrontmatterMutation, error) {
	if isList {
		vals, err := SplitFrontmatterListValues(rawVal, typeFlag)
		if err != nil {
			return FrontmatterMutation{}, err
		}
		return FrontmatterMutation{Key: key, Op: FrontmatterSet, Value: vals}, nil
	}

	value, err := CoerceFrontmatterValue(rawVal, typeFlag)
	if err != nil {
		return FrontmatterMutation{}, err
	}
	return FrontmatterMutation{Key: key, Op: FrontmatterSet, Value: value}, nil
}

func parseSetListMutation(key, opStr, rawVal, typeFlag string, isList, isWikilink bool) (FrontmatterMutation, error) {
	if !isList {
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: operator %q requires %q to be defined as type = \"list\" in [frontmatter.properties]", opStr, key)
	}
	if strings.TrimSpace(rawVal) == "" {
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: empty value for %q on key %q", opStr, key)
	}

	vals, err := SplitFrontmatterListValues(rawVal, typeFlag)
	if err != nil {
		return FrontmatterMutation{}, err
	}
	if len(vals) == 0 {
		return FrontmatterMutation{}, Errorf(CodeInvalidRequest, "set: empty value for %q on key %q", opStr, key)
	}
	if isWikilink {
		vals = WrapWikilinkFrontmatterValues(vals)
	}

	op := FrontmatterListAdd
	if opStr == "-=" {
		op = FrontmatterListRemove
	}
	return FrontmatterMutation{Key: key, Op: op, Value: vals}, nil
}

// SplitFrontmatterListValues splits a comma-separated list into typed values.
func SplitFrontmatterListValues(rawVal, typeFlag string) ([]any, error) {
	parts := strings.Split(rawVal, ",")
	vals := make([]any, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		value, err := CoerceFrontmatterValue(trimmed, typeFlag)
		if err != nil {
			return nil, err
		}
		vals = append(vals, value)
	}
	return vals, nil
}

// CoerceFrontmatterValue converts raw text into a Go-native frontmatter value.
func CoerceFrontmatterValue(s, typeFlag string) (any, error) {
	switch typeFlag {
	case "date":
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, WrapError(CodeInvalidRequest, fmt.Sprintf("set: invalid date %q", s), err)
		}
		return t, nil
	case "datetime":
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, WrapError(CodeInvalidRequest, fmt.Sprintf("set: invalid datetime %q", s), err)
		}
		return t, nil
	case "int":
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, WrapError(CodeInvalidRequest, fmt.Sprintf("set: invalid int %q", s), err)
		}
		return n, nil
	case "bool":
		switch strings.ToLower(s) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, Errorf(CodeInvalidRequest, "set: invalid bool %q (expected true or false)", s)
	case "float":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, WrapError(CodeInvalidRequest, fmt.Sprintf("set: invalid float %q", s), err)
		}
		return f, nil
	case "raw", "string":
		return s, nil
	case "", "auto":
	default:
		return nil, Errorf(CodeInvalidRequest, "set: unknown --type %q (want date|datetime|int|bool|float|raw)", typeFlag)
	}

	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if intValueRe.MatchString(s) {
		hasLeadingZero := len(s) > 1 && s[0] == '0'
		if s[0] == '-' && len(s) > 2 && s[1] == '0' {
			hasLeadingZero = true
		}
		if !hasLeadingZero {
			if n, err := strconv.Atoi(s); err == nil {
				return n, nil
			}
		}
	}
	if dateValueRe.MatchString(s) {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, nil
		}
	}
	return s, nil
}

var (
	intValueRe  = regexp.MustCompile(`^-?\d+$`)
	dateValueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// WikilinkNormalizedFrontmatterValue wraps string values for wikilink-typed keys.
func WikilinkNormalizedFrontmatterValue(v any, key string, fm *config.FrontmatterConfig) any {
	if fm == nil || !fm.IsWikilink(key) {
		return v
	}

	switch vv := v.(type) {
	case string:
		return WrapWikilinkFrontmatterValue(vv)
	case []any:
		return WrapWikilinkFrontmatterValues(vv)
	default:
		return v
	}
}

// WrapWikilinkFrontmatterValues wraps every string value in `[[...]]`.
func WrapWikilinkFrontmatterValues(vals []any) []any {
	out := make([]any, len(vals))
	for i, item := range vals {
		if s, ok := item.(string); ok {
			out[i] = WrapWikilinkFrontmatterValue(s)
			continue
		}
		out[i] = item
	}
	return out
}

// WrapWikilinkFrontmatterValue wraps a bare string in `[[...]]` unless it is already wrapped.
func WrapWikilinkFrontmatterValue(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s
	}
	return "[[" + s + "]]"
}

// SetFrontmatter applies one or more mutations to a vault-relative note path.
func SetFrontmatter(vaultRoot, sourceRel string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, opts ...FrontmatterOption) (*FrontmatterResult, error) {
	options := defaultFrontmatterOptions("bn web frontmatter set")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	source, err := resolveFrontmatterSource(vaultRoot, sourceRel)
	if err != nil {
		return nil, err
	}
	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	changed, err := applyFrontmatterMutations(vaultRoot, source.AbsPath, mutations, fm, j, options.requires)
	if err != nil {
		return nil, err
	}
	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	return &FrontmatterResult{
		Source:  source,
		Journal: entry,
		Changed: changed,
	}, nil
}

// UnsetFrontmatter removes properties from a vault-relative note path.
func UnsetFrontmatter(vaultRoot, sourceRel string, props []string, fm *config.FrontmatterConfig, opts ...FrontmatterOption) (*FrontmatterResult, error) {
	options := defaultFrontmatterOptions("bn web frontmatter unset")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	source, err := resolveFrontmatterSource(vaultRoot, sourceRel)
	if err != nil {
		return nil, err
	}
	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	changed, err := applyFrontmatterUnset(vaultRoot, source.AbsPath, props, fm, j, options.requires)
	if err != nil {
		return nil, err
	}
	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	return &FrontmatterResult{
		Source:  source,
		Journal: entry,
		Changed: changed,
	}, nil
}

func defaultFrontmatterOptions(command string) frontmatterOptions {
	return frontmatterOptions{command: command}
}

func resolveFrontmatterSource(vaultRoot, sourceRel string) (PathResolution, error) {
	source, err := resolveExistingMutationPath(vaultRoot, mutationTargetPath(vaultRoot, sourceRel), "frontmatter:")
	if err != nil {
		return PathResolution{}, err
	}
	if err := ensureRegularSource(source.AbsPath, "frontmatter:"); err != nil {
		return PathResolution{}, err
	}
	return source, nil
}

// ApplyFrontmatterMutations mutates an already-resolved note path and saves it
// only when the final frontmatter state differs from the initial state. The
// write goes through an in-vault transaction, so it is rejected if the file
// changed on disk since it was parsed.
func ApplyFrontmatterMutations(vaultRoot, path string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, journaler captureJournal) (bool, error) {
	return applyFrontmatterMutations(vaultRoot, path, mutations, fm, journaler, nil)
}

func applyFrontmatterMutations(vaultRoot, path string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, journaler captureJournal, requires []fileRequirement) (bool, error) {
	note, front, err := loadFrontmatterNote(path)
	if err != nil {
		return false, err
	}

	before := snapshotFrontmatterKeys(front, mutations)
	changed, err := ApplyFrontmatterMutationsToMap(front, mutations, fm)
	if err != nil || !changed {
		return false, err
	}
	if !frontmatterMutationsNetChanged(front, before, mutations) {
		return false, nil
	}
	if err := commitNoteWrite(vaultRoot, note, fm, journaler, requires); err != nil {
		return false, err
	}
	return true, nil
}

// commitNoteWrite serializes the mutated note and commits it through an in-vault
// transaction: the precondition pins the exact revision that was parsed, every
// requirement pins a file the write depended on, the journal captures the
// pre-state once, and a failed commit is rolled back.
func commitNoteWrite(vaultRoot string, note *vault.Note, fm *config.FrontmatterConfig, journaler captureJournal, requires []fileRequirement) error {
	data, err := note.Marshal(fm)
	if err != nil {
		return WrapError(CodeInternal, "frontmatter: marshal note", err)
	}
	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return WrapError(CodeInternal, "frontmatter: open transaction", err)
	}
	if err := txn.Write(note.Path, data, 0, vault.ExpectHash(note.SourceHash())); err != nil {
		return classifyTxnError("frontmatter: plan write", err)
	}
	for _, requirement := range requires {
		if err := txn.Require(requirement.path, requirement.expect); err != nil {
			return classifyTxnError("frontmatter: pin dependency", err)
		}
	}
	var txnJournal vault.TxnJournal
	if journaler != nil {
		txnJournal = journaler
	}
	if err := txn.Commit(txnJournal); err != nil {
		return classifyTxnError("frontmatter: save note", err)
	}
	return nil
}

// classifyTxnError maps transaction failures onto the stable operation codes the
// web APIs answer with. A precondition mismatch is a conflict the caller can
// retry; a failed rollback is an internal error whose message already carries
// recovery guidance.
//
// A transaction that left displaced content in a recovery file is checked
// first: it also unwraps to a conflict, and answering "retry" would hide the
// one message telling the caller where their note went.
func classifyTxnError(message string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case vault.IsTxnRecoveryNeeded(err):
		return WrapError(CodeInternal, err.Error(), err)
	case vault.IsTxnConflict(err):
		return WrapError(CodeConflict, MessageOf(err), err)
	case vault.IsTxnRollbackFailure(err):
		return WrapError(CodeInternal, err.Error(), err)
	default:
		return WrapError(CodeInternal, message, err)
	}
}

// ApplyFrontmatterMutationsToMap applies mutations to a frontmatter map in order.
func ApplyFrontmatterMutationsToMap(front *frontmatter.Map, mutations []FrontmatterMutation, fm *config.FrontmatterConfig) (bool, error) {
	changed := false
	for _, mutation := range mutations {
		mutationChanged, err := ApplyFrontmatterMutation(front, mutation, fm)
		if err != nil {
			return false, err
		}
		if mutationChanged {
			changed = true
		}
	}
	return changed, nil
}

// ApplyFrontmatterMutation applies one mutation to a frontmatter map.
func ApplyFrontmatterMutation(front *frontmatter.Map, mutation FrontmatterMutation, fm *config.FrontmatterConfig) (bool, error) {
	if front == nil {
		return false, Errorf(CodeInvalidRequest, "frontmatter: nil map")
	}
	switch mutation.Op {
	case FrontmatterSet:
		newVal := WikilinkNormalizedFrontmatterValue(mutation.Value, mutation.Key, fm)
		existing, present := front.Get(mutation.Key)
		if present && reflect.DeepEqual(existing, newVal) {
			return false, nil
		}
		front.Set(mutation.Key, newVal)
		return true, nil
	case FrontmatterListAdd:
		return applyFrontmatterListMutation(front, mutation.Key, mutation.Value, "+=")
	case FrontmatterListRemove:
		return applyFrontmatterListMutation(front, mutation.Key, mutation.Value, "-=")
	default:
		return false, Errorf(CodeInternal, "set: internal error: unsupported operation for %q", mutation.Key)
	}
}

func applyFrontmatterListMutation(front *frontmatter.Map, key string, value any, op string) (bool, error) {
	vals, ok := value.([]any)
	if !ok {
		return false, Errorf(CodeInternal, "set: internal error: %s value for %q is not a list", op, key)
	}

	var (
		changed bool
		err     error
	)
	if op == "+=" {
		changed, err = front.ListAdd(key, vals...)
	} else {
		changed, err = front.ListRemove(key, vals...)
	}
	if err != nil {
		if errors.Is(err, frontmatter.ErrNotAList) {
			return false, Errorf(CodeConflict, "set: cannot apply %q to %q: existing value is not a list", op, key)
		}
		return false, WrapError(CodeInternal, "set: mutate list value", err)
	}
	return changed, nil
}

// ApplyFrontmatterUnset removes keys from an already-resolved note path.
func ApplyFrontmatterUnset(vaultRoot, path string, props []string, fm *config.FrontmatterConfig, journaler captureJournal) (bool, error) {
	return applyFrontmatterUnset(vaultRoot, path, props, fm, journaler, nil)
}

func applyFrontmatterUnset(vaultRoot, path string, props []string, fm *config.FrontmatterConfig, journaler captureJournal, requires []fileRequirement) (bool, error) {
	if len(props) == 0 {
		return false, Errorf(CodeInvalidRequest, "unset: at least one property name is required")
	}
	for _, p := range props {
		if strings.TrimSpace(p) == "" {
			return false, Errorf(CodeInvalidRequest, "unset: empty property name")
		}
	}

	note, front, err := loadFrontmatterNote(path)
	if err != nil {
		return false, err
	}

	changed := false
	for _, p := range props {
		if front.Has(p) {
			front.Unset(p)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := commitNoteWrite(vaultRoot, note, fm, journaler, requires); err != nil {
		return false, err
	}
	return true, nil
}

func loadFrontmatterNote(path string) (*vault.Note, *frontmatter.Map, error) {
	note := &vault.Note{Path: path}
	if err := note.Load(); err != nil {
		return nil, nil, WrapError(CodeConflict, "frontmatter: load note", err)
	}
	front, err := note.Front()
	if err != nil {
		return nil, nil, WrapError(CodeConflict, "frontmatter: parse note frontmatter", err)
	}
	return note, front, nil
}

func snapshotFrontmatterKeys(front *frontmatter.Map, mutations []FrontmatterMutation) map[string]snapshot {
	out := make(map[string]snapshot, len(mutations))
	for _, mutation := range mutations {
		if _, seen := out[mutation.Key]; seen {
			continue
		}
		v, ok := front.Get(mutation.Key)
		out[mutation.Key] = snapshot{value: v, present: ok}
	}
	return out
}

func orderedFrontmatterKeys(mutations []FrontmatterMutation) []string {
	seen := make(map[string]bool, len(mutations))
	out := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		if seen[mutation.Key] {
			continue
		}
		seen[mutation.Key] = true
		out = append(out, mutation.Key)
	}
	return out
}

func frontmatterMutationsNetChanged(front *frontmatter.Map, before map[string]snapshot, mutations []FrontmatterMutation) bool {
	for _, key := range orderedFrontmatterKeys(mutations) {
		snap := before[key]
		afterVal, afterOK := front.Get(key)
		if !snap.present && !afterOK {
			continue
		}
		if snap.present && afterOK && reflect.DeepEqual(snap.value, afterVal) {
			continue
		}
		return true
	}
	return false
}
