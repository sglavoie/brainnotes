// Package restorestate persists the outer intent for a backup restore.
//
// The file transaction used by backup.Restore makes individual regular-file
// changes recoverable. This record is deliberately wider: it remains durable
// until directory cleanup and the Git restore snapshot have both finished.
// Ordinary checkpoints refuse a vault while the record exists; the restore
// entry point is the only caller allowed to acquire the barrier and resume it.
package restorestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	RelDir       = ".bnotes/restore"
	intentName   = "intent.json"
	archiveName  = "archive.tar.gz"
	version      = 1
	PhaseContent = "content"
	PhaseHistory = "history"
)

type Intent struct {
	Version int       `json:"version"`
	Source  string    `json:"source"`
	Archive string    `json:"archive"`
	Wipe    bool      `json:"wipe"`
	Phase   string    `json:"phase"`
	Started time.Time `json:"started"`
}

func path(root string) string { return filepath.Join(root, RelDir, intentName) }

// Load returns a durable restore intent, if one exists. Malformed records are
// errors rather than absence: guessing would let a checkpoint copy a tree whose
// history may not describe it.
func Load(root string) (*Intent, error) {
	data, err := os.ReadFile(path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("restore state: read intent %s: %w", path(root), err)
	}
	var intent Intent
	if err := json.Unmarshal(data, &intent); err != nil {
		return nil, fmt.Errorf("restore state: decode intent %s: %w", path(root), err)
	}
	if intent.Version != version || intent.Archive == "" || (intent.Phase != PhaseContent && intent.Phase != PhaseHistory) {
		return nil, fmt.Errorf("restore state: unsupported or incomplete intent %s", path(root))
	}
	wantArchive := filepath.Join(root, RelDir, archiveName)
	if filepath.Clean(intent.Archive) != filepath.Clean(wantArchive) {
		return nil, fmt.Errorf("restore state: archive path escapes recovery directory")
	}
	return &intent, nil
}

func Pending(root string) (bool, error) {
	intent, err := Load(root)
	return intent != nil, err
}

// Begin durably copies the selected archive into machine-local recovery state
// before publishing the intent. A death during the copy changes no vault
// content; a death after the intent is published is resumable from this copy.
func Begin(root, source string, wipe bool) (*Intent, error) {
	dir := filepath.Join(root, RelDir)
	//bn:direct-write noteops holds the vault checkpoint exclusively; this machine-local directory is excluded from archive, sync and Git scopes and is the recovery boundary itself
	if err := vault.EnsureDirDurable(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restore state: create recovery directory: %w", err)
	}
	//bn:direct-write the exclusive checkpoint is held and owner rwx is required so the live checkpoint and transaction records remain reachable
	if err := os.Chmod(filepath.Join(root, ".bnotes"), 0o700); err != nil {
		return nil, fmt.Errorf("restore state: secure sidecar directory: %w", err)
	}
	archive, err := stageArchive(dir, source)
	if err != nil {
		return nil, err
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("restore state: resolve source: %w", err)
	}
	intent := &Intent{Version: version, Source: absSource, Archive: archive, Wipe: wipe, Phase: PhaseContent, Started: time.Now().UTC()}
	if err := write(root, intent); err != nil {
		return nil, err
	}
	return intent, nil
}

func stageArchive(dir, source string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("restore state: open archive: %w", err)
	}
	defer in.Close()
	//bn:direct-write creates a unique temporary inside the excluded restore directory while the exclusive checkpoint is held; it is synced then renamed or removed
	tmp, err := os.CreateTemp(dir, ".archive-*.tmp")
	if err != nil {
		return "", fmt.Errorf("restore state: create staged archive: %w", err)
	}
	tmpName := tmp.Name()
	//bn:direct-write removes only the unique recovery temporary created immediately above
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("restore state: copy archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("restore state: sync archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("restore state: close archive: %w", err)
	}
	archive := filepath.Join(dir, archiveName)
	//bn:direct-write publishes the durable recovery archive inside the excluded restore directory before any content mutation begins
	if err := os.Rename(tmpName, archive); err != nil {
		return "", fmt.Errorf("restore state: publish archive: %w", err)
	}
	if err := vault.SyncDir(dir); err != nil {
		return "", fmt.Errorf("restore state: sync recovery directory: %w", err)
	}
	return archive, nil
}

func SetPhase(root string, intent *Intent, phase string) error {
	if phase != PhaseContent && phase != PhaseHistory {
		return fmt.Errorf("restore state: invalid phase %q", phase)
	}
	intent.Phase = phase
	return write(root, intent)
}

func write(root string, intent *Intent) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("restore state: encode intent: %w", err)
	}
	dir := filepath.Join(root, RelDir)
	//bn:direct-write creates a unique intent temporary inside the excluded restore directory while the exclusive checkpoint is held
	tmp, err := os.CreateTemp(dir, ".intent-*.tmp")
	if err != nil {
		return fmt.Errorf("restore state: create intent temporary for %s: %w", path(root), err)
	}
	name := tmp.Name()
	//bn:direct-write removes only the unique intent temporary created immediately above
	defer os.Remove(name)
	target := path(root)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restore state: secure intent temporary for %s: %w", target, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restore state: write intent temporary for %s: %w", target, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restore state: sync intent temporary for %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("restore state: close intent temporary for %s: %w", target, err)
	}
	//bn:direct-write atomically publishes the fsynced outer restore intent under the exclusive checkpoint before the next restore phase proceeds
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("restore state: publish intent %s: %w", target, err)
	}
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("restore state: sync recovery directory after publishing %s: %w", target, err)
	}
	return nil
}

func Clear(root string) error {
	dir := filepath.Join(root, RelDir)
	//bn:direct-write removes the completed outer intent only after content and Git history have converged under the exclusive checkpoint
	if err := os.Remove(path(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restore state: remove intent %s: %w", path(root), err)
	}
	if err := vault.SyncDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restore state: sync recovery directory: %w", err)
	}
	//bn:direct-write removes only the excluded recovery directory after its completed intent is durably absent under the exclusive checkpoint
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("restore state: remove recovery directory: %w", err)
	}
	if err := vault.SyncDir(filepath.Join(root, ".bnotes")); err != nil {
		return fmt.Errorf("restore state: sync sidecar directory after removing %s: %w", path(root), err)
	}
	return nil
}
