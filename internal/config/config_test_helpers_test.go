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

// resetEnv clears env vars that affect config loading and restores them at test end.
func resetEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envVault, "")
	t.Setenv(envConfig, "")
	t.Setenv(envXDG, "")
	// Force HOME into the temp dir so a stray ~/.config/bnotes/config.toml on the
	// developer's machine cannot leak into the tests.
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
