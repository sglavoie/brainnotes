package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// assertFields reports every named field whose loaded value differs from want.
func assertFields[T comparable](t *testing.T, fields map[string][2]T) {
	t.Helper()
	for name, pair := range fields {
		if got, want := pair[0], pair[1]; got != want {
			t.Errorf("%s: got %#v, want %#v", name, got, want)
		}
	}
}

// assertSliceFields reports every named slice that differs from want.
func assertSliceFields(t *testing.T, fields map[string][2][]string) {
	t.Helper()
	for name, pair := range fields {
		if got, want := pair[0], pair[1]; !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}

// assertEmptyNonNil reports every named slice that is nil or not empty.
func assertEmptyNonNil(t *testing.T, fields map[string][]string) {
	t.Helper()
	for name, got := range fields {
		if got == nil || len(got) != 0 {
			t.Errorf("%s: got %#v, want empty non-nil slice", name, got)
		}
	}
}

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
