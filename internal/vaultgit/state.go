package vaultgit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Operational state lives under `<git-dir>/brainnotes-safety/`. That location
// is chosen, not incidental: everything inside `.git` is invisible to
// `git status`, unreachable from any tree, and travels with the repository
// rather than with the working tree, so a block marker cannot itself become a
// pending change the engine would then try to commit, and cannot be
// accidentally pushed to the content remote.
const (
	// safetyDirName is the state directory inside the git directory.
	safetyDirName = "brainnotes-safety"
	// safetyLockName serializes mutation snapshots.
	safetyLockName = "safety.lock"
	// pushLockName serializes remote attempts. It is deliberately a different
	// lock: a push is a network round trip with no useful bound, and a note save
	// must never wait behind one.
	pushLockName = "push.lock"
	// blockedFileName is the persistent fail-closed marker.
	blockedFileName = "blocked.json"
	// intentFileName is the pre-operation recovery intent: the durable statement
	// that a snapshot was about to be taken, written before anything can change
	// and removed only once the result is durable.
	intentFileName = "intent.json"
	// pushStateFileName holds remote retry state.
	pushStateFileName = "push.json"
	// watchStateFileName holds the external-change debounce clock.
	watchStateFileName = "watch.json"
	// indexFileName is the private index. It is truncated and re-seeded from
	// HEAD at the start of every operation, so its residue is never read.
	indexFileName = "index"
	// manifestFileName mirrors journal.ManifestFileName. It is restated rather
	// than imported so scope.go stays a pure string policy.
	manifestFileName = "manifest.json"

	// stateVersion is the on-disk schema version shared by every JSON file here.
	// A record written by a newer version is treated as unreadable rather than
	// as absent, which for blocked.json means "still blocked".
	stateVersion = 1

	// intentPathCap bounds how many paths an intent record lists. The list is
	// diagnostic — recovery decides from PathCount and the object identifiers, not
	// from it — so truncating a thousand-file import keeps the record readable
	// without weakening any decision made from it.
	intentPathCap = 256
)

// Debounce policy for external editor changes, fixed by the plan: a change is
// committed after 30 seconds of quiet, and never later than two minutes after
// the first change in a burst. The maximum matters because an editor that
// autosaves every 20 seconds would otherwise never reach quiet.
//
// These two are deliberately not configurable and neither is the poll interval
// or the push interval. They describe *readiness* — how long the engine waits to
// be reasonably sure a burst of edits is over — and a user who shortened them
// would be asking for snapshots of half-written files. The one dial a vault owns
// is the floor *between* external autosaves, which is a question about how much
// history a person wants rather than about when a burst is finished; it arrives
// as a resolved [config.AutosavePolicy] and never as a constant here.
const (
	// DebounceQuiet is how long the working tree must be still before an
	// external change is committed.
	DebounceQuiet = 30 * time.Second
	// DebounceMaxLatency bounds how long a burst of changes may defer its commit.
	DebounceMaxLatency = 2 * time.Minute
)

// Push retry policy. The plan requires an attempt at least every five minutes,
// so the exponential backoff is capped there rather than growing without bound.
const (
	pushBackoffBase = 30 * time.Second
	pushBackoffMax  = 5 * time.Minute
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
	// and retired only once LastCommitAt durably names the result, so the window
	// in which a crash or a failed state write could lose the floor's anchor is
	// closed by construction: whatever survives that window, one of the two
	// fields already holds an instant no earlier than the commit's.
	//
	// It anchors the floor exactly as LastCommitAt does — see externalAnchor —
	// which is what makes it fail-closed rather than merely diagnostic. A record
	// left behind by a crash therefore defers the next external snapshot by one
	// interval and then expires on its own, because the anchor it states recedes
	// with the clock like any other.
	//
	// Like the two fields above it is written only by the watcher's own paths, so
	// an explicit `bn`, web, or sidecar commit still neither waits for the floor
	// nor moves it.
	ExternalCommitStartedAt *time.Time `json:"external_commit_started_at,omitempty"`
}

// safetyState owns the state directory.
type safetyState struct {
	dir string
	// failWrite is a test seam: when set, it runs in place of the durable write
	// of the named state file and its error is returned instead. It exists
	// because the failure this package has to survive — a state write that fails
	// in the instant *after* a commit landed — cannot be provoked from outside a
	// process without also breaking the writes that come before it. It is nil in
	// production and set only by tests in this package.
	failWrite func(name string) error
}

