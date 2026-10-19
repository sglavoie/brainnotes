package web

import (
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
func planPropertyConfigRename(root, configPath, property, newName string, fm *config.FrontmatterConfig) (*propertyConfigPlan, *propertyBlocker, error) {
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

	sources, localPin, err := planConfigSources(root)
	if err != nil {
		return nil, nil, err
	}
	source := sources.Portable
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
		Local:      localPin,
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
func (s *Server) commitPropertyPlan(plan *propertyPlan, operation, command string, pin configPin, opts ...journal.RecorderOption) (*journal.Entry, error) {
	if plan == nil || (len(plan.Notes) == 0 && plan.Config == nil) {
		return nil, nil
	}
	// Before the transaction rather than just before its commit: the location and
	// Git safety refusals both belong ahead of the first artifact, and a property
	// plan's first artifact is the transaction's staged temp files.
	entry, err := noteops.BeginOperation(s.vault.Root, operation, command, propertyPlanPaths(plan), opts...)
	if err != nil {
		return nil, err
	}
	defer entry.Release()
	txn, err := vault.NewFileTxn(s.vault.Root)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "properties: open transaction", err)
	}
	if err := stagePropertyPlan(txn, plan, pin); err != nil {
		return nil, err
	}

	if s.beforePropertyCommit != nil {
		s.beforePropertyCommit()
	}
	if err := txn.Commit(entry); err != nil {
		return nil, classifyPropertyTxnError("properties: write", err)
	}
	return entry.Finalize()
}

// stagePropertyPlan stages every planned write onto txn, each pinned to the
// bytes the plan was computed from, and then pins the configuration the plan was
// validated against. Nothing is written until the caller commits.
func stagePropertyPlan(txn *vault.FileTxn, plan *propertyPlan, pin configPin) error {
	for _, note := range plan.Notes {
		if err := txn.Write(note.AbsPath, note.Data, 0, vault.ExpectHash(note.SourceHash)); err != nil {
			return classifyPropertyTxnError("properties: plan write", err)
		}
	}
	if plan.Config != nil {
		if err := txn.Write(plan.Config.AbsPath, plan.Config.Data, 0, vault.ExpectHash(plan.Config.SourceHash)); err != nil {
			return classifyPropertyTxnError("properties: plan configuration write", err)
		}
	}
	return pinPlanConfig(txn, plan, pin)
}

// propertyPlanPaths lists every file a property plan will write, so the
// operation's preflight names them before anything is staged.
func propertyPlanPaths(plan *propertyPlan) []string {
	paths := make([]string, 0, len(plan.Notes)+1)
	for _, note := range plan.Notes {
		paths = append(paths, note.AbsPath)
	}
	if plan.Config != nil {
		paths = append(paths, plan.Config.AbsPath)
	}
	return paths
}

// pinPlanConfig ties the transaction to the configuration the plan was validated
// against — both layers, because either one decides what a declaration means.
// The file a plan rewrites is already pinned by its own write precondition and
// only has to agree with the pin; every other layer takes a read-only
// precondition, which is what stops a note-only write from landing under
// declarations that were replaced after planning.
//
// A plan disagreeing with the pin is a race between publication and planning
// rather than a race at the commit boundary, so it is refused here instead of
// being handed to the transaction, which would have nothing to compare it to.
func pinPlanConfig(txn *vault.FileTxn, plan *propertyPlan, pin configPin) error {
	if !pin.tracked() {
		return nil
	}
	written := ""
	if plan.Config != nil {
		if plan.Config.SourceHash != pin.Portable.Hash {
			return configPlanRaceError(config.FileName)
		}
		if plan.Config.Local != pin.Local {
			return configPlanRaceError(config.LocalFileName)
		}
		written = plan.Config.AbsPath
	}
	for _, layer := range pin.layers() {
		if layer.Path == written {
			continue
		}
		if err := txn.Require(layer.Path, layer.expectation()); err != nil {
			return noteops.WrapError(noteops.CodeInternal, "properties: pin "+layer.name(), err)
		}
	}
	return nil
}

func configPlanRaceError(name string) error {
	return noteops.Errorf(noteops.CodeConflict,
		"properties: %s changed while the change was being planned", name)
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
	case vault.IsForbiddenMutation(err):
		// A location the policy refuses is not a malfunction, so it is a 403 with
		// its own message rather than a 500 with a generic one.
		return noteops.WrapError(noteops.CodeForbidden, err.Error(), err)
	case vault.IsTxnRecoveryNeeded(err):
		return noteops.WrapError(noteops.CodeInternal, err.Error(), err)
	case vault.IsTxnRollbackFailure(err):
		return noteops.WrapError(noteops.CodeInternal, err.Error(), err)
	case vault.IsTxnConflict(err):
		return noteops.WrapError(noteops.CodeConflict, noteops.MessageOf(err), err)
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
