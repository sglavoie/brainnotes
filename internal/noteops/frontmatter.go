package noteops

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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

// FrontmatterResult describes a completed note frontmatter update. Single-note
// callers read Source; SetFrontmatterAtomic fills Sources with every note it
// resolved, in request order, and leaves Source zero.
type FrontmatterResult struct {
	Source  PathResolution
	Sources []PathResolution
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
	ctx            context.Context
	requires       []fileRequirement
	// lockEscalation overrides the transaction's production-derived threshold.
	// Only tests set it so an atomic caller in another package can exercise the
	// vault-wide lock path with a small fixture.
	lockEscalation int
	// beforeAtomicCommit runs after SetFrontmatterAtomic has planned every note
	// and before the transaction stages anything. Only a test sets it: the window
	// where a pinned hash goes stale is not otherwise reachable from outside the
	// call, and it is the window the whole-set refusal depends on.
	beforeAtomicCommit func() error
}

// FrontmatterOption customizes frontmatter mutations.
type FrontmatterOption func(*frontmatterOptions)

// WithFrontmatterCommand sets the command stored in the undo journal entry.
func WithFrontmatterCommand(command string) FrontmatterOption {
	return func(o *frontmatterOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithFrontmatterContext makes the operation honour ctx, so a CLI interrupt
// cancels it at the same boundaries as every other journaled write.
func WithFrontmatterContext(ctx context.Context) FrontmatterOption {
	return func(o *frontmatterOptions) {
		o.ctx = ctx
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

// WithFrontmatterLockEscalationForTest overrides the atomic transaction's
// production-derived lock-escalation threshold. It is intended only for tests
// that need to cover the vault-wide lock path without creating thousands of
// note files; non-positive values are ignored.
func WithFrontmatterLockEscalationForTest(threshold int) FrontmatterOption {
	return func(o *frontmatterOptions) {
		if threshold > 0 {
			o.lockEscalation = threshold
		}
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
// A forced type (--value-type) is applied strictly; otherwise the value is
// inferred: bool, then a plain integer, then a YYYY-MM-DD date, else string.
func CoerceFrontmatterValue(s, typeFlag string) (any, error) {
	if typeFlag != "" && typeFlag != "auto" {
		return coerceForcedValue(s, typeFlag)
	}
	return coerceInferredValue(s), nil
}

// coerceForcedValue parses s as exactly the type typeFlag names.
func coerceForcedValue(s, typeFlag string) (any, error) {
	switch typeFlag {
	case "date":
		return parseForced(s, "date", func(v string) (any, error) { return time.Parse("2006-01-02", v) })
	case "datetime":
		return parseForced(s, "datetime", func(v string) (any, error) { return time.Parse(time.RFC3339, v) })
	case "int":
		return parseForced(s, "int", func(v string) (any, error) { return strconv.Atoi(v) })
	case "float":
		return parseForced(s, "float", func(v string) (any, error) { return strconv.ParseFloat(v, 64) })
	case "bool":
		if b, ok := parseBoolWord(s); ok {
			return b, nil
		}
		return nil, Errorf(CodeInvalidRequest, "set: invalid bool %q (expected true or false)", s)
	case "raw", "string":
		return s, nil
	default:
		// The wording matches the CLI's enumValueError: <cmd>: --<flag> must be a, b, or c (got "x").
		return nil, Errorf(CodeInvalidRequest, "set: --value-type must be date, datetime, int, bool, float, or raw (got %q)", typeFlag)
	}
}

// parseForced runs parse and reports a failure as `set: invalid <kind> "s"`.
func parseForced(s, kind string, parse func(string) (any, error)) (any, error) {
	v, err := parse(s)
	if err != nil {
		return nil, WrapError(CodeInvalidRequest, fmt.Sprintf("set: invalid %s %q", kind, s), err)
	}
	return v, nil
}

func parseBoolWord(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// coerceInferredValue infers a type for s. An integer with a leading zero
// stays a string, so identifiers like 007 survive a round trip.
func coerceInferredValue(s string) any {
	if b, ok := parseBoolWord(s); ok {
		return b
	}
	if n, ok := inferredInt(s); ok {
		return n
	}
	if dateValueRe.MatchString(s) {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
	}
	return s
}

func inferredInt(s string) (int, bool) {
	if !intValueRe.MatchString(s) {
		return 0, false
	}
	hasLeadingZero := len(s) > 1 && s[0] == '0'
	if s[0] == '-' && len(s) > 2 && s[1] == '0' {
		hasLeadingZero = true
	}
	if hasLeadingZero {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
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
