package journal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func normalizeWithinRoot(root, p string) (abs, relSlash string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", fmt.Errorf("empty root")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve root symlinks %q: %w", rootAbs, err)
	}
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("empty path")
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve path %q: %w", p, err)
	}
	targetParentResolved, err := resolveExistingPath(filepath.Dir(targetAbs))
	if err != nil {
		return "", "", fmt.Errorf("resolve parent %q: %w", filepath.Dir(targetAbs), err)
	}
	targetAbs = filepath.Join(targetParentResolved, filepath.Base(targetAbs))
	rel, err := filepath.Rel(rootResolved, targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("rel %q: %w", targetAbs, err)
	}
	if isEscapingRel(rel) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("path %q resolves outside root %q", p, rootAbs)
	}
	relSlash = filepath.ToSlash(rel)
	return targetAbs, relSlash, nil
}

func resolveExistingPath(path string) (string, error) {
	path = filepath.Clean(path)
	missing := make([]string, 0, 4)
	current := path
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("eval symlinks %q: %w", current, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("lstat %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func isEscapingRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == ".." {
		return true
	}
	return strings.HasPrefix(rel, "../")
}

func snapshotFileName(relSlash string) string {
	sum := sha256.Sum256([]byte(relSlash))
	return hex.EncodeToString(sum[:]) + ".snap"
}

// fileStateFromDisk reads a path's pre-change state together with its bytes, for
// the one caller that has to snapshot them. Anything that is not a regular file
// is refused rather than described: there is nothing to snapshot, so recording a
// before-state for it would promise an undo that cannot happen. Callers that only
// need the state use fileStateOnly, which never reads the file.
func fileStateFromDisk(path string) (FileState, []byte, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return FileState{Kind: KindMissing}, nil, nil
	default:
		return FileState{}, nil, err
	}

	switch {
	case info.Mode().IsRegular():
		data, err := os.ReadFile(path)
		if err != nil {
			return FileState{}, nil, err
		}
		sum := sha256.Sum256(data)
		return FileState{
			Kind:   KindFile,
			SHA256: hex.EncodeToString(sum[:]),
			Size:   int64(len(data)),
			Mode:   uint32(info.Mode().Perm()),
		}, data, nil
	case info.IsDir():
		return FileState{}, nil, fmt.Errorf("path %q is a directory", path)
	default:
		return FileState{}, nil, fmt.Errorf("path %q is not a regular file", path)
	}
}

// fileStateOnly reports a path's state without reading it into memory.
//
// It is fileStateFromDisk's non-strict half for the two callers that discard the
// bytes: finalizing an entry, which only records the after-state's digest, and
// verifying a restore target. Both may be pointed at an attachment rather than a
// note, and hashing one by reading it in full would put a file the write itself
// deliberately streamed back into memory.
func fileStateOnly(path string) (FileState, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return FileState{Kind: KindMissing}, nil
	default:
		return FileState{}, err
	}
	switch {
	case info.Mode().IsRegular():
		digest, size, err := vault.HashFile(path)
		if err != nil {
			return FileState{}, err
		}
		return FileState{
			Kind:   KindFile,
			SHA256: digest,
			Size:   size,
			Mode:   uint32(info.Mode().Perm()),
		}, nil
	case info.IsDir():
		return FileState{Kind: KindDirectory}, nil
	default:
		return FileState{Kind: KindOther}, nil
	}
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".journal-*")
	if err != nil {
		return fmt.Errorf("create temp in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %q: %w", tmpPath, err)
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, path, err)
	}
	// The rename is atomic against a concurrent reader the moment it returns, but
	// the directory entry naming it is not durable until the directory is synced.
	// That matters here more than anywhere else in this package: a finalized
	// manifest is the *only* thing that authorizes committing a deletion, so a
	// manifest that a crash un-names turns an explained removal into an
	// unexplained one and blocks the vault over a change bn itself made.
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("sync %q: %w", dir, err)
	}
	return nil
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(path, data, 0o644)
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return err
	}
	return nil
}

func defaultEntryID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
