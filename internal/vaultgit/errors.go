package vaultgit

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnmanaged reports a vault that carries no Git repository of its own.
//
// It is deliberately not a safety failure. A vault with no `.git` directory is
// simply not under content history — a fresh vault, a test fixture, a copy
// someone made with `cp -r` — and every caller is expected to treat it as
// "nothing to snapshot" rather than as damage. A vault that has a `.git`
// directory but cannot be used is the opposite case and returns a *SafetyError.
var ErrUnmanaged = errors.New("vaultgit: vault has no Git repository")

// Kind classifies a safety failure. It is a stable machine token: it is written
// into blocked.json and is what a caller switches on, so the string values are
// part of the on-disk format and must not be renamed casually.
type Kind string

const (
	// KindGitUnavailable means the git executable could not be found or run.
	KindGitUnavailable Kind = "git_unavailable"
	// KindRepositoryState means the repository is not in a state this engine
	// will touch: an unborn or detached HEAD, a `.git` file rather than a
	// directory, a merge/rebase/cherry-pick in progress, an unmerged index, or a
	// working tree whose top level is not the vault root.
	KindRepositoryState Kind = "repository_state"
	// KindBlocked means a previous failure recorded blocked.json and no mutation
	// snapshot will be attempted until a person clears it.
	KindBlocked Kind = "blocked"
	// KindBusy means another process holds the safety lock. It is retryable and
	// says nothing about the vault's health.
	KindBusy Kind = "busy"
	// KindUnexplainedDeletion means a tracked path is gone from the working tree
	// and no uncommitted finalized journal record proves the transition.
	KindUnexplainedDeletion Kind = "unexplained_deletion"
	// KindAmbiguousChange means Git reported a pending change this engine will
	// not classify deterministically — a type change, a rename pair, or an
	// unrecognized status code.
	KindAmbiguousChange Kind = "ambiguous_change"
	// KindRefConflict means the branch ref moved under a compare-and-swap and
	// the operation could not be retried safely.
	KindRefConflict Kind = "ref_conflict"
	// KindCommitFailed means object writing, tree writing, or the ref update
	// failed for a reason that is not a conflict.
	KindCommitFailed Kind = "commit_failed"
	// KindStaleRequest means the working tree no longer matches what the caller
	// asked to commit. Nothing is lost and nothing is blocked: the next snapshot
	// picks up whatever the current state is.
	KindStaleRequest Kind = "stale_request"
	// KindPushFailed means the remote rejected or could not be reached. It never
	// blocks local work.
	KindPushFailed Kind = "push_failed"
)

// blocking reports whether a failure of this kind is one the engine records in
// blocked.json. The set is deliberately small: it is exactly the failures that
// leave the vault in a state where continuing to mutate risks losing content
// that is not yet in the history. Busy, stale, repository-state, and push
// failures are all recoverable by retrying or by fixing the repository, so
// none of them latch.
func (k Kind) blocking() bool {
	switch k {
	case KindUnexplainedDeletion, KindAmbiguousChange, KindRefConflict, KindCommitFailed:
		return true
	default:
		return false
	}
}

// SafetyError is the one error type this package returns for a Git safety
// failure. It carries the classification, the operation that hit it, the paths
// involved, and a one-clause reason. It never carries note contents: every
// reason is composed here from fixed text plus paths and object identifiers,
// and any diagnostic taken from Git is sanitized to a single bounded line
// before it gets in.
type SafetyError struct {
	// Kind classifies the failure.
	Kind Kind
	// Op names the internal operation, for example "preflight" or "commit".
	Op string
	// Reason is a one-clause explanation with no file contents in it.
	Reason string
	// Paths lists the vault-relative paths the failure is about, if any.
	Paths []string
	// OldOID and NewOID name the objects or refs involved, when the failure is
	// about a specific transition. They are safe to record and to print: an
	// object name is how a person recovers content with `git cat-file`, and it
	// discloses nothing on its own.
	OldOID string
	NewOID string
	// Err is the underlying failure, if there was one.
	Err error
}

func (e *SafetyError) Error() string {
	if e == nil {
		return "vaultgit: safety failure"
	}
	var b strings.Builder
	b.WriteString("vaultgit: ")
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	if e.Reason != "" {
		b.WriteString(e.Reason)
	} else {
		b.WriteString(string(e.Kind))
	}
	if len(e.Paths) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(e.Paths, ", "))
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *SafetyError) Unwrap() error { return e.Err }

func safetyErrorf(kind Kind, op string, format string, args ...any) *SafetyError {
	return &SafetyError{Kind: kind, Op: op, Reason: fmt.Sprintf(format, args...)}
}

// KindOf reports the classification of err, or the empty Kind when err is not a
// *SafetyError.
func KindOf(err error) Kind {
	var safety *SafetyError
	if errors.As(err, &safety) {
		return safety.Kind
	}
	return ""
}

// IsUnmanaged reports whether err means the vault carries no Git repository.
func IsUnmanaged(err error) bool { return errors.Is(err, ErrUnmanaged) }

// IsBlocked reports whether err means a persisted block marker is refusing
// mutation snapshots.
func IsBlocked(err error) bool { return KindOf(err) == KindBlocked }

// IsBusy reports whether err means another process holds a safety lock. The
// caller may retry.
func IsBusy(err error) bool { return KindOf(err) == KindBusy }

// IsUnexplainedDeletion reports whether err means a tracked path disappeared
// with no journal record proving it.
func IsUnexplainedDeletion(err error) bool { return KindOf(err) == KindUnexplainedDeletion }

// IsPushFailure reports whether err is a remote failure. A caller should log,
// notify, and retry: it never means local work must stop.
func IsPushFailure(err error) bool { return KindOf(err) == KindPushFailed }

// Blocks reports whether err is a failure that latched persistent blocked
// state, so later mutations are refused until a person clears it.
func Blocks(err error) bool {
	kind := KindOf(err)
	return kind == KindBlocked || kind.blocking()
}
