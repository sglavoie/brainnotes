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

// classifyStateReadError turns the read half of currentFileState into the right
// kind of failure. The stat said "regular file", so a read that now reports the
// path missing means another writer took it in between — takeCustody renames the
// destination aside, leaving exactly that window. That is a precondition
// mismatch, not an I/O fault, and every caller that retries a conflict must see
// it as one.
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
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", fmt.Errorf("vault: write %q: %w", tmpPath, err)
	}
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