func newSafetyState(gitDir string) *safetyState {
	return &safetyState{dir: filepath.Join(gitDir, safetyDirName)}
}

// Dir reports the state directory. It is exported through the manager so an
// operator can be told exactly where to look.
func (s *safetyState) Dir() string { return s.dir }

func (s *safetyState) path(name string) string { return filepath.Join(s.dir, name) }

// ensureDir creates the state directory. The mutation guard runs first because
// this is the package's own "create something before you lock" case: the guard
// exists so that a refused vault has no artifact left behind, and the state
// directory is an artifact like any other.
func (s *safetyState) ensureDir() error {
	if err := vault.GuardMutation("", s.dir); err != nil {
		return err
	}
	return os.MkdirAll(s.dir, 0o755)
}

// writeAtomic stages bytes in a sibling temp file, fsyncs them, and renames.
// It mirrors internal/journal's writeAtomic: a reader opens either the whole
// old file or the whole new one, never a torn record.
func (s *safetyState) writeAtomic(name string, data []byte) error {
	if s.failWrite != nil {
		if err := s.failWrite(name); err != nil {
			return err
		}
	}
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("vaultgit: prepare state directory: %w", err)
	}
	target := s.path(name)
	tmp, err := os.CreateTemp(s.dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("vaultgit: create temp for %q: %w", target, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("vaultgit: write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("vaultgit: sync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("vaultgit: close %q: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("vaultgit: chmod %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		cleanup()
		return fmt.Errorf("vaultgit: rename %q -> %q: %w", tmpPath, target, err)
	}
	// The rename is atomic against a concurrent reader the moment it returns, but
	// not against power loss until the directory entry itself is durable. Every
	// file written through here is one whose absence after a crash would be read
	// as "nothing was wrong": a block marker that vanished re-enables mutations,
	// and an intent that vanished turns an interrupted snapshot back into one that
	// looks like it never started. Syncing here is what makes those records mean
	// what the rest of the package assumes they mean.
	if err := vault.SyncDir(s.dir); err != nil {
		return fmt.Errorf("vaultgit: sync state directory: %w", err)
	}
	return nil
}

func (s *safetyState) writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("vaultgit: encode %s: %w", name, err)
	}
	return s.writeAtomic(name, append(data, '\n'))
}

// readJSON loads a state file. A missing file reports found=false with no
// error; a file that exists but does not decode reports found=true *and* an
// error, so a caller can distinguish "there is nothing here" from "there is
// something here and I cannot read it". For blocked.json those two answers have
// opposite safety consequences.
func (s *safetyState) readJSON(name string, dst any) (found bool, err error) {
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, fmt.Errorf("vaultgit: read %s: %w", name, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return true, fmt.Errorf("vaultgit: decode %s: %w", name, err)
	}
	return true, nil
}

