package web

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Journal command labels. History and undo read these, so they name the user's
// action rather than the internal endpoint.
const (
	propertySetCommand          = "bn serve property set"
	propertyUnsetCommand        = "bn serve property unset"
	propertyReplaceCommand      = "bn serve property replace value"
	propertyRenameCommand       = "bn serve property rename"
	propertyRepairCommand       = "bn serve property repair"
	propertyListAddCommand      = "bn serve property list add"
	propertySchemaUpsertCommand = "bn serve property schema upsert"
	propertySchemaDeleteCommand = "bn serve property schema delete"
)

// Blocker kinds. Every kind except propertyBlockerRepairSkip is fatal: it
// suppresses the receipt, so the whole selection is refused rather than partly
// written.
const (
	propertyBlockerParseError     = "parse_error"
	propertyBlockerNotFound       = "not_found"
	propertyBlockerKeyConflict    = "key_conflict"
	propertyBlockerConfigConflict = "config_conflict"
	propertyBlockerRepairSkip     = "repair_skip"
)

// propertyBlocker is one reason a planned change cannot proceed, or one issue
// the plan deliberately leaves alone.
type propertyBlocker struct {
	RelPath  string `json:"rel_path,omitempty"`
	Property string `json:"property,omitempty"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

func (b propertyBlocker) fatal() bool { return b.Kind != propertyBlockerRepairSkip }

// propertyPlanNote is one note the plan will rewrite, pinned to the bytes it
// was planned from.
type propertyPlanNote struct {
	RelPath    string
	AbsPath    string
	SourceHash string
	Data       []byte
	Front      *frontmatter.Map
	Preview    noteops.NotePreview
}

// propertyConfigPlan is the planned rewrite of brainnotes.toml.
type propertyConfigPlan struct {
	AbsPath    string
	SourceHash string
	Data       []byte
	Diff       []config.PatchLine
	Properties map[string]config.PropertyDefinition
}

// propertyViolationTotals counts schema violations for the properties a plan
// touches.
type propertyViolationTotals struct {
	Missing int `json:"missing"`
	Type    int `json:"type"`
	Value   int `json:"value"`
	Notes   int `json:"notes"`
}

// propertyViolationProjection contrasts the violations that exist now with the
// ones the plan would leave behind.
type propertyViolationProjection struct {
	Before propertyViolationTotals `json:"before"`
	After  propertyViolationTotals `json:"after"`
}

// propertyPlan is a fully resolved, not-yet-written property change.
type propertyPlan struct {
	Action     propertyActionKind
	Property   string
	NewName    string
	Selected   int
	Notes      []propertyPlanNote
	Blockers   []propertyBlocker
	Config     *propertyConfigPlan
	Projection propertyViolationProjection
}

func (p *propertyPlan) blocked() bool {
	if p == nil {
		return true
	}
	for _, blocker := range p.Blockers {
		if blocker.fatal() {
			return true
		}
	}
	return false
}

func (p *propertyPlan) previews() []noteops.NotePreview {
	out := make([]noteops.NotePreview, 0, len(p.Notes))
	for _, note := range p.Notes {
		out = append(out, note.Preview)
	}
	return out
}

func (p *propertyPlan) fileStates() []propertyPreviewFileState {
	states := make([]propertyPreviewFileState, 0, len(p.Notes)+1)
	for _, note := range p.Notes {
		states = append(states, propertyPreviewFileState{
			RelPath: note.RelPath,
			AbsPath: note.AbsPath,
			Digest:  note.SourceHash,
		})
	}
	if p.Config != nil {
		states = append(states, propertyPreviewFileState{
			RelPath: config.FileName,
			AbsPath: p.Config.AbsPath,
			Digest:  p.Config.SourceHash,
		})
	}
	return states
}

// planPropertyMutation resolves a note-mutation request into the exact bytes it
// would write. It never touches the vault.
func planPropertyMutation(root string, snapshot vaultSnapshot, cfg *config.Config, configPath string, req propertyMutationRequest) (*propertyPlan, error) {
	fm := &cfg.Frontmatter
	def, defined := fm.Definition(req.Property)

	plan := &propertyPlan{
		Action:   req.Action,
		Property: req.Property,
		NewName:  req.NewName,
		Selected: len(req.Notes),
	}

	replacement, err := decodePropertyMutationValue(req, def, defined)
	if err != nil {
		return nil, err
	}
	if req.Action == propertyActionRename && !noteops.ValidFrontmatterKey(req.NewName) {
		return nil, noteops.Errorf(noteops.CodeInvalidKey, "properties: invalid property name %q (allowed: letters, digits, '_', '-')", req.NewName)
	}

	byRelPath := snapshotNotesByRelPath(root, snapshot)
	parseErrors := snapshotParseErrorsByRelPath(root, snapshot)
	overrides := make(map[string]*frontmatter.Map, len(req.Notes))

	for _, relPath := range req.Notes {
		if detail, ok := parseErrors[relPath]; ok {
			plan.Blockers = append(plan.Blockers, propertyBlocker{
				RelPath: relPath,
				Kind:    propertyBlockerParseError,
				Detail:  detail,
			})
			continue
		}
		note, ok := byRelPath[relPath]
		if !ok {
			plan.Blockers = append(plan.Blockers, propertyBlocker{
				RelPath: relPath,
				Kind:    propertyBlockerNotFound,
				Detail:  "note is not in the vault",
			})
			continue
		}

		planned, blockers, err := planPropertyNote(note.Path, relPath, req, fm, replacement)
		if err != nil {
			return nil, err
		}
		plan.Blockers = append(plan.Blockers, blockers...)
		if planned == nil {
			continue
		}
		plan.Notes = append(plan.Notes, *planned)
		overrides[relPath] = planned.Front
	}

	afterProps := fm.Properties
	if req.Action == propertyActionRename {
		configPlan, blocker, err := planPropertyConfigRename(configPath, req.Property, req.NewName, fm)
		if err != nil {
			return nil, err
		}
		if blocker != nil {
			plan.Blockers = append(plan.Blockers, *blocker)
		}
		if configPlan != nil {
			plan.Config = configPlan
			afterProps = configPlan.Properties
		}
	}

	keys := []string{req.Property}
	if req.NewName != "" {
		keys = append(keys, req.NewName)
	}
	plan.Projection = projectPropertyViolations(root, snapshot, fm.Properties, afterProps, keys, overrides)
	return plan, nil
}

// decodePropertyMutationValue validates the requested value once, before any
// note is read, so an invalid value fails the request outright instead of
// becoming a per-note blocker.
func decodePropertyMutationValue(req propertyMutationRequest, def config.PropertyDefinition, defined bool) (any, error) {
	switch req.Action {
	case propertyActionSet:
		return properties.ValidateTypedValue(req.Property, *req.Value, def, defined)
	case propertyActionReplaceValue:
		if defined && def.IsList() {
			return properties.ValidateTypedListItem(req.Property, *req.Value, def, defined)
		}
		return properties.ValidateTypedValue(req.Property, *req.Value, def, defined)
	default:
		return nil, nil
	}
}

// planPropertyNote reads one note and computes its planned rewrite. It returns
// a nil plan when the note needs no change.
func planPropertyNote(absPath, relPath string, req propertyMutationRequest, fm *config.FrontmatterConfig, replacement any) (*propertyPlanNote, []propertyBlocker, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "properties: read note", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, []propertyBlocker{{
			RelPath: relPath,
			Kind:    propertyBlockerParseError,
			Detail:  noteops.MessageOf(err),
		}}, nil
	}

	front := &doc.Front
	changes, blockers, changed, err := applyPropertyAction(front, req, fm, replacement)
	if err != nil {
		return nil, nil, err
	}
	for i := range blockers {
		blockers[i].RelPath = relPath
	}
	if !changed {
		return nil, blockers, nil
	}

	next, err := frontmatter.Marshal(doc, fm)
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "properties: marshal note", err)
	}
	return &propertyPlanNote{
		RelPath:    relPath,
		AbsPath:    absPath,
		SourceHash: vault.HashBytes(data),
		Data:       next,
		Front:      front,
		Preview:    noteops.NotePreview{RelPath: relPath, Changes: changes},
	}, blockers, nil
}

// applyPropertyAction mutates front in place and reports the preview changes it
// made. front belongs to a document parsed for this plan alone, never to a
// cached snapshot note.
func applyPropertyAction(front *frontmatter.Map, req propertyMutationRequest, fm *config.FrontmatterConfig, replacement any) ([]noteops.PreviewChange, []propertyBlocker, bool, error) {
	key := req.Property
	before, present := front.Get(key)

	switch req.Action {
	case propertyActionSet:
		if !properties.WriteTyped(front, key, *req.Value, replacement) {
			return nil, nil, false, nil
		}
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, present, before, true, replacement)}, nil, true, nil

	case propertyActionUnset:
		if !present {
			return nil, nil, false, nil
		}
		front.Unset(key)
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, true, before, false, nil)}, nil, true, nil

	case propertyActionReplaceValue:
		next, kinds, ok := replacePropertyValue(front, key, *req.Match, *req.Value, replacement)
		if !ok {
			return nil, nil, false, nil
		}
		front.Set(key, next)
		properties.RecordTimestampSpelling(front, key, kinds...)
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, true, before, true, next)}, nil, true, nil

	case propertyActionRename:
		if front.Has(req.NewName) {
			return nil, []propertyBlocker{{
				Property: req.NewName,
				Kind:     propertyBlockerKeyConflict,
				Detail:   "note already has a " + req.NewName + " property",
			}}, false, nil
		}
		renamed, err := front.RenameKey(key, req.NewName)
		if err != nil {
			if errors.Is(err, frontmatter.ErrKeyConflict) {
				return nil, []propertyBlocker{{
					Property: req.NewName,
					Kind:     propertyBlockerKeyConflict,
					Detail:   "note already has a " + req.NewName + " property",
				}}, false, nil
			}
			return nil, nil, false, noteops.WrapError(noteops.CodeInvalidRequest, "properties: rename property", err)
		}
		if !renamed {
			return nil, nil, false, nil
		}
		return []noteops.PreviewChange{
			noteops.FrontmatterChange(key, true, before, false, nil),
			noteops.FrontmatterChange(req.NewName, false, nil, true, before),
		}, nil, true, nil

	case propertyActionRepair:
		return applyPropertyRepair(front, key, fm)
	}
	return nil, nil, false, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unsupported action %q", string(req.Action))
}

// applyPropertyRepair runs the shared schema repair planner and keeps only the
// action for the requested property. Skips are advisory: they explain what
// repair deliberately would not touch.
func applyPropertyRepair(front *frontmatter.Map, key string, fm *config.FrontmatterConfig) ([]noteops.PreviewChange, []propertyBlocker, bool, error) {
	result := schema.PlanRepairs(front, fm.Properties)

	var blockers []propertyBlocker
	for _, skip := range result.Skips {
		if skip.Property != key {
			continue
		}
		blockers = append(blockers, propertyBlocker{
			Property: key,
			Kind:     propertyBlockerRepairSkip,
			Detail:   skip.Kind + ": " + skip.Detail,
		})
	}

	for _, action := range result.Actions {
		if action.Property != key {
			continue
		}
		schema.ApplyRepairAction(front, action, fm.Properties)
		return []noteops.PreviewChange{
			noteops.FrontmatterChange(key, true, action.OldValue, true, action.NewValue),
		}, blockers, true, nil
	}
	return nil, blockers, false, nil
}

// replacePropertyValue swaps every occurrence of the matched logical value for
// replacement. Matching is by classified kind and text — the exact pair the
// workbench displayed — so the browser and the server agree on what the user
// selected without either side guessing at Go-native equality.
//
// It also reports the kind of every value in the result: a replaced element
// takes the requested kind and a kept element keeps its current one, which is
// what lets the caller restore the timestamp spelling that Set drops.
func replacePropertyValue(front *frontmatter.Map, key string, match, value properties.TypedValue, replacement any) (any, []properties.Kind, bool) {
	current, ok := properties.ClassifyProperty(front, key)
	if !ok {
		return nil, nil, false
	}
	raw, _ := front.Get(key)

	if !current.IsList() {
		if current.Kind != match.Kind || current.Text != match.Text {
			return nil, nil, false
		}
		return replacement, []properties.Kind{value.Kind}, true
	}

	items, ok := raw.([]any)
	if !ok || len(items) != len(current.Items) {
		return nil, nil, false
	}
	next := make([]any, len(items))
	kinds := make([]properties.Kind, len(items))
	replaced := false
	for i, item := range current.Items {
		if item.Kind == match.Kind && item.Text == match.Text {
			next[i] = replacement
			kinds[i] = value.Kind
			replaced = true
			continue
		}
		next[i] = items[i]
		kinds[i] = item.Kind
	}
	if !replaced {
		return nil, nil, false
	}
	return next, kinds, true
}

// planPropertyConfigRename retargets the property's definition block. A rename
// onto a declared name is a collision, never a merge.
func planPropertyConfigRename(configPath, property, newName string, fm *config.FrontmatterConfig) (*propertyConfigPlan, *propertyBlocker, error) {
	if _, declared := fm.Definition(property); !declared {
		return nil, nil, nil
	}
	if _, taken := fm.Definition(newName); taken {
		return nil, &propertyBlocker{
			Property: newName,
			Kind:     propertyBlockerConfigConflict,
			Detail:   newName + " is already declared in " + config.FileName,
		}, nil
	}

	source, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "properties: read "+config.FileName, err)
	}
	result, err := config.PatchProperties(source, []config.PropertyPatch{{
		Op:     config.PatchRename,
		Key:    property,
		NewKey: newName,
	}})
	if err != nil {
		return nil, &propertyBlocker{
			Property: newName,
			Kind:     propertyBlockerConfigConflict,
			Detail:   err.Error(),
		}, nil
	}
	if !result.Changed {
		return nil, nil, nil
	}

	props := make(map[string]config.PropertyDefinition, len(fm.Properties))
	for name, def := range fm.Properties {
		if name == property {
			props[newName] = def
			continue
		}
		props[name] = def
	}
	return &propertyConfigPlan{
		AbsPath:    configPath,
		SourceHash: vault.HashBytes(source),
		Data:       result.Patched,
		Diff:       result.Diff,
		Properties: props,
	}, nil, nil
}

// projectPropertyViolations counts the violations of the named properties
// across the whole vault, before and after the plan. Notes the plan rewrites
// are counted from their planned frontmatter; every other note is counted as it
// stands, so a definition change is measured against the notes it will govern.
func projectPropertyViolations(root string, snapshot vaultSnapshot, before, after map[string]config.PropertyDefinition, keys []string, overrides map[string]*frontmatter.Map) propertyViolationProjection {
	interesting := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key != "" {
			interesting[key] = struct{}{}
		}
	}

	var projection propertyViolationProjection
	for _, note := range snapshot.notes {
		front, err := note.Front()
		if err != nil || front == nil {
			continue
		}
		relPath := noteVaultPath(root, note)
		tallyPropertyViolations(&projection.Before, front, relPath, before, interesting)

		afterFront := front
		if override, ok := overrides[relPath]; ok {
			afterFront = override
		}
		tallyPropertyViolations(&projection.After, afterFront, relPath, after, interesting)
	}
	return projection
}

func tallyPropertyViolations(totals *propertyViolationTotals, front *frontmatter.Map, relPath string, props map[string]config.PropertyDefinition, interesting map[string]struct{}) {
	counted := false
	for _, violation := range schema.ValidateNote(front, relPath, props) {
		if _, ok := interesting[violation.Property]; !ok {
			continue
		}
		switch violation.Kind {
		case "missing":
			totals.Missing++
		case "type":
			totals.Type++
		case "value":
			totals.Value++
		}
		if !counted {
			totals.Notes++
			counted = true
		}
	}
}

func snapshotNotesByRelPath(root string, snapshot vaultSnapshot) map[string]*vault.Note {
	out := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		relPath := noteVaultPath(root, note)
		if relPath == "" {
			continue
		}
		out[relPath] = note
	}
	return out
}

func snapshotParseErrorsByRelPath(root string, snapshot vaultSnapshot) map[string]string {
	out := make(map[string]string, len(snapshot.parseErrors))
	for _, failure := range snapshot.parseErrors {
		rel, err := filepath.Rel(root, failure.Path)
		if err != nil {
			continue
		}
		out[filepath.ToSlash(rel)] = failure.Error
	}
	return out
}

// commitPropertyPlan writes every planned file in one transaction. Each write
// is pinned to the bytes the plan was computed from, so a concurrent edit is a
// conflict, and a failure mid-commit rolls the earlier writes back. pin adds the
// configuration the plan was validated against to that set of preconditions.
func (s *Server) commitPropertyPlan(plan *propertyPlan, command string, pin configPin) (*journal.Entry, error) {
	if plan == nil || (len(plan.Notes) == 0 && plan.Config == nil) {
		return nil, nil
	}
	txn, err := vault.NewFileTxn(s.vault.Root)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "properties: open transaction", err)
	}
	for _, note := range plan.Notes {
		if err := txn.Write(note.AbsPath, note.Data, 0, vault.ExpectHash(note.SourceHash)); err != nil {
			return nil, classifyPropertyTxnError("properties: plan write", err)
		}
	}
	if plan.Config != nil {
		if err := txn.Write(plan.Config.AbsPath, plan.Config.Data, 0, vault.ExpectHash(plan.Config.SourceHash)); err != nil {
			return nil, classifyPropertyTxnError("properties: plan configuration write", err)
		}
	}
	if err := pinPlanConfig(txn, plan, pin); err != nil {
		return nil, err
	}

	entry := noteops.NewJournal(s.vault.Root, command)
	if s.beforePropertyCommit != nil {
		s.beforePropertyCommit()
	}
	if err := txn.Commit(entry); err != nil {
		return nil, classifyPropertyTxnError("properties: write", err)
	}
	return entry.Finalize()
}

// pinPlanConfig ties the transaction to the brainnotes.toml the plan was
// validated against. A plan that rewrites that file already pins it through its
// own write precondition and only has to agree with the pin; every other plan
// takes a read-only precondition, which is what stops a note-only write from
// landing under declarations that were replaced after planning.
func pinPlanConfig(txn *vault.FileTxn, plan *propertyPlan, pin configPin) error {
	if !pin.tracked() {
		return nil
	}
	if plan.Config != nil {
		if plan.Config.SourceHash != pin.Hash {
			return noteops.Errorf(noteops.CodeConflict,
				"properties: %s changed while the change was being planned", config.FileName)
		}
		return nil
	}
	if err := txn.Require(pin.Path, pin.expectation()); err != nil {
		return noteops.WrapError(noteops.CodeInternal, "properties: pin "+config.FileName, err)
	}
	return nil
}

// classifyPropertyTxnError maps transaction failures onto the stable API codes.
// A precondition mismatch is a conflict the caller can retry after a fresh
// preview; a failed rollback is internal and its message carries the recovery
// hint.
//
// A transaction that left displaced content in a recovery file is checked
// first: it also unwraps to a conflict, and answering "retry" would hide the
// one message telling the caller where their note went.
func classifyPropertyTxnError(message string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case vault.IsTxnRecoveryNeeded(err):
		return noteops.WrapError(noteops.CodeInternal, err.Error(), err)
	case vault.IsTxnConflict(err):
		return noteops.WrapError(noteops.CodeConflict, noteops.MessageOf(err), err)
	case vault.IsTxnRollbackFailure(err):
		return noteops.WrapError(noteops.CodeInternal, err.Error(), err)
	default:
		return noteops.WrapError(noteops.CodeInternal, message, err)
	}
}

// propertyMutationCommand names the journal entry for one action.
func propertyMutationCommand(action propertyActionKind) string {
	switch action {
	case propertyActionSet:
		return propertySetCommand
	case propertyActionUnset:
		return propertyUnsetCommand
	case propertyActionReplaceValue:
		return propertyReplaceCommand
	case propertyActionRename:
		return propertyRenameCommand
	case propertyActionRepair:
		return propertyRepairCommand
	}
	return "bn serve property"
}

// propertyConfigDiffLine is one previewable line of the brainnotes.toml change.
type propertyConfigDiffLine struct {
	Kind string `json:"kind"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func propertyConfigDiffLines(diff []config.PatchLine) []propertyConfigDiffLine {
	out := make([]propertyConfigDiffLine, 0, len(diff))
	for _, line := range diff {
		out = append(out, propertyConfigDiffLine{
			Kind: string(line.Kind),
			Line: line.Line,
			Text: line.Text,
		})
	}
	return out
}

func sortedPropertyBlockers(blockers []propertyBlocker) []propertyBlocker {
	out := append([]propertyBlocker(nil), blockers...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RelPath != out[j].RelPath {
			return out[i].RelPath < out[j].RelPath
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Detail < out[j].Detail
	})
	if out == nil {
		return []propertyBlocker{}
	}
	return out
}

// propertyBlockerSummary renders the fatal blockers into one refusal message.
func propertyBlockerSummary(blockers []propertyBlocker) string {
	details := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if !blocker.fatal() {
			continue
		}
		if blocker.RelPath != "" {
			details = append(details, blocker.RelPath+": "+blocker.Detail)
			continue
		}
		details = append(details, blocker.Detail)
	}
	return strings.Join(details, "; ")
}
