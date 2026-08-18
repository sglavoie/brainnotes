package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// pathLockRelDir holds one lock file per note path. It lives under the existing
	// hidden .bnotes directory so Walk already skips it.
	pathLockRelDir = ".bnotes/locks"
	// pathLockTimeout bounds how long a writer waits for another bn writer. It is
	// generous relative to a note rewrite so ordinary contention always waits it
	// out, and short enough that a stale holder cannot hang a command.
	pathLockTimeout = 5 * time.Second
	// pathLockPollInterval paces the non-blocking retry loop.
	pathLockPollInterval = 5 * time.Millisecond
)

// LockPaths takes one exclusive lock per path and returns the release function.
// It is how a writer that does not go through FileTxn joins the same mutual
// exclusion: replacing a note leaves its path transiently vacant, and a writer
// that ignores the locks can still take that vacancy and strand the other
// writer's content in a recovery file.
//
// Locks are taken in sorted path order, which is what makes concurrent
// multi-path callers deadlock-free. Release is idempotent and safe to defer.
//
// The locks are not re-entrant: every acquisition opens a fresh descriptor
// because flock ownership belongs to the open file description. A caller holding
// a lock on a path must not call into anything that locks the same path again,
// or it will wait out the full budget against itself.
func LockPaths(root string, paths ...string) (func(), error) {
	locks, err := acquirePathLocks(root, 0, paths)
	if err != nil {
		return func() {}, err
	}
	return locks.release, nil
}

// pathLocks is a set of held exclusive path locks.
type pathLocks struct {
	files []*os.File
}

// acquirePathLocks locks every path under root, in sorted order. A zero wait
// means pathLockTimeout. On any failure the locks it already took are released,
// so the caller never has to unwind a partial acquisition.
//
// Paths are resolved the same way FileTxn resolves its planned paths, so a
// caller passing a raw absolute path and a transaction passing an
// already-resolved one meet on the same lock file. Resolution is idempotent, so
// re-resolving a resolved path is a no-op.
func acquirePathLocks(root string, wait time.Duration, paths []string) (*pathLocks, error) {
	locks := &pathLocks{}
	if !lockingSupported || len(paths) == 0 {
		return locks, nil
	}
	if wait <= 0 {
		wait = pathLockTimeout
	}
	// Lock names have to be relative to the resolved root, or two processes
	// reaching one vault by different symlinked names would hash different lock
	// files and exclude nobody.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("vault: resolve vault root %q: %w", root, err)
	}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		abs, err := resolveVaultPath(root, path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, abs)
	}
	sort.Strings(resolved)

	dir := filepath.Join(realRoot, filepath.FromSlash(pathLockRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vault: create lock directory %q: %w", dir, err)
	}
	var previous string
	for i, path := range resolved {
		// Duplicates would deadlock against themselves, so collapse them. Sorting
		// first makes equal paths adjacent.
		if i > 0 && path == previous {
			continue
		}
		previous = path
		file, err := lockOne(filepath.Join(dir, lockFileName(realRoot, path)), path, wait)
		if err != nil {
			locks.release()
			return nil, err
		}
		locks.files = append(locks.files, file)
	}
	return locks, nil
}

// lockOne blocks until it owns path's lock or the wait budget runs out. A timeout
// means another writer is still working, not that anything changed, so it is
// reported as a retryable *TxnLockBusyError.
func lockOne(name, path string, wait time.Duration) (*os.File, error) {
	// A fresh descriptor per acquisition is required: flock locks belong to the
	// open file description, so a cached descriptor would make a process fail to
	// contend with itself.
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("vault: open lock %q: %w", name, err)
	}
	deadline := time.Now().Add(wait)
	for {
		held, err := tryLockExclusive(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("vault: lock %q: %w", path, err)
		}
		if held {
			return file, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, &TxnLockBusyError{Path: path}
		}
		time.Sleep(pathLockPollInterval)
	}
}

// release drops every held lock, newest first. Lock files are deliberately left
// on disk: unlinking one another process holds would let a third process create a
// new file at the same name and lock nothing.
func (l *pathLocks) release() {
	if l == nil {
		return
	}
	for i := len(l.files) - 1; i >= 0; i-- {
		file := l.files[i]
		_ = unlockFile(file)
		_ = file.Close()
	}
	l.files = nil
}

// lockFileName hashes the path so one flat directory can hold a lock for every
// note without mirroring the vault's folders or hitting name-length limits.
func lockFileName(root, path string) string {
	name := path
	if rel, err := filepath.Rel(root, path); err == nil {
		name = filepath.ToSlash(rel)
	}
	return HashBytes([]byte(name)) + ".lock"
}
