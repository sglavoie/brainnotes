package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// RestoreOptions controls undo behavior.
type RestoreOptions struct {
	Force  bool
	DryRun bool
	// Journal records what the restore is about to change, using the same
	// transaction hook an ordinary mutation uses.
	//
	// An undo is a mutation like any other, and one of the things it can do is
	// remove a note the original command created. The Git safety engine commits a
	// deletion only when an uncommitted journal record proves it, so a restore
	// that journaled nothing would leave an unexplained removal behind and block
	// the vault over a change bn itself made. Passing a recorder here is what
	// keeps an undo explainable — and it is what makes an undo undoable in turn.
	//
	// nil keeps the old behavior and is correct only for a caller that has no
	// vault to protect, which in practice means a test.
	Journal vault.TxnJournal

	// Require pins each recorded path to the state a caller has already shown
	// somebody, keyed by the path exactly as the entry spells it.
	//
	// It exists because of what Force means. Without it the only expectation this
	// package can form is its own reading of disk taken moments earlier inside
	// planRestore (see stageRestoreTarget), which refuses a path that moved
	// between the plan and the commit but knows nothing about what a person was
	// shown before they consented. A caller that asked somebody to authorize
	// replacing specific newer work has to be able to say so *inside the
	// transaction*: any check it performs itself happens before the locks exist
	// and before BeginOperation's Git preflight, and a scheduled writer —
	// bn-vault-sync applying a peer's change, most realistically — can land in
	// that window without changing which paths are in conflict, so no amount of
	// re-planning outside the lock can catch it.
	//
	// A map that is supplied must name every recorded path, since a path left out
	// would fall back to exactly the unpinned behavior the caller supplied it to
	// avoid. nil means no caller-side consent is being enforced, which is right
	// for an unforced restore: that one aborts on any drift regardless.
	Require map[string]vault.FileExpectation
}

// RestoreChange is one path-level transition planned by restore.
type RestoreChange struct {
	Path          string
	CurrentState  string
	RestoredState string
}

// RestorePlan is the deterministic restore preview/apply result.
type RestorePlan struct {
	EntryID   string
	Command   string
	Timestamp time.Time
	Changes   []RestoreChange

	// Cleanup is a housekeeping failure that did not stop the restore: every
	// path was written and is durable, and a transaction temp file outlived the
	// commit.
	//
	// Carried out here rather than returned as the call's error, which is the
	// difference between a leftover file and a wedged vault. noteops.Undo stops
	// at the first error Restore gives it and never reaches Finalize, so
	// returning this would leave the restore's writes on disk with no undo
	// manifest authorizing them and no snapshot recording them — and an undo
	// that removed a tracked note would then look to vaultgit's preflight like a
	// deletion nothing explains, latching blocked.json and refusing every
	// subsequent mutation. Same treatment vaultimport gives it on Result.Cleanup,
	// and for the same reason: this is the one commit failure that means the
	// work succeeded.
	//
	// Always nil for a dry run, which commits nothing.
	Cleanup error
}

// RestoreConflict reports one path whose current state no longer matches the
// journaled post-command ("after") state.
type RestoreConflict struct {
	Path     string
	Expected string
	Actual   string
}

// RestoreConflictError aborts restore before writes when preflight fails.
type RestoreConflictError struct {
	EntryID   string
	Conflicts []RestoreConflict
}

func (e *RestoreConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return "journal: restore conflict"
	}
	parts := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		parts[i] = fmt.Sprintf("%s expected=%s actual=%s", c.Path, c.Expected, c.Actual)
	}
	return fmt.Sprintf("journal: restore %s aborted due to conflicts: %s", e.EntryID, strings.Join(parts, "; "))
}

func describeState(s FileState) string {
	if s.Kind != KindFile {
		return s.Label()
	}
	return fmt.Sprintf("file(sha256=%s,size=%d)", s.SHA256, s.Size)
}