func (s *safetyState) remove(name string) error {
	if err := os.Remove(s.path(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultgit: remove %s: %w", name, err)
	}
	// An unlink is durable only once the directory entry is, and clearing the
	// intent record is precisely the step the recovery protocol reads as "this
	// finished". A record that reappears after a crash costs one no-op recovery
	// pass, which is cheap; the symmetric case for a cleared block marker is not,
	// so both go through the same sync.
	if err := vault.SyncDir(s.dir); err != nil {
		return fmt.Errorf("vaultgit: sync state directory: %w", err)
	}
	return nil
}

// LoadBlock reports the persisted block marker.
//
// The corrupt case is the interesting one and it fails closed: a blocked.json
// that exists but does not parse still means blocked, and the returned record
// says so. Treating an unreadable marker as absent would make truncating the
// file — which a crash mid-write, a full disk, or a careless `>` all do — the
// way to re-enable mutations, which is precisely backwards. A version this
// build does not understand is handled the same way.
func (s *safetyState) LoadBlock() (record BlockRecord, blocked bool, err error) {
	var stored BlockRecord
	found, decodeErr := s.readJSON(blockedFileName, &stored)
	if !found {
		return BlockRecord{}, false, nil
	}
	if decodeErr != nil {
		return BlockRecord{
			Version:   stateVersion,
			Kind:      KindBlocked,
			Reason:    "the block marker exists but could not be read, so mutation snapshots stay refused",
			Recovery:  corruptMarkerRecovery(s.path(blockedFileName)),
			Operation: "load-block",
		}, true, decodeErr
	}
	if stored.Version != stateVersion {
		return BlockRecord{
			Version:   stored.Version,
			Kind:      KindBlocked,
			Reason:    fmt.Sprintf("the block marker is version %d, which this build does not understand", stored.Version),
			Recovery:  corruptMarkerRecovery(s.path(blockedFileName)),
			Operation: "load-block",
		}, true, nil
	}
	return stored, true, nil
}

func corruptMarkerRecovery(path string) []string {
	return []string{
		"Inspect " + path + " by hand; it is JSON and holds no note contents.",
		"Run `git -C <vault> status` and `git -C <vault> log --oneline -5` to see what the working tree and the history actually hold.",
		"Recover anything missing from the history with `git -C <vault> cat-file blob <oid> > <path>` before clearing the marker.",
		"Delete " + path + " to allow BrainNotes mutations again.",
	}
}

// SaveBlock persists the marker.
//
// Its failure is reported but is not what makes the vault safe. If this write
// fails, the condition that produced it — a deleted file still missing from the
// working tree, a ref that still will not advance — is unchanged on disk, and
// the next preflight re-derives the same verdict from the same evidence. The
// marker makes the refusal cheap to explain, not real.
func (s *safetyState) SaveBlock(record BlockRecord) error {
	record.Version = stateVersion
	record.ObservedAt = record.ObservedAt.UTC()
	return s.writeJSON(blockedFileName, record)
}

// ClearBlock removes the marker. Nothing in this package calls it: clearing a
// block is a deliberate human act after the content has been recovered, and an
// engine that could clear its own marker could loop through the same failure
// forever. It exists for a future `bn` recovery command and for tests.
func (s *safetyState) ClearBlock() error { return s.remove(blockedFileName) }

// LoadIntent reports the persisted recovery intent.
//
// It fails closed in the same shape as LoadBlock, and for a sharper reason. The
// only way recovery can retire an intent is by proving NewOID is reachable from
// the ref, so an unreadable record is returned with an *empty* NewOID: there is
// nothing to prove reachable, the proof cannot succeed, and the operation stays
// refused until a person looks. Returning "absent" for a file that exists would
// let a truncated record — a crash mid-write, a full disk — silently retire the
// one durable statement that a snapshot may have been lost.
//
// A record whose version this build does not understand is handled identically:
// its NewOID may well be meaningful, but this build cannot claim to know what
// the field means, and guessing is exactly the wrong move here.
func (s *safetyState) LoadIntent() (record IntentRecord, present bool, err error) {
	var stored IntentRecord
	found, decodeErr := s.readJSON(intentFileName, &stored)
	if !found {
		return IntentRecord{}, false, nil
	}
	if decodeErr != nil {
		return IntentRecord{
			Version:    stateVersion,
			Operation:  "load-intent",
			Phase:      IntentPhasePlanned,
			ObservedAt: time.Time{},
			Recovery:   unreadableIntentRecovery(s.path(intentFileName)),
		}, true, decodeErr
	}
	if stored.Version != stateVersion {
		return IntentRecord{
			Version:   stored.Version,
			Operation: "load-intent",
			Phase:     IntentPhasePlanned,
			Recovery:  unreadableIntentRecovery(s.path(intentFileName)),
		}, true, nil
	}
	return stored, true, nil
}

func unreadableIntentRecovery(path string) []string {
	return []string{
		"Inspect " + path + " by hand; it is JSON and holds no note contents.",
		"Run `git -C <vault> status` and `git -C <vault> log --oneline -5` to see whether the interrupted snapshot landed.",
		"Commit anything still uncommitted by hand before continuing.",
		"Delete " + path + " to allow BrainNotes mutation snapshots again.",
	}
}

// SaveIntent persists the intent and, through writeAtomic, fsyncs both the
// record and the directory entry naming it. Unlike SaveBlock, this write's
// failure is decisive: an operation that cannot record its intent must not
// proceed, because the state it is about to create would then be unrecoverable
// by construction.
func (s *safetyState) SaveIntent(record IntentRecord) error {
	record.Version = stateVersion
	record.ObservedAt = record.ObservedAt.UTC()
	if len(record.Paths) > intentPathCap {
		record.Paths = record.Paths[:intentPathCap]
	}
	return s.writeJSON(intentFileName, record)
}

// ClearIntent retires the record. It is called only after the outcome is durable
// or after recovery has proved what happened, never as cleanup for an error
// whose result is unknown.
func (s *safetyState) ClearIntent() error { return s.remove(intentFileName) }

// LoadPushState reports remote retry state. An unreadable file is reported as
// absent, the opposite of the block marker: forgetting a backoff schedule costs
// one early retry, while forgetting a block would cost a note.
func (s *safetyState) LoadPushState() (PushState, bool) {
	var state PushState
	found, err := s.readJSON(pushStateFileName, &state)
	if !found || err != nil || state.Version != stateVersion {
		return PushState{}, false
	}
	return state, true
}

// RecordPushFailure advances the retry schedule and persists it.
func (s *safetyState) RecordPushFailure(now time.Time, detail string) (PushState, error) {
	now = now.UTC()
	state, ok := s.LoadPushState()
	if !ok {
		state = PushState{FirstFailureAt: now}
	}
	state.Version = stateVersion
	state.Attempts++
	state.LastAttemptAt = now
	state.LastError = detail
	state.NextAttemptAfter = now.Add(pushBackoff(state.Attempts))
	return state, s.writeJSON(pushStateFileName, state)
}

// ClearPushState forgets the failure streak after a successful push.
func (s *safetyState) ClearPushState() error { return s.remove(pushStateFileName) }

// pushBackoff doubles from 30 seconds and caps at five minutes, which is the
// plan's "at least every five minutes" floor expressed as a ceiling on the
// wait.
func pushBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	wait := pushBackoffBase
	for i := 1; i < attempts; i++ {
		wait *= 2
		if wait >= pushBackoffMax {
			return pushBackoffMax
		}
	}
	return wait
}

