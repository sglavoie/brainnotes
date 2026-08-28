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

	targets := make([]restoreTarget, 0, len(entry.Files))
	conflicts := make([]RestoreConflict, 0)
	changes := make([]RestoreChange, 0, len(entry.Files))
	for _, rec := range entry.Files {
		abs, relSlash, err := normalizeWithinRoot(vaultAbs, rec.Path)
		if err != nil {
			return nil, fmt.Errorf("journal: restore path %q: %w", rec.Path, err)
		}
		actual, _, err := fileStateFromDisk(abs, false)
		if err != nil {
			return nil, fmt.Errorf("journal: stat %q: %w", relSlash, err)
		}
		if actual.Kind == KindDirectory || actual.Kind == KindOther {
			conflicts = append(conflicts, RestoreConflict{
				Path:     rec.Path,
				Expected: describeState(rec.After),
				Actual:   describeState(actual),
			})
		} else if !actual.equalsContent(rec.After) {
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
		targets = append(targets, restoreTarget{
			record: rec,
			abs:    abs,
			actual: actual,
		})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	plan := &RestorePlan{
		EntryID:   entry.ID,
		Command:   entry.Command,
		Timestamp: entry.Timestamp.UTC(),
		Changes:   changes,
	}

	if len(conflicts) > 0 && !opts.Force {
		return nil, &RestoreConflictError{EntryID: entry.ID, Conflicts: conflicts}
	}
	if opts.DryRun {
		return plan, nil
	}

	sort.Slice(targets, func(i, j int) bool { return targets[i].record.Path < targets[j].record.Path })
	// Named here rather than left to the transaction's own preflight so an undo
	// onto refused storage reports the location before it reads a single
	// snapshot. A dry run never reaches this point, so previewing an undo stays
	// available on a vault that cannot be written.
	if err := vault.GuardMutation(vaultAbs, restorePaths(targets)...); err != nil {
		return nil, err
	}
	txn, err := vault.NewFileTxn(vaultAbs)
	if err != nil {
		return nil, fmt.Errorf("journal: restore %s: %w", entry.ID, err)
	}
	for _, target := range targets {
		expect := vault.ExpectMissing()
		if target.actual.Kind == KindFile {
			expect = vault.ExpectHash(target.actual.SHA256)
		}
		switch target.record.Before.Kind {
		case KindMissing:
			if target.actual.Kind == KindDirectory || target.actual.Kind == KindOther {
				return nil, fmt.Errorf("journal: restore %q: cannot remove %s", target.record.Path, target.actual.Label())
			}
			if err := txn.Remove(target.abs, expect); err != nil {
				return nil, fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
			}
		case KindFile:
			snapshotRel := filepath.ToSlash(target.record.Before.Snapshot)
			if strings.TrimSpace(snapshotRel) == "" {
				return nil, fmt.Errorf("journal: restore %q: missing snapshot", target.record.Path)
			}
			snapshotAbs, err := resolveSnapshotPath(entryDirectory, snapshotRel)
			if err != nil {
				return nil, fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
			}
			data, err := os.ReadFile(snapshotAbs)
			if err != nil {
				return nil, fmt.Errorf("journal: restore %q: read snapshot: %w", target.record.Path, err)
			}
			if target.actual.Kind == KindDirectory || target.actual.Kind == KindOther {
				return nil, fmt.Errorf("journal: restore %q: cannot overwrite %s", target.record.Path, target.actual.Label())
			}
			if err := txn.Write(target.abs, data, os.FileMode(target.record.Before.Mode), expect); err != nil {
				return nil, fmt.Errorf("journal: restore %q: %w", target.record.Path, err)
			}
		default:
			return nil, fmt.Errorf("journal: restore %q: unsupported before state %q", target.record.Path, target.record.Before.Kind)
		}
	}
	// Restore shares the vault's compensating transaction: every snapshot is
	// staged and every precondition re-checked before the first file moves, and
	// a failure mid-commit rolls the already-restored paths back instead of
	// leaving the entry half-applied.
	if err := txn.Commit(nil); err != nil {
		return nil, fmt.Errorf("journal: restore %s: %w", entry.ID, err)
	}
	return plan, nil
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
