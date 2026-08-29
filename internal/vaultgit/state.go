package vaultgit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// WatchState is the external-change debounce clock. It is persisted rather than
// held in memory so a watcher restart cannot reset a burst's maximum-latency
// deadline and defer a commit forever.
type WatchState struct {
	Version int `json:"version"`
	// FirstChangeAt is when the current uncommitted burst began, in UTC. It is
	// zero when there is no pending burst.
	FirstChangeAt time.Time `json:"first_change_at,omitempty"`
	// LastChangeAt is the most recent observed change, in UTC.
	LastChangeAt time.Time `json:"last_change_at,omitempty"`
	// LastCommitAt is when a snapshot last consumed a burst, in UTC.
	LastCommitAt time.Time `json:"last_commit_at,omitempty"`
}

// safetyState owns the state directory.
type safetyState struct {
	dir string
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
func (s *safetyState) LoadWatchState() WatchState {
	var state WatchState
	found, err := s.readJSON(watchStateFileName, &state)
	if !found || err != nil || state.Version != stateVersion {
		return WatchState{Version: stateVersion}
	}
	return state
}

// NoteChange records that the working tree changed at now, starting a burst if
// one is not already open.
func (s *safetyState) NoteChange(now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.Version = stateVersion
	if state.FirstChangeAt.IsZero() {
		state.FirstChangeAt = now
	}
	state.LastChangeAt = now
	return state, s.writeJSON(watchStateFileName, state)
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
func (s *safetyState) NoteSettled() (closed bool, err error) {
	state := s.LoadWatchState()
	if state.FirstChangeAt.IsZero() && state.LastChangeAt.IsZero() {
		return false, nil
	}
	state.Version = stateVersion
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	return true, s.writeJSON(watchStateFileName, state)
}

// NoteCommit closes the current burst.
func (s *safetyState) NoteCommit(now time.Time) (WatchState, error) {
	now = now.UTC()
	state := s.LoadWatchState()
	state.Version = stateVersion
	state.FirstChangeAt = time.Time{}
	state.LastChangeAt = time.Time{}
	state.LastCommitAt = now
	return state, s.writeJSON(watchStateFileName, state)
}

// DueAt reports whether an open burst should be committed at now: either the
// working tree has been quiet for DebounceQuiet, or the burst has been open for
// DebounceMaxLatency. A state with no open burst is never due.
func (w WatchState) DueAt(now time.Time) bool {
	if w.FirstChangeAt.IsZero() {
		return false
	}
	now = now.UTC()
	if !w.LastChangeAt.IsZero() && !now.Before(w.LastChangeAt.Add(DebounceQuiet)) {
		return true
	}
	return !now.Before(w.FirstChangeAt.Add(DebounceMaxLatency))
}

// DueAt reports whether a retry is worth making at now.
func (p PushState) DueAt(now time.Time) bool {
	return !now.UTC().Before(p.NextAttemptAfter)
}
