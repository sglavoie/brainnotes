package vaultgit

import (
	"time"
)

// BlockRecord is the persistent fail-closed marker. It records why mutation
// snapshots stopped and what a person has to do about it.
//
// It carries paths and object identifiers but never file contents: the whole
// point of the marker is that it can be read, mailed, and pasted into an issue
// without leaking a note. Object identifiers are safe and load-bearing — they
// are how a person recovers the exact bytes with `git cat-file blob <oid>`,
// which is the first line of every recovery instruction below.
type BlockRecord struct {
	Version int `json:"version"`
	// Kind is the machine token from the failure that latched the block.
	Kind Kind `json:"kind"`
	// Reason is a one-clause human explanation with no file contents in it.
	Reason string `json:"reason"`
	// Operation names what the engine was doing.
	Operation string `json:"operation"`
	// ObservedAt is when the condition was seen, in UTC.
	ObservedAt time.Time `json:"observed_at"`
	// Paths are the vault-relative paths involved.
	Paths []string `json:"paths,omitempty"`
	// OldOID and NewOID name the objects or refs involved, when known.
	OldOID string `json:"old_oid,omitempty"`
	NewOID string `json:"new_oid,omitempty"`
	// Recovery is the manual procedure, one step per line.
	Recovery []string `json:"recovery,omitempty"`
}

// IntentPhase names how far a snapshot had progressed when the record was last
// written. It is diagnostic rather than decisive: recovery proves what happened
// by asking Git whether NewOID is reachable from the ref, because a phase is a
// statement about the past and the repository is the present.
type IntentPhase string

const (
	// IntentPhasePlanned means the operation had identified its paths and its
	// starting ref but had not yet produced a commit object.
	IntentPhasePlanned IntentPhase = "planned"
	// IntentPhaseCommitted means a commit object exists and the ref update was
	// about to be attempted. This is the only phase from which recovery can prove
	// a snapshot landed, because it is the only one that carries a NewOID.
	IntentPhaseCommitted IntentPhase = "committed"
)

// IntentRecord is the durable pre-operation intent.
//
// It exists to answer one question after a crash: did the snapshot that was
// being taken actually land? Without it, an interrupted operation is
// indistinguishable from one that never started — and the difference matters,
// because the second is fine and the first may mean a change the user made is
// recorded nowhere. The record is written and fsynced before the first step that
// could produce a commit, and removed only after the ref update and its
// containing directory are durable.
//
// Like BlockRecord it carries paths and object identifiers but never note
// contents, for the same reason: it is meant to be readable, mailable, and
// pasteable into an issue.
type IntentRecord struct {
	Version int `json:"version"`
	// Operation names what the engine was doing, from the same sanitized
	// vocabulary as BlockRecord.Operation.
	Operation string `json:"operation"`
	// ID identifies this attempt, so a block marker and an intent written by the
	// same operation can be correlated in a log.
	ID string `json:"id"`
	// Phase is how far the operation had progressed. See IntentPhase.
	Phase IntentPhase `json:"phase"`
	// ObservedAt is when the record was written, in UTC.
	ObservedAt time.Time `json:"observed_at"`
	// Ref is the fully qualified ref the snapshot targets.
	Ref string `json:"ref"`
	// OldOID is the ref value the operation started from, empty on an unborn
	// branch. NewOID is the commit the operation built, known only once the phase
	// is committed.
	OldOID string `json:"old_oid,omitempty"`
	NewOID string `json:"new_oid,omitempty"`
	// LandedAt is the first clock reading taken after the ref update was observed
	// to succeed and its containing directory was synced. It is populated only
	// for external autosaves, whose recovery must preserve the full configured
	// interval after that landing even when watch.json's completion write is lost.
	LandedAt *time.Time `json:"landed_at,omitempty"`
	// Paths are the vault-relative paths involved, truncated at intentPathCap.
	Paths []string `json:"paths,omitempty"`
	// PathCount is the untruncated count, so a truncated Paths list never
	// misrepresents the size of what was in flight.
	PathCount int `json:"path_count"`
	// Message is the deterministic commit subject, kept for a person comparing
	// the record against `git log`. It is generated, never derived from a note.
	Message string `json:"message,omitempty"`
	// Recovery is the manual procedure, one step per line.
	Recovery []string `json:"recovery,omitempty"`
}