func resolveSnapshotPath(entryDirectory, snapshotRel string) (string, error) {
	snapshotAbs := filepath.Join(entryDirectory, filepath.FromSlash(snapshotRel))
	relToEntry, err := filepath.Rel(entryDirectory, snapshotAbs)
	if err != nil {
		return "", fmt.Errorf("snapshot rel: %w", err)
	}
	if isEscapingRel(relToEntry) || filepath.IsAbs(relToEntry) {
		return "", fmt.Errorf("snapshot escapes entry dir")
	}

	entryResolved, err := filepath.EvalSymlinks(entryDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve entry dir symlinks: %w", err)
	}
	snapshotResolved, err := resolveExistingPath(snapshotAbs)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot symlinks: %w", err)
	}
	relResolved, err := filepath.Rel(entryResolved, snapshotResolved)
	if err != nil {
		return "", fmt.Errorf("snapshot rel resolved: %w", err)
	}
	if isEscapingRel(relResolved) || filepath.IsAbs(relResolved) {
		return "", fmt.Errorf("snapshot escapes entry dir")
	}
	return snapshotResolved, nil
}

type restoreTarget struct {
	record FileRecord
	abs    string
	actual FileState
}

// Restore applies (or previews) one entry.
func Restore(vaultRoot, journalRoot, entryID string, opts RestoreOptions) (*RestorePlan, error) {
	vaultAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("journal: resolve vault root %q: %w", vaultRoot, err)
	}
	entry, err := LoadEntry(journalRoot, entryID)
	if err != nil {
		return nil, err
	}
	entryDirectory, err := entryDir(journalRoot, entryID)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}

	targets, plan, conflicts, err := planRestore(vaultAbs, entry)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !opts.Force {
		return nil, &RestoreConflictError{EntryID: entry.ID, Conflicts: conflicts}
	}
	if opts.DryRun {
		return plan, nil
	}
	if err := applyRestore(vaultAbs, entryDirectory, entry.ID, targets, opts, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// restoreCommitOutcome splits what txn.Commit answered into the housekeeping
// failure a restore survives and the failure it does not.
//
// The cleanup question is asked first and alone, the way vaultimport's
// classifyCommit asks it: *vault.TxnRollbackError and *vault.TxnRecoveryError
// nest the conflict that caused them and vault's predicates are errors.As, so
// the order the arms are asked in is what decides which one is reported — and
// this is the only arm where the bytes are already where the caller asked for
// them. Provoking a real one means arranging an unlink to fail; the
// classification is a pure function of the error, so it is tested as one.
func restoreCommitOutcome(err error) (cleanup, fatal error) {
	switch {
	case err == nil:
		return nil, nil
	case vault.IsTxnCleanupFailure(err):
		return err, nil
	default:
		return nil, err
	}
}

// PreviewRestore answers what restoring one entry would do *and* every path
// whose current state has moved on since the entry was written, in one pass.
//
// Restore already computes both, but it can only ever return one of them: with
// Force off a conflict is the error and the plan is thrown away, and with Force
// on the plan comes back with the conflicts silently dropped. A surface that
// asks somebody to authorize an overriding restore needs the pair — the changes
// it would make, and the newer work it would replace — and asking Restore twice
// would read the same paths under two different stats and could answer about two
// different vaults.
//
// It writes nothing, reads no snapshot, and takes no lock, exactly as a dry run
// does, so previewing stays available on a vault that cannot be written.
func PreviewRestore(vaultRoot, journalRoot, entryID string) (*RestorePlan, []RestoreConflict, error) {
	vaultAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: resolve vault root %q: %w", vaultRoot, err)
	}
	entry, err := LoadEntry(journalRoot, entryID)
	if err != nil {
		return nil, nil, err
	}
	_, plan, conflicts, err := planRestore(vaultAbs, entry)
	if err != nil {
		return nil, nil, err
	}
	return plan, conflicts, nil
}

