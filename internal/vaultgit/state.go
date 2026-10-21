package vaultgit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
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
//
// Creation is durable, not just visible: a record's own fsync and the sync of
// this directory do not make the directory's entry in `.git` survive power
// loss, and a first blocked.json or intent.json would vanish with it.
func (s *safetyState) ensureDir() error {
	if err := vault.GuardMutation("", s.dir); err != nil {
		return err
	}
	return vault.EnsureDirDurable(s.dir, 0o755)
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
	if s.failWrite != nil {
		if err := s.failWrite(name); err != nil {
			return err
		}
	}
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
