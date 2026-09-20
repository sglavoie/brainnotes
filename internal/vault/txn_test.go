package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func txnTestRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func stagedTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	paths := stagedTempPaths(t, dir)
	var leftovers []string
	for _, path := range paths {
		leftovers = append(leftovers, filepath.Base(path))
	}
	return leftovers
}

func stagedTempPaths(t *testing.T, dir string) []string {
	t.Helper()
	var leftovers []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".txn-") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dir %s: %v", dir, err)
	}
	return leftovers
}

type captureRecorder struct {
	paths []string
	err   error
}

func (c *captureRecorder) Capture(path string) error {
	if c.err != nil {
		return c.err
	}
	c.paths = append(c.paths, path)
	return nil
}

// interloperRecorder overwrites one of the transaction's paths from inside the
// journal capture, which is the widest part of the window between staging and
// the first rename.
type interloperRecorder struct {
	inner *captureRecorder
	on    string
}

func (r interloperRecorder) Capture(path string) error {
	if err := r.inner.Capture(path); err != nil {
		return err
	}
	if strings.HasSuffix(path, r.on) {
		return os.WriteFile(path, []byte("someone-else\n"), 0o644)
	}
	return nil
}