// planRestore reads what the entry says it left behind and what is on disk now,
// and turns the pair into the plan a caller previews and the targets an apply
// works from.
//
// It reads no snapshot and takes no lock, which is what makes a dry run
// available on a vault that cannot be written at all.
func planRestore(vaultAbs string, entry *Entry) ([]restoreTarget, *RestorePlan, []RestoreConflict, error) {
	targets := make([]restoreTarget, 0, len(entry.Files))
	conflicts := make([]RestoreConflict, 0)
	changes := make([]RestoreChange, 0, len(entry.Files))
	for _, rec := range entry.Files {
		abs, relSlash, err := normalizeWithinRoot(vaultAbs, rec.Path)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("journal: restore path %q: %w", rec.Path, err)
		}
		actual, err := fileStateOnly(abs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("journal: stat %q: %w", relSlash, err)
		}
		if !stillAsRecorded(actual, rec.After) {
			conflicts = append(conflicts, RestoreConflict{
				Path:     rec.Path,
				Expected: describeState(rec.After),
				Actual:   describeState(actual),
			})
		}
		changes = append(changes, RestoreChange{
			Path:          rec.Path,
			CurrentState:  actual.Label(),
			RestoredState: rec.Before.Label(),
		})
		targets = append(targets, restoreTarget{record: rec, abs: abs, actual: actual})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	plan := &RestorePlan{
		EntryID:   entry.ID,
		Command:   entry.Command,
		Timestamp: entry.Timestamp.UTC(),
		Changes:   changes,
	}
	return targets, plan, conflicts, nil
}

// stillAsRecorded reports whether the path is exactly what the entry recorded
// leaving behind.
//
// A directory or a special file never is, whatever it might hash to: the entry
// describes a file, and something else standing in that name is a change nobody
// recorded rather than a content difference.
func stillAsRecorded(actual, recorded FileState) bool {
	if actual.Kind == KindDirectory || actual.Kind == KindOther {
		return false
	}
	return actual.equalsContent(recorded)
}

// applyRestore puts the recorded before-states back, in one transaction.
func applyRestore(vaultAbs, entryDirectory, entryID string, targets []restoreTarget, opts RestoreOptions, plan *RestorePlan) error {
	sort.Slice(targets, func(i, j int) bool { return targets[i].record.Path < targets[j].record.Path })
	// Named here rather than left to the transaction's own preflight so an undo
	// onto refused storage reports the location before it reads a single
	// snapshot. A dry run never reaches this point, so previewing an undo stays
	// available on a vault that cannot be written.
	if err := vault.GuardMutation(vaultAbs, restorePaths(targets)...); err != nil {
		return err
	}
	txn, err := vault.NewFileTxn(vaultAbs)
	if err != nil {
		return fmt.Errorf("journal: restore %s: %w", entryID, err)
	}
	for _, target := range targets {
		if err := stageRestoreTarget(txn, entryDirectory, target, opts.Require); err != nil {
			return err
		}
	}
	// Restore shares the vault's compensating transaction: every snapshot is
	// staged and every precondition re-checked before the first file moves, and
	// a failure mid-commit rolls the already-restored paths back instead of
	// leaving the entry half-applied.
	cleanup, fatal := restoreCommitOutcome(txn.Commit(opts.Journal))
	if fatal != nil {
		return fmt.Errorf("journal: restore %s: %w", entryID, fatal)
	}
	if cleanup != nil {
		// Reported alongside the plan rather than instead of it: see
		// RestorePlan.Cleanup for why an operation that must still be finalized
		// cannot be stopped over a temp file.
		plan.Cleanup = fmt.Errorf("journal: restore %s: %w", entryID, cleanup)
	}
	return nil
}

