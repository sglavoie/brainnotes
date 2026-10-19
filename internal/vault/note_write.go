package vault

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWrite writes data to dest atomically by writing a sibling temp file,
// fsyncing it, and installing it at dest. A destination that already exists is
// replaced through the same path-preserving compare-and-swap a FileTxn commit
// uses, with preimage naming the revision the caller planned this write against;
// a destination that does not exist yet is created exclusively. The temp file is
// removed on any error so it does not linger.
//
// The replacement never vacates dest, and it never lands if the bytes it
// displaces are not the ones preimage describes — an editor that saved the note
// since the caller read it keeps its version and gets a *TxnConflictError back.
//
// If dest already exists, its file mode is preserved across the rewrite. Go's
// os.CreateTemp creates files with mode 0600, so without this step every Save
// would silently strip group/world read bits from a 0644 note. When dest does
// not exist (new note), the mode is derived from a sibling probe file created
// with 0666 so the final note respects the process umask instead of forcing
// world-readable 0644 permissions. The mode travels on the temp inode, which the
// exchange moves to dest, so it is applied before the swap rather than after.
// atomicWrite installs data at dest without ever vacating dest's name.
//
// newFileMode is the mode to give a destination that does not exist yet. Zero
// means "probe the directory", which is what a note wants: the mode a file
// created there would naturally get. A caller that keeps durable state rather
// than a note passes an explicit mode instead, so its state file does not
// inherit whatever umask the process happened to be started with.
func atomicWrite(dest string, data []byte, preimage FileExpectation, newFileMode os.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("note: mkdir %q: %w", dir, err)
	}
	destMode, destExists, err := statWriteDestination(dest)
	if err != nil {
		return err
	}
	tmpPath, err := stageNoteTemp(dir, data)
	if err != nil {
		return err
	}
	// Apply the destination's prior mode (or a sensible default for a new file)
	// before the rename so the final inode lands with the right perms. Doing
	// this on the temp path keeps the operation atomic with respect to readers.
	wantMode, err := stagedNoteMode(dir, destMode, destExists, newFileMode)
	if err == nil {
		if chmodErr := os.Chmod(tmpPath, wantMode); chmodErr != nil {
			err = fmt.Errorf("note: chmod %q: %w", tmpPath, chmodErr)
		}
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := installNote(dest, tmpPath, destExists, preimage); err != nil {
		return err
	}
	// fsync the parent directory so the rename is durable across power loss.
	// POSIX guarantees rename atomicity within a running system, but the
	// directory entry update isn't persisted until the directory itself is
	// synced. The note is already installed when this fails, so the error says
	// so: the caller can see the new content but cannot claim it is durable.
	if err := SyncDir(dir); err != nil {
		return &installedNotDurableError{Path: dest, Err: err}
	}
	return nil
}

// statWriteDestination captures the existing mode, if any, before the temp file
// is written. Stat errors other than "not exist" propagate so a file that could
// not be introspected is not silently clobbered. exists distinguishes "file has
// 0o000 permissions" from "file does not exist", so the mode applied later does
// not silently widen a deliberately restrictive mode.
func statWriteDestination(dest string) (mode os.FileMode, exists bool, err error) {
	info, err := os.Stat(dest)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("note: stat %q: %w", dest, err)
	}
	// An exchange against a directory would succeed and swap the note with it,
	// where the old plain rename simply failed. Refuse anything that is not a
	// regular file before the primitive can be that literal.
	if !info.Mode().IsRegular() {
		return 0, false, &TxnConflictError{Path: dest, Expected: "file", Actual: nonRegularLabel(info)}
	}
	return info.Mode().Perm(), true, nil
}

