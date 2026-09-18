package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func (t *FileTxn) cleanupStaged() {
	for _, path := range t.order {
		if change := t.changes[path]; change != nil && change.staged != "" {
			_ = os.Remove(change.staged)
			change.staged = ""
		}
	}
}

// cleanupStagedAfter drops the temp files for paths at or after the failed one;
// the earlier paths are already applied and own no temp file.
func (t *FileTxn) cleanupStagedAfter(failedPath string) {
	seen := false
	for _, path := range t.order {
		if path == failedPath {
			seen = true
		}
		if !seen {
			continue
		}
		if change := t.changes[path]; change != nil && change.staged != "" {
			_ = os.Remove(change.staged)
			change.staged = ""
		}
	}
}

func currentFileState(path string) (data []byte, mode os.FileMode, exists bool, err error) {
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil:
	case os.IsNotExist(statErr):
		return nil, 0, false, nil
	default:
		return nil, 0, false, fmt.Errorf("vault: stat %q: %w", path, statErr)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, &TxnConflictError{
			Path:     path,
			Expected: "file",
			Actual:   nonRegularLabel(info),
		}
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, 0, false, classifyStateReadError(path, readErr)
	}
	return data, info.Mode().Perm(), true, nil
}

// currentFileDigest is currentFileState for a caller that only needs to know
// which bytes are there, not what they say. It stats, refuses the same
// non-regular destinations, and streams the file through a hasher.
//
// Recovery is the caller that makes this necessary. A recovery intent names the
// prior and planned digests, so every question it asks of a destination is a
// digest comparison — and the destination may be a 25 MiB attachment a streamed
// create was in the middle of installing, which is exactly the residency that
// whole path was built to avoid.
func currentFileDigest(path string) (digest string, exists bool, err error) {
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil:
	case os.IsNotExist(statErr):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("vault: stat %q: %w", path, statErr)
	}
	if !info.Mode().IsRegular() {
		return "", false, &TxnConflictError{
			Path:     path,
			Expected: "file",
			Actual:   nonRegularLabel(info),
		}
	}
	digest, _, hashErr := HashFile(path)
	if hashErr != nil {
		return "", false, classifyStateReadError(path, hashErr)
	}
	return digest, true, nil
}

// classifyStateReadError turns a read that follows a successful stat into the
// right kind of failure. The stat said "regular file", so a read that now
// reports the path missing means another writer unlinked it in between — a
// concurrent delete, or the custody rename a delete performs. That is a
// precondition mismatch, not an I/O fault, and every caller that retries a
// conflict must see it as one.
//
// compareAndSwap reuses it for the displaced copy, where a missing file means
// something outside this process removed a temp file it does not own; a conflict
// is the right verdict there too, because the transaction cannot prove what it
// just replaced and must not keep the replacement.
func classifyStateReadError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &TxnConflictError{Path: path, Expected: "file", Actual: "missing"}
	}
	return fmt.Errorf("vault: read %q: %w", path, err)
}

func nonRegularLabel(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "irregular"
	}
}

// VerifyState reports whether path currently satisfies expect, returning a
// *TxnConflictError when it does not. It lets a caller revalidate a dependency
// at plan time so a doomed plan is refused before it is offered to a user;
// passing the same expectation to FileTxn.Require is still required, because
// only the transaction rechecks it at the commit boundary.
//
// path must already be absolute and vault-contained. No containment check is
// applied here.
func VerifyState(path string, expect FileExpectation) error {
	return verifyCurrentState(path, expect)
}

func verifyCurrentState(path string, expect FileExpectation) error {
	data, _, exists, err := currentFileState(path)
	if err != nil {
		return err
	}
	return verifyExpectation(path, expect, data, exists)
}

func verifyExpectation(path string, expect FileExpectation, data []byte, exists bool) error {
	actual := "missing"
	if exists {
		actual = "file(sha256=" + HashBytes(data) + ")"
	}
	if expect.Exists != exists {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: actual}
	}
	if expect.Exists && expect.SHA256 != "" && expect.SHA256 != HashBytes(data) {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: actual}
	}
	return nil
}

// stageTempFile writes data to a sibling temp file with the final mode already
// applied, so committing it is a single rename.
func stageTempFile(dest string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("vault: mkdir %q: %w", dir, err)
	}
	tmp, tmpPath, err := openExclusiveTemp(dir, ".txn-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("vault: tempfile in %q: %w", dir, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vault: write %q: %w", tmpPath, err)
	}
	return finishTempFile(tmp, tmpPath, mode)
}

// finishTempFile makes a staged temp file durable and gives it the mode its
// destination will have, so installing it is a single rename. It owns tmp from
// here on and removes the file on any failure, because a staged copy that never
// became durable describes nothing a later step could finish.
func finishTempFile(tmp *os.File, tmpPath string, mode os.FileMode) (string, error) {
	cleanup := func() { _ = os.Remove(tmpPath) }
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", fmt.Errorf("vault: fsync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("vault: close %q: %w", tmpPath, err)
	}
	if mode == 0 {
		mode = 0o644
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return "", fmt.Errorf("vault: chmod %q: %w", tmpPath, err)
	}
	return tmpPath, nil
}

func syncParentDirs(paths []string) {
	synced := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		dir := filepath.Dir(path)
		if _, ok := synced[dir]; ok {
			continue
		}
		synced[dir] = struct{}{}
		if f, err := os.Open(dir); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
}