// stageRestoreTarget plans one path's undo, pinned to the state the restore was
// authorized against.
//
// With no Require map that is this package's own reading of disk rather than the
// recorded one, on purpose: --force is what lets an undo run over a path
// somebody else changed, and the transaction still has to refuse if that path
// changes again between this plan and the commit. With one, it is the caller's
// pin instead, for the reason RestoreOptions.Require gives — a caller that
// obtained consent for specific bytes must not have that consent silently
// re-pointed at whatever the vault held a moment later.
func stageRestoreTarget(txn *vault.FileTxn, entryDirectory string, target restoreTarget, require map[string]vault.FileExpectation) error {
	expect := vault.ExpectMissing()
	if target.actual.Kind == KindFile {
		expect = vault.ExpectHash(target.actual.SHA256)
	}
	if require != nil {
		pinned, ok := require[target.record.Path]
		if !ok {
			return fmt.Errorf("journal: restore %q: no authorized state for this path", target.record.Path)
		}
		expect = pinned
	}
	switch target.record.Before.Kind {
	case KindMissing:
		return stageRestoreRemoval(txn, target, expect)
	case KindFile:
		return stageRestoreContent(txn, entryDirectory, target, expect)
	default:
		return fmt.Errorf("journal: restore %q: unsupported before state %q", target.record.Path, target.record.Before.Kind)
	}
}

// stageRestoreRemoval undoes a creation.
func stageRestoreRemoval(txn *vault.FileTxn, target restoreTarget, expect vault.FileExpectation) error {
	if target.actual.Kind == KindDirectory || target.actual.Kind == KindOther {
		return fmt.Errorf("journal: restore %q: cannot remove %s", target.record.Path, target.actual.Label())
	}
	if err := txn.Remove(target.abs, expect); err != nil {
		return fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
	}
	return nil
}

// stageRestoreContent puts a snapshot back.
func stageRestoreContent(txn *vault.FileTxn, entryDirectory string, target restoreTarget, expect vault.FileExpectation) error {
	snapshotRel := filepath.ToSlash(target.record.Before.Snapshot)
	if strings.TrimSpace(snapshotRel) == "" {
		return fmt.Errorf("journal: restore %q: missing snapshot", target.record.Path)
	}
	snapshotAbs, err := resolveSnapshotPath(entryDirectory, snapshotRel)
	if err != nil {
		return fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
	}
	if target.actual.Kind == KindDirectory || target.actual.Kind == KindOther {
		return fmt.Errorf("journal: restore %q: cannot overwrite %s", target.record.Path, target.actual.Label())
	}
	mode := os.FileMode(target.record.Before.Mode)
	// A snapshot that goes back to an absent path is a create, which is what
	// FileTxn.WriteFrom streams: the snapshot file is already durable, and the
	// digest and size the record pins are what staging verifies the copy against.
	// applyRestore stages every target before it commits any of them, so reading
	// the snapshots instead would make peak residency the sum of the entry — and
	// an entry that committed attachments is exactly the one where that sum is
	// measured in hundreds of megabytes.
	//
	// The overwrite case still reads: replacing content means capturing the bytes
	// it displaces, which is the one thing a streamed change cannot do.
	if !expect.Exists && target.record.Before.SHA256 != "" {
		if err := txn.WriteFrom(target.abs, snapshotAbs, target.record.Before.Size, target.record.Before.SHA256, mode); err != nil {
			return fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
		}
		return nil
	}
	data, err := os.ReadFile(snapshotAbs)
	if err != nil {
		return fmt.Errorf("journal: restore %q: read snapshot: %w", target.record.Path, err)
	}
	if err := txn.Write(target.abs, data, mode, expect); err != nil {
		return fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
	}
	return nil
}

func restorePaths(targets []restoreTarget) []string {
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.abs)
	}
	return paths
}

// IsRestoreConflict reports whether err is a preflight conflict failure.
func IsRestoreConflict(err error) bool {
	var conflictErr *RestoreConflictError
	return errors.As(err, &conflictErr)
}