// LoadWatchState reports the debounce clock, defaulting to an empty one.
//
// It reports the record as it is on disk, including a pre-existing version-1
// record that predates the schedule fields. Deriving those is publishWatchState's
// job, so that every path which persists a change publishes the same schedule
// rather than each reader computing its own.
func (s *safetyState) LoadWatchState() WatchState {
	var state WatchState
	found, err := s.readJSON(watchStateFileName, &state)
	if !found || err != nil || state.Version != stateVersion {
		return WatchState{Version: stateVersion}
	}
	return state
}

// publishWatchState writes the record with its derived fields recomputed, and
// only when doing so would change what is on disk.
//
// The conditional is what makes it safe to call from the watcher's poll: an idle
// vault whose record already says what this call would say costs one read, and a
// vault whose policy or schedule moved gets one atomic republish. Every mutator
// below routes through here so there is exactly one place a due time is derived.
func (s *safetyState) publishWatchState(state WatchState, policy config.AutosavePolicy, force bool) (WatchState, error) {
	before := state
	state.Version = stateVersion
	state.applyPolicy(policy)
	if !force && before.publishedAs(state) {
		return state, nil
	}
	return state, s.writeJSON(watchStateFileName, state)
}

// RefreshWatchState republishes the derived fields of the persisted record under
// the vault's current policy and reports the result.
//
// This is the "read an older record and bring it up to date" path: a version-1
// record written before the schedule existed, or one written under a different
// floor, is recomputed from the burst timestamps it already carries and rewritten
// atomically. Nothing about the burst itself is invented — an absent
// LastCommitAt stays absent rather than being inferred from the Git history,
// because a history full of explicit `bn` commits would otherwise be read as
// external autosaves and would hold the very first external snapshot back behind
// a floor nothing ever anchored.
func (s *safetyState) RefreshWatchState(policy config.AutosavePolicy) (WatchState, error) {
	return s.publishWatchState(s.LoadWatchState(), policy, false)
}

// NoteChange records that the working tree changed at now, starting a burst if
// one is not already open.
func (s *safetyState) NoteChange(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	if state.FirstChangeAt.IsZero() {
		state.FirstChangeAt = now
	}
	state.LastChangeAt = now
	// Forced: a change always rewrites the record. The burst timestamps moved even
	// when the due time did not — a quiet period restarted under a floor that is
	// still the binding deadline computes the same due time, and a reader that saw
	// no write would think the burst had gone still.
	return s.publishWatchState(state, policy, true)
}

