package vaultgit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// guardRecordFileName records which bn-vault-guard build last started on this
// vault. It sits beside the engine's other operational state, inside `.git`, so
// it can never become a pending change.
const guardRecordFileName = "guard.json"

// GuardRecord is what bn-vault-guard writes when it starts watching a vault.
//
// It is a report, never an input: nothing in this package reads it to decide
// anything. Its job is version skew. `just install` replaces the guard binary on
// disk while the LaunchAgent keeps the previous one running, and the only way a
// reader can tell is for the running process to say which build it is.
type GuardRecord struct {
	Version int `json:"version"`
	// Build is the guard's internal/buildinfo identifier; empty when the build
	// could not say what it was.
	Build string `json:"build,omitempty"`
	// PID is the guard process that wrote the record.
	PID int `json:"pid"`
	// StartedAt is when that process started watching, in UTC.
	StartedAt time.Time `json:"started_at"`
}

// RecordGuardStart publishes the running guard's build identifier.
//
// It goes through checkpointMutation like the engine's other operational
// writes, so a checkpoint never copies a half-replaced record.
func (m *Manager) RecordGuardStart(build string) (GuardRecord, error) {
	record := GuardRecord{Version: stateVersion, Build: build, PID: os.Getpid(), StartedAt: m.now().UTC()}
	err := m.checkpointMutation("record guard start", func() error {
		return m.state.writeJSON(guardRecordFileName, record)
	})
	return record, err
}

// ReadGuardRecord reports the guard record of the vault at root without opening
// the engine, taking a lock, or running git. found is false when no guard has
// recorded a start. A record that does not decode or carries another schema
// version is reported as an error, not as absence.
//
// It reads `<root>/.git` directly because that is the only layout this engine
// manages: a vault whose `.git` is a file is refused by Open.
func ReadGuardRecord(root string) (record GuardRecord, found bool, err error) {
	path := filepath.Join(root, ".git", safetyDirName, guardRecordFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return GuardRecord{}, false, nil
	}
	if err != nil {
		return GuardRecord{}, true, fmt.Errorf("vaultgit: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return GuardRecord{}, true, fmt.Errorf("vaultgit: decode %s: %w", path, err)
	}
	if record.Version != stateVersion {
		return GuardRecord{}, true, fmt.Errorf("vaultgit: %s is version %d, this build reads %d", path, record.Version, stateVersion)
	}
	return record, true, nil
}