// stageNoteTemp writes data to a fresh fsynced temp file in dir, removing it on
// any failure.
func stageNoteTemp(dir string, data []byte) (string, error) {
	tmp, tmpPath, err := openExclusiveTemp(dir, ".note-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("note: tempfile in %q: %w", dir, err)
	}
	if _, err := tmp.Write(data); err != nil {
		err = fmt.Errorf("note: write %q: %w", tmpPath, err)
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		err = fmt.Errorf("note: fsync %q: %w", tmpPath, err)
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("note: close %q: %w", tmpPath, err)
	}
	return tmpPath, nil
}

// stagedNoteMode is the mode the staged file must carry: the destination's own
// when it exists, else newFileMode, else what a file created in dir would get.
func stagedNoteMode(dir string, destMode os.FileMode, destExists bool, newFileMode os.FileMode) (os.FileMode, error) {
	if destExists {
		return destMode, nil
	}
	if newFileMode != 0 {
		return newFileMode, nil
	}
	return defaultNewFileMode(dir)
}

// installedNotDurableError reports a write whose bytes are installed at Path but
// whose directory entry could not be flushed. It is an error, not a warning:
// the content may not survive a power loss. It stays distinguishable so a caller
// tracking the on-disk revision knows the revision did change.
type installedNotDurableError struct {
	Path string
	Err  error
}

func (e *installedNotDurableError) Error() string {
	return fmt.Sprintf("note: %q installed but its directory could not be synced: %v", e.Path, e.Err)
}

func (e *installedNotDurableError) Unwrap() error { return e.Err }

// installNote moves the staged bytes onto dest and owns the temp name from that
// point on, including deciding when it may be deleted.
//
// A destination that exists is replaced by the compare-and-swap, so it is never
// absent and never loses a concurrent writer's version. A destination that does
// not exist is created exclusively; if a file appeared between the stat and the
// create, the write is retried as a replacement against the same expectation,
// which is what keeps a caller who asked for an unconditional write getting one
// without ever opening a hole at the path.
func installNote(dest, staged string, destExists bool, preimage FileExpectation) error {
	if !destExists {
		err := placeNew(nil, staged, dest)
		if err == nil {
			return nil
		}
		if !IsTxnConflict(err) {
			// Nothing landed, so the staged bytes are this function's to drop.
			_ = os.Remove(staged)
			return err
		}
	}
	outcome, err := compareAndSwap(nil, staged, dest, preimage)
	if err != nil {
		if !outcome.sourceConsumed {
			_ = os.Remove(staged)
		}
		// Otherwise the temp name holds content this call did not create — a
		// concurrent writer's version the error names for recovery — and
		// deleting it would destroy the thing the error exists to preserve.
		return err
	}
	// The note is on disk under its own name. outcome.cleanup only ever reports a
	// temp file that could not be unlinked afterwards, which the vault walk
	// already ignores; failing the save over it would tell the caller a lie about
	// the note.
	return nil
}

func openExclusiveTemp(dir, prefix, suffix string, perm os.FileMode) (*os.File, string, error) {
	const attempts = 1000
	for i := 0; i < attempts; i++ {
		var randBytes [8]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return nil, "", fmt.Errorf("note: random temp suffix: %w", err)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s%x%s", prefix, randBytes, suffix))
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return f, path, nil
		}
		if os.IsExist(err) {
			continue
		}
		return nil, "", err
	}
	return nil, "", fmt.Errorf("note: unique temp file in %q: too many collisions", dir)
}

func defaultNewFileMode(dir string) (os.FileMode, error) {
	probe, probePath, err := openExclusiveTemp(dir, ".note-mode-", ".tmp", 0o666)
	if err != nil {
		return 0, fmt.Errorf("note: probe tempfile in %q: %w", dir, err)
	}
	cleanup := func() { _ = os.Remove(probePath) }
	if err := probe.Close(); err != nil {
		cleanup()
		return 0, fmt.Errorf("note: close %q: %w", probePath, err)
	}
	info, err := os.Stat(probePath)
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("note: stat %q: %w", probePath, err)
	}
	cleanup()
	return info.Mode().Perm(), nil
}
