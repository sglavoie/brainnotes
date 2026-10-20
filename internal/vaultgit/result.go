package vaultgit

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// CommitResult describes what a snapshot did. It is safe to log in full: it
// carries paths and object names, never contents.
type CommitResult struct {
	// Committed reports whether a new commit was created. A snapshot that finds
	// nothing to record is a success with Committed false.
	Committed bool
	// Commit is the new commit's object name, when one was created.
	Commit string
	// Parent is the commit the snapshot was built on, empty for a root commit.
	Parent string
	// Branch is the full ref the snapshot advanced.
	Branch string
	// Message is the commit message that was used.
	Message string
	// Added lists the vault-relative paths whose content was recorded.
	Added []string
	// Deleted lists the vault-relative paths whose removal was recorded. Every
	// one of them was proven by an uncommitted finalized journal record.
	Deleted []string
	// Skipped lists paths the engine could not read and therefore left out. A
	// skip never removes anything from the history, but it does suppress every
	// deletion in the same pass — see commitPending.
	//
	// Only an external-change snapshot can produce one. An exact snapshot fails
	// closed instead, so a caller that named its paths is never handed a success
	// that quietly omits one of them.
	Skipped []SkippedPath
	// DeferredDeletions lists proven deletions that were held back because
	// something else in the same pass was skipped.
	DeferredDeletions []string
	// landedAt is the first clock reading after the ref update was observed to
	// succeed and its containing directory was synced. It stays internal because
	// callers need only the commit verdict; the external floor protocol uses it
	// to record the completion boundary.
	landedAt time.Time
	// refDurable reports that the ref directory sync after the update succeeded.
	// When it is false the recovery intent is kept for the next recovery pass.
	refDurable bool
}

// Unrecorded reports the paths this snapshot was asked to record and did not:
// everything it skipped, plus every proven deletion it held back behind a skip.
// It is empty for a complete snapshot, including one that found nothing to do.
//
// An exact snapshot can never return a non-empty answer, because the engine
// refuses rather than reporting an unstaged required path. Exact callers check
// it anyway, and the redundancy is the point: the guarantee they depend on is
// that content the history does not hold is never reported as recorded, and a
// caller that confirms its own result does not have to take that on trust.
func (r CommitResult) Unrecorded() []string {
	if len(r.Skipped) == 0 && len(r.DeferredDeletions) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Skipped)+len(r.DeferredDeletions))
	for _, skip := range r.Skipped {
		out = append(out, skip.Path)
	}
	out = append(out, r.DeferredDeletions...)
	sort.Strings(out)
	return out
}

// SkippedPath is one path a snapshot could not read.
type SkippedPath struct {
	// Path is vault-relative.
	Path string
	// Reason is a sanitized single-line diagnostic from git.
	Reason string
}

// skippedPaths reduces skips to their paths, for a block record.
func skippedPaths(skipped []SkippedPath) []string {
	out := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		out = append(out, skip.Path)
	}
	return out
}

// skippedError joins the skips into one diagnostic. Every part of it is a vault
// path or a sanitized single line from git, so it stays as safe to log as the
// block record it ends up in.
func skippedError(skipped []SkippedPath) error {
	errs := make([]error, 0, len(skipped))
	for _, skip := range skipped {
		errs = append(errs, fmt.Errorf("%s: %s", skip.Path, skip.Reason))
	}
	return errors.Join(errs...)
}