// NoteSettled closes an open burst that no longer has anything to commit,
// without claiming a commit happened.
//
// It exists for the case where a burst opens and then resolves itself: an
// editor writes a file and the user undoes it, a sync client touches a
// timestamp and puts it back. NoteCommit is wrong there because LastCommitAt
// would name a snapshot that was never taken, and leaving the burst open is
// worse — DueAt would stay true forever and every poll would rescan and commit
// nothing.
// It reports whether there was an open burst to close, so a caller polling an
// idle vault can stay silent instead of announcing the same non-event forever.
// It clears the due time with the burst, because there is no longer anything for
// a schedule to be about. LastCommitAt is left exactly where it was: a burst that
// resolved itself took no snapshot, so it must not move the floor a real one
// would be measured from.
//
// ExternalCommitStartedAt is left alone for the sharper version of the same
// reason. The state a crash between a landed commit and its anchor write leaves
// behind is exactly "an in-flight marker and a clean working tree", so this is
// the first path that runs afterwards; retiring the marker here would drop the
// only durable evidence of that snapshot and hand the next burst a stale floor.
func (s *safetyState) NoteSettled(policy config.AutosavePolicy) (closed bool, err error) {
	state := s.LoadWatchState()
	if state.FirstChangeAt.IsZero() && state.LastChangeAt.IsZero() {
		// Nothing to close, but the policy or the schedule fields may still be stale
		// — an unforced publish rewrites the record only if they are.
		_, err = s.publishWatchState(state, policy, false)
		return false, err
	}
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	_, err = s.publishWatchState(state, policy, true)
	return true, err
}

// BeginExternalCommit records that an external autosave is about to be attempted
// at now, and anchors the floor there before it can be taken.
//
// This is the write-ahead half of the floor's durability contract, and its
// ordering is the whole of it: it runs before anything the attempt does can
// produce a commit, so a commit that exists is a commit an anchor no earlier than
// it already survives. Its failure is decisive for the same reason
// [safetyState.SaveIntent]'s is — a snapshot whose floor could not be anchored is
// one that could be taken twice inside the interval the vault configured — and it
// costs nothing but a deferral, because the burst it refuses stays open and the
// next poll retries it.
//
// It forces the write. The marker is not a derived field, so an unforced publish
// would skip it whenever the due time it implies happens to match the one already
// on disk, which is precisely the case where the floor is binding.
func (s *safetyState) BeginExternalCommit(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.ExternalCommitStartedAt = &now
	return s.publishWatchState(state, policy, true)
}

// AbandonExternalCommit retires the marker BeginExternalCommit wrote at
// startedAt, for the pass that provably recorded nothing.
//
// A snapshot that reached the repository and committed no tree owes the floor
// nothing, so holding the interval against it would spend a vault's snapshot
// budget on a non-event. The instant is matched rather than assumed: only the
// attempt that wrote a marker may retire it, so a pass cannot lift a floor some
// other attempt is still holding.
func (s *safetyState) AbandonExternalCommit(policy config.AutosavePolicy, startedAt time.Time) (WatchState, error) {
	state := s.LoadWatchState()
	if state.ExternalCommitStartedAt == nil || !state.ExternalCommitStartedAt.Equal(startedAt.UTC()) {
		return state, nil
	}
	state.ExternalCommitStartedAt = nil
	return s.publishWatchState(state, policy, true)
}

// NoteCommit closes the current burst and anchors the external floor at now.
//
// It is called from exactly one place — [Manager.CommitPending], after a
// successful external snapshot — and that is the whole of the floor's contract.
// Every other entry point commits without touching this record, so an explicit
// mutation neither waits for the floor nor resets it.
//
// It also retires the in-flight marker, which is what makes this the *durable*
// statement of the same fact rather than a second one: from here on the anchor is
// LastCommitAt, and it names when the snapshot finished rather than when it
// started. A failure here is survivable precisely because the marker it would
// have retired is still on disk holding the floor.
func (s *safetyState) NoteCommit(policy config.AutosavePolicy, now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	state.LastCommitAt = now
	state.ExternalCommitStartedAt = nil
	return s.publishWatchState(state, policy, true)
}

// applyPolicy recomputes the two derived fields in place. It is the only writer
// of either, so the published token and the published due time cannot disagree
// with each other or with the schedule DueAt enforces.
func (w *WatchState) applyPolicy(policy config.AutosavePolicy) {
	policy = resolveAutosavePolicy(policy)
	w.AutosaveMinInterval = policy.Canonical
	due, scheduled := w.DueTime(policy)
	if !scheduled {
		w.NextCommitDueAt = nil
		return
	}
	w.NextCommitDueAt = &due
}

// publishedAs reports whether w already carries exactly the derived fields of
// next, so an unforced publish can skip the write.
func (w WatchState) publishedAs(next WatchState) bool {
	if w.Version != next.Version || w.AutosaveMinInterval != next.AutosaveMinInterval {
		return false
	}
	switch {
	case w.NextCommitDueAt == nil && next.NextCommitDueAt == nil:
		return true
	case w.NextCommitDueAt == nil || next.NextCommitDueAt == nil:
		return false
	default:
		return w.NextCommitDueAt.Equal(*next.NextCommitDueAt)
	}
}

