package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// isolatedEnv reports every environment variable as unset so tests never pick
// up the developer's own $BNOTES_VAULT.
func isolatedEnv() func(string) string {
	return func(string) string { return "" }
}

// newVault creates a vault root whose brainnotes.toml holds body.
func newVault(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, FileName), body)
	return root
}

// loadVault loads the configuration of an explicitly named vault root.
func loadVault(t *testing.T, root string) (*Config, error) {
	t.Helper()
	return Load(LoadOptions{VaultFlag: root, Getenv: isolatedEnv()})
}

// loadTOML creates a vault root holding body and loads it.
func loadTOML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	return loadVault(t, newVault(t, body))
}

// resetEnv isolates the environment inputs that affect vault resolution and
// tilde expansion.
func resetEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envVault, "")
	// Force HOME into a temp dir so a stray vault in the developer's home
	// cannot leak into tilde-expansion tests.
	t.Setenv("HOME", t.TempDir())
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
