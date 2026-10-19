package noteops

import (
	"errors"
	"reflect"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func SetFrontmatter(vaultRoot, sourceRel string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, opts ...FrontmatterOption) (*FrontmatterResult, error) {
	options := newFrontmatterOptions("bn web frontmatter set", opts)

	source, err := resolveFrontmatterSource(vaultRoot, sourceRel)
	if err != nil {
		return nil, err
	}
	j, err := BeginOperation(vaultRoot, "frontmatter-set", options.command, []string{source.AbsPath}, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
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
	options := newFrontmatterOptions("bn web frontmatter unset", opts)

	source, err := resolveFrontmatterSource(vaultRoot, sourceRel)
	if err != nil {
		return nil, err
	}
	j, err := BeginOperation(vaultRoot, "frontmatter-unset", options.command, []string{source.AbsPath}, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
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

// newFrontmatterOptions folds the variadic options onto the defaults for
// command, skipping the nil ones a conditional caller passes.
func newFrontmatterOptions(command string, opts []FrontmatterOption) frontmatterOptions {
	options := defaultFrontmatterOptions(command)
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
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
		return ClassifyTxnError("frontmatter: plan write", err)
	}
	for _, requirement := range requires {
		if err := txn.Require(requirement.path, requirement.expect); err != nil {
			return ClassifyTxnError("frontmatter: pin dependency", err)
		}
	}
	var txnJournal vault.TxnJournal
	if journaler != nil {
		txnJournal = journaler
	}
	if err := txn.Commit(txnJournal); err != nil {
		return ClassifyTxnError("frontmatter: save note", err)
	}
	return nil
}

// ClassifyTxnError maps transaction failures onto the stable operation codes the
// web APIs answer with. A precondition mismatch is a conflict the caller can
// retry; a failed rollback is an internal error whose message already carries
// recovery guidance.
//
// A transaction that left displaced content in a recovery file is checked
// first: it also unwraps to a conflict, and answering "retry" would hide the
// one message telling the caller where their note went.
//
// A lock timeout is reported as a conflict too. Nothing changed, so the caller's
// plan is still valid, but "retry" is the correct advice and conflict is the code
// callers already retry on.
//
// A refused or half-finished replacement keeps its own message rather than the
// generic one. Neither is retryable and neither is the caller's fault: one says
// this volume cannot be written to safely at all, the other names the file
// holding content that is nowhere else. Replacing either with "frontmatter: save
// note" would throw away the only sentence that tells a person what to do.
//
// A refusal by the mutation guard is checked first and is forbidden rather than
// internal: nothing failed and nothing is broken — the vault is somewhere writes
// are not allowed to land. It is the one class here that is a policy answer, so
// it maps to 403 on the web and keeps its own message on the CLI.
func ClassifyTxnError(message string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case vault.IsForbiddenMutation(err):
		return WrapError(CodeForbidden, err.Error(), err)
	case vault.IsTxnRecoveryNeeded(err):
		return WrapError(CodeInternal, err.Error(), err)
	case vault.IsTxnRollbackFailure(err):
		return WrapError(CodeInternal, err.Error(), err)
	case vault.IsTxnLockBusy(err), vault.IsTxnConflict(err):
		return WrapError(CodeConflict, MessageOf(err), err)
	case vault.IsPartialReplacement(err), vault.IsUnsafeReplacement(err):
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