// DueTime computes the schedule the plan specifies:
//
//	ready_at = min(last_change + DebounceQuiet, first_change + DebounceMaxLatency)
//	floor_at = last_external_autosave + policy.Interval
//	due_at   = max(ready_at, floor_at)
//
// Readiness still decides eligibility on its own — the floor can only ever push a
// due time later, never earlier — so an editor that goes quiet and an editor that
// never does are handled exactly as before, and the floor is what stops a vault
// under continuous edits from acquiring one commit every two minutes forever.
//
// It reports false when there is no schedule to state: the policy is off, or no
// burst is open. With no previous external autosave the floor has nothing to be
// measured from, and readiness alone controls the first snapshot. It is exactly
// what the persisted NextCommitDueAt holds.
func (w WatchState) DueTime(policy config.AutosavePolicy) (time.Time, bool) {
	policy = resolveAutosavePolicy(policy)
	if policy.Disabled || w.FirstChangeAt.IsZero() {
		return time.Time{}, false
	}
	ready := w.FirstChangeAt.UTC().Add(DebounceMaxLatency)
	if !w.LastChangeAt.IsZero() {
		if quiet := w.LastChangeAt.UTC().Add(DebounceQuiet); quiet.Before(ready) {
			ready = quiet
		}
	}
	anchor := w.externalAnchor()
	if anchor.IsZero() {
		return ready, true
	}
	if floor := anchor.Add(policy.Interval); floor.After(ready) {
		return floor, true
	}
	return ready, true
}

// externalAnchor reports the instant the floor is measured from: the later of the
// last external autosave known to have finished and the last one known to have
// been attempted.
//
// The two disagree only across the window this record exists to survive — a
// commit that landed and whose completion could not be written down, because the
// state write failed or the process died. Taking the later of them is what makes
// that window fail closed: the anchor is never earlier than the commit the
// history actually holds, so the next external snapshot cannot land inside the
// configured interval no matter which of the two writes survived.
func (w WatchState) externalAnchor() time.Time {
	anchor := w.LastCommitAt.UTC()
	if w.ExternalCommitStartedAt == nil {
		return anchor
	}
	if started := w.ExternalCommitStartedAt.UTC(); started.After(anchor) {
		return started
	}
	return anchor
}

// DueAt reports whether an open burst may be committed at now: readiness has
// elapsed *and* the configured floor since the last external autosave has too. A
// state with no open burst, and every state under a disabled policy, is never
// due.
func (w WatchState) DueAt(policy config.AutosavePolicy, now time.Time) bool {
	due, scheduled := w.DueTime(policy)
	if !scheduled {
		return false
	}
	return !now.UTC().Before(due)
}

// resolveAutosavePolicy fills in what a caller left out, so that the zero
// AutosavePolicy means the vault default rather than "commit as often as you
// like".
//
// The direction matters: a policy that arrived unset must not resolve to no
// floor. Everything in this package that can hand one over — an Options literal a
// test wrote, a future reload that failed — is a place where "I did not say"
// should mean the shipped default, because the alternative is a vault that
// quietly loses the protection its owner never turned off.
func resolveAutosavePolicy(policy config.AutosavePolicy) config.AutosavePolicy {
	if policy.Disabled {
		return config.AutosavePolicy{Disabled: true, Canonical: config.AutosaveOff}
	}
	if policy.Interval <= 0 {
		fallback, err := config.ParseAutosaveMinInterval(config.DefaultAutosaveMinInterval)
		if err != nil {
			// Unreachable: the default is a constant this build parses at every
			// startup. Falling back to the bound rather than to zero keeps the
			// impossible case on the safe side of the floor.
			return config.AutosavePolicy{
				Interval:  config.MinAutosaveInterval,
				Canonical: config.CanonicalAutosaveDuration(config.MinAutosaveInterval),
			}
		}
		return fallback
	}
	if policy.Canonical == "" {
		policy.Canonical = config.CanonicalAutosaveDuration(policy.Interval)
	}
	return policy
}

// DueAt reports whether a retry is worth making at now.
func (p PushState) DueAt(now time.Time) bool {
	return !now.UTC().Before(p.NextAttemptAfter)
}
