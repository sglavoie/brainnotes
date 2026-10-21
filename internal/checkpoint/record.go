package checkpoint

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// recordVersion is the shape of the checkpoint record on disk. A reader that
// does not recognize it refuses rather than guessing, because the one thing the
// record is for is telling a writer whether to wait.
const recordVersion = 1

// recordPerm is the mode a new record is created with. It is world-readable
// because a person diagnosing a stuck vault should be able to read it without
// being the account that runs the daemon; it carries no note content.
const recordPerm fs.FileMode = 0o644

// Phase names how far a checkpoint has got, which is the difference between
// "writers should stop joining" and "the vault is quiet and being copied".
type Phase string

const (
	// PhaseDraining means the checkpoint has announced and is waiting for the
	// writers that were already running to finish.
	PhaseDraining Phase = "draining"
	// PhaseHeld means the barrier is held exclusively and the vault is quiet.
	PhaseHeld Phase = "held"
)

// record is the durable announcement of a checkpoint's intent.
//
// It is advisory in the sense that no correctness property rests on it — the
// flock is what excludes — and load-bearing in the sense that without it the
// exclusive acquisition can be starved forever. Everything in it beyond ID and
// Phase exists so a person looking at a vault that will not accept writes can
// tell who is holding it and since when.
type record struct {
	Version     int       `json:"version"`
	ID          string    `json:"id"`
	Phase       Phase     `json:"phase"`
	Reason      string    `json:"reason,omitempty"`
	PID         int       `json:"pid"`
	Host        string    `json:"host,omitempty"`
	AnnouncedAt time.Time `json:"announced_at"`
	Deadline    time.Time `json:"deadline"`
}

// readRecord returns the vault's checkpoint record, or nil when there is none.
//
// An absent record is the ordinary case and not an error. A malformed one is an
// error: the file exists only while some process believes it is checkpointing
// this vault, and a reader that cannot tell what it says must not conclude the
// vault is free.
func readRecord(path string) (*record, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("checkpoint: read record %q: %w", path, err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("checkpoint: parse record %q: %w", path, err)
	}
	if rec.Version != recordVersion {
		return nil, fmt.Errorf("checkpoint: record %q is version %d, this build reads %d",
			path, rec.Version, recordVersion)
	}
	return &rec, nil
}

// writeRecord replaces the record durably.
//
// vault.WriteStateFile rather than vaultstate.Save, for the reason
// vaultexport.saveState gives: Save's third guarantee is a snapshot into the
// content history, and this file is out of that history's scope by design, so
// Save would silently drop the snapshot rather than refuse. The two guarantees
// that do apply — the storage-location guard and the path-continuous
// replacement — are taken directly.
//
// Only a caller holding the claim lock may call this. That is what makes
// overwriting a record somebody else wrote safe: a record whose claim lock is
// free belongs to a process that is gone.
func writeRecord(path string, rec *record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: encode record: %w", err)
	}
	data = append(data, '\n')
	//bn:direct-write the record's parent is .bnotes, which vault.WriteStateFile
	// requires to exist and which a vault that has never been written to does not
	// have yet. It creates no name a refusal is supposed to prevent: the guard on
	// the next line decides whether the record itself may exist, and an empty
	// .bnotes is what every other sidecar owner creates for the same reason.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("checkpoint: create record directory: %w", err)
	}
	if err := vault.WriteStateFile(path, data, recordPerm); err != nil {
		return fmt.Errorf("checkpoint: write record: %w", err)
	}
	return nil
}

// clearRecord removes the announcement. A record that is already gone is the
// outcome the caller wanted.
func clearRecord(path string) error {
	//bn:direct-write removing the announcement is the whole operation; there is no
	// replacement to make path-continuous, and the file carries no user intent — a
	// record that outlives its client is already treated as absent by every reader.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checkpoint: clear record %q: %w", path, err)
	}
	return nil
}

// newID mints an identity for one checkpoint. It exists so a held checkpoint can
// be named in a log, a status line, and a backup manifest without those three
// having to agree on a clock.
func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("checkpoint: mint identity: %w", err)
	}
	return "ckpt_" + hex.EncodeToString(buf), nil
}
