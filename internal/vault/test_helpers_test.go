package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// newTestVault writes the given files into a temp dir and returns an opened
// Vault. files maps vault-relative paths (forward slashes) to file contents.
// Directories along the way are created automatically; empty content with a
// trailing slash creates an empty directory.
func newTestVault(t *testing.T, ignore []string, files map[string]string) *Vault {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", full, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	cfg := &config.Config{
		Vault:     dir,
		Ignore:    ignore,
		Templates: config.TemplatesConfig{Dir: "Templates"},
	}
	v, err := Open(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}
