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
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var leftovers []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".txn-") {
			leftovers = append(leftovers, entry.Name())
		}
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