// PushState records remote retry state. A push failure is operational, not a
// safety failure: this file exists so retries back off and so a caller can tell
// a person "the remote has been unreachable for two hours" without keeping that
// in memory across a restart.
type PushState struct {
	Version int `json:"version"`
	// Attempts counts consecutive failures.
	Attempts int `json:"attempts"`
	// FirstFailureAt is when the current failure streak began, in UTC.
	FirstFailureAt time.Time `json:"first_failure_at"`
	// LastAttemptAt is the most recent attempt, in UTC.
	LastAttemptAt time.Time `json:"last_attempt_at"`
	// NextAttemptAfter is the earliest time a retry is worth making.
	NextAttemptAfter time.Time `json:"next_attempt_after"`
	// LastError is a sanitized single-line diagnostic.
	LastError string `json:"last_error,omitempty"`
}

// WatchState is the external-change debounce clock and the schedule derived
// from it. It is persisted rather than held in memory so a watcher restart
// cannot reset a burst's maximum-latency deadline and defer a commit forever.
//
// The last two fields are additive to schema version 1 rather than a version 2.
// A record written before they existed is still a complete statement of the
// burst — they are *derived* from the first three plus the vault's policy, so an
// older reader loses nothing by not knowing them and this build republishes them
// from the same inputs the moment it reads one. Bumping the version would have
// meant treating every pre-existing record as unreadable, which for a debounce
// clock means silently restarting a burst that was already past its deadline.
type WatchState struct {
	Version int `json:"version"`
	// FirstChangeAt is when the current uncommitted burst began, in UTC. It is
	// zero when there is no pending burst.
	FirstChangeAt time.Time `json:"first_change_at,omitempty"`
	// LastChangeAt is the most recent observed change, in UTC.
	LastChangeAt time.Time `json:"last_change_at,omitempty"`
	// LastCommitAt is when an *external* autosave last consumed a burst, in UTC.
	//
	// It is the floor's anchor, so it means one specific thing: the last snapshot
	// [Manager.CommitPending] took. A commit made by an explicit BrainNotes
	// mutation — a `bn` command, a web write, the sidecar — never advances it,
	// because the floor exists to bound how often the *watcher* writes history and
	// a person who just renamed a note did not ask to be rate limited.
	LastCommitAt time.Time `json:"last_commit_at,omitempty"`
	// AutosaveMinInterval is the canonical policy token in force when this record
	// was written: config.AutosaveOff, or a canonical duration spelling such as
	// "5m". It is published rather than consulted — the schedule below is computed
	// from the live policy — so a reader can say which floor produced the due time
	// it is looking at.
	AutosaveMinInterval string `json:"autosave_min_interval,omitempty"`
	// NextCommitDueAt is the earliest time the watcher may snapshot the pending
	// burst, in UTC. It is a pointer so that "there is no schedule" is *absence*
	// rather than a zero timestamp: a health consumer reading this file has to be
	// able to tell an unscheduled vault from one due in the year 1, and a struct
	// field with `omitempty` cannot express that (encoding/json omits no struct,
	// which is why FirstChangeAt is written as 0001-01-01T00:00:00Z on an idle
	// vault). It is absent while the policy is off and while no burst is open.
	NextCommitDueAt *time.Time `json:"next_commit_due_at,omitempty"`
	// ExternalCommitStartedAt is the durable statement that an external autosave
	// was attempted at that instant and its outcome is not known to be recorded
	// here. It is written and fsynced *before* the snapshot can produce a commit
	// and retired only once LastCommitAt durably names the result. A landed
	// snapshot whose completion write is lost keeps its recovery intent too; that
	// intent records the first clock reading after the ref was observed to land,
	// and recovery publishes that later bound before retiring either marker.
	//
	// It anchors the floor exactly as LastCommitAt does — see externalAnchor —
	// which is what makes a pre-intent crash fail closed rather than merely
	// diagnostic. With no commit or recovery intent to settle, that marker defers
	// one interval and then expires on its own as its timestamp recedes.
	//
	// Like the two fields above it is written only by the watcher's own paths, so
	// an explicit `bn`, web, or sidecar commit still neither waits for the floor
	// nor moves it.
	ExternalCommitStartedAt *time.Time `json:"external_commit_started_at,omitempty"`
}
