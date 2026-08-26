package index

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// formatVersion identifies the on-disk layout. Bump it on any change to entry
// or state, however compatible it looks. A bump is a rebuild, never a
// migration: there is no user intent in this file to preserve, so migration
// code would be pure risk in exchange for one avoided walk.
const formatVersion = 1

// state is the gob-encoded file. Root is stored so a vault that was moved or
// copied — same relative paths, different place — is caught and rebuilt rather
// than answered from another vault's bytes.
type state struct {
	Version int
	Root    string
	Entries []entry
}

// prime loads the state file the first time it is needed. Every way this can
// fail resolves the same way: keep an empty cache, note the failure class once,
// and let the walk rebuild. Callers must hold ix.mu.
func (ix *Index) prime() {
	if ix.primed {
		return
	}
	ix.primed = true

	entries, class, err := ix.read()
	if err != nil {
		// A vault that has never been indexed is the ordinary path, not a
		// failure class worth telling anyone about.
		if !errors.Is(err, errNoState) {
			ix.warnOnce(class, err)
		}
		ix.entries = map[string]entry{}
		ix.cold = true
		return
	}
	ix.entries = entries
	// An empty state file is indistinguishable from an empty vault, and both
	// are a legitimate warm start; cold is about whether an earlier observation
	// exists at all, which at this point it does.
	ix.cold = false
}

// errNoState marks the ordinary cold start — no file yet — so it is not
// reported as a failure.
var errNoState = errors.New("index: no state file")

// read returns the cached entries from disk. The second return value names the
// failure class for once-per-process logging; it is empty on success and on a
// plain missing file.
func (ix *Index) read() (map[string]entry, string, error) {
	path := filepath.Join(ix.dir, stateFileName)

	// Lstat, not Stat: a symlink where the state file belongs is not something
	// to follow. The index writes only regular files, so anything else is
	// either corruption or someone aiming this process at a path it did not
	// choose.
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]entry{}, "", errNoState
		}
		return nil, "state file unreadable", err
	}
	if !info.Mode().IsRegular() {
		return nil, "state file is not a regular file", fmt.Errorf("mode %s", info.Mode())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "state file unreadable", err
	}
	if len(data) == 0 {
		return nil, "state file is empty", errors.New("zero bytes")
	}

	var decoded state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&decoded); err != nil {
		return nil, "state file could not be decoded", err
	}
	if decoded.Version != formatVersion {
		return nil, "state file has an unsupported version", fmt.Errorf("version %d, want %d", decoded.Version, formatVersion)
	}
	if decoded.Root != ix.root {
		return nil, "state file belongs to another vault root", fmt.Errorf("root %q, want %q", decoded.Root, ix.root)
	}

	entries := make(map[string]entry, len(decoded.Entries))
	for _, e := range decoded.Entries {
		if err := validEntry(e); err != nil {
			return nil, "state file is internally inconsistent", err
		}
		if _, dup := entries[e.Rel]; dup {
			return nil, "state file is internally inconsistent", fmt.Errorf("duplicate entry %q", e.Rel)
		}
		entries[e.Rel] = e
	}
	return entries, "", nil
}

// validEntry rejects a record that cannot have been written by this package.
// The hash check is what makes silent bit rot a rebuild rather than a wrong
// answer, and it is affordable: hashing a whole vault's note bytes is
// milliseconds next to re-reading them from disk.
func validEntry(e entry) error {
	switch {
	case e.Rel == "":
		return errors.New("entry with empty path")
	case filepath.IsAbs(e.Rel) || strings.HasPrefix(e.Rel, "/"):
		return fmt.Errorf("entry path %q is not vault-relative", e.Rel)
	case e.Rel == ".." || strings.HasPrefix(e.Rel, "../"):
		return fmt.Errorf("entry path %q escapes the vault root", e.Rel)
	case e.Size != int64(len(e.Data)):
		return fmt.Errorf("entry %q records size %d for %d bytes", e.Rel, e.Size, len(e.Data))
	case e.Hash != vault.HashBytes(e.Data):
		return fmt.Errorf("entry %q does not match its recorded hash", e.Rel)
	}
	return nil
}

// persist writes the current entries out. Every failure here is swallowed on
// purpose: a read-only vault or a full disk costs the next process a warm
// start and nothing else, so surfacing it would turn a performance regression
// into a user-visible error. Callers must hold ix.mu.
//
// The lock is vault.TryLockPath rather than a blocking acquire, and it names a
// file inside .bnotes/index/ rather than anything under .bnotes/locks/ — that
// directory's filenames are sha256(relpath) and belong to the note namespace.
// On contention the write is skipped entirely: another process is already
// writing an equally good revision, and this process keeps its in-memory cache
// either way. The index takes no vault.lock, shared or otherwise, so a refresh
// can never block behind an escalating writer nor make one wait.
func (ix *Index) persist() {
	if err := os.MkdirAll(ix.dir, 0o755); err != nil {
		ix.warnOnce("state directory could not be created", err)
		return
	}

	release, acquired, err := vault.TryLockPath(filepath.Join(ix.dir, lockFileName))
	defer release()
	if err != nil {
		ix.warnOnce("state lock could not be taken", err)
		return
	}
	if !acquired {
		return
	}

	records := make([]entry, 0, len(ix.entries))
	for _, e := range ix.entries {
		records = append(records, e)
	}
	// Sorted so two runs over the same vault produce byte-identical files,
	// which makes a diff of the state file mean something.
	sort.Slice(records, func(i, j int) bool { return records[i].Rel < records[j].Rel })

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{Version: formatVersion, Root: ix.root, Entries: records}); err != nil {
		ix.warnOnce("state could not be encoded", err)
		return
	}
	if err := writeAtomic(filepath.Join(ix.dir, stateFileName), buf.Bytes()); err != nil {
		ix.warnOnce("state file could not be written", err)
	}
}

// writeAtomic writes data to path via a temp file in the same directory
// followed by a rename, the same idiom vault's atomicWrite uses. That is what
// makes a torn read impossible without any reader-side locking: a reader either
// opens the old inode or the new one, never a half-written file.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
