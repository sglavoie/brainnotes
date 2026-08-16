package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestInit_WritesStarterConfig(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()

	out, _, err := runConfig(t, "init", root)
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}

	path := filepath.Join(root, config.FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != config.DefaultTemplate() {
		t.Errorf("starter config content mismatch:\ngot=%q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode: got %v, want 0644", info.Mode().Perm())
	}
	if !strings.Contains(out, "created "+path) {
		t.Errorf("expected creation notice, got: %q", out)
	}
}

func TestInit_DefaultsToCurrentDirectory(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	t.Chdir(root)

	if _, _, err := runConfig(t, "init"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, config.FileName)); err != nil {
		t.Fatalf("expected %s in the current directory: %v", config.FileName, err)
	}
}

func TestInit_RefusesToOverwriteExistingConfig(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	const original = "ignore = [\"Drafts\"]\n"
	path := markVaultRoot(t, root, original)

	_, _, err := runConfig(t, "init", root)
	if err == nil {
		t.Fatal("expected an error for an existing config file")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected an already-exists error, got: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("existing config was modified: got %q, want %q", data, original)
	}
}

func TestInit_RefusesToWriteThroughSymlink(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.toml")
	const original = "# do not touch\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, config.FileName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, _, err := runConfig(t, "init", root)
	if err == nil {
		t.Fatal("expected an error for a symlinked config path")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected a symlink error, got: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("symlink target was written through: got %q", data)
	}
	info, lerr := os.Lstat(link)
	if lerr != nil {
		t.Fatalf("lstat link: %v", lerr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by a regular file")
	}
}

func TestInit_RequiresAnExistingDirectory(t *testing.T) {
	resetConfigEnv(t)

	missing := filepath.Join(t.TempDir(), "nope")
	if _, _, err := runConfig(t, "init", missing); err == nil {
		t.Fatal("expected an error for a missing directory")
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := runConfig(t, "init", file)
	if err == nil {
		t.Fatal("expected an error for a path that is not a directory")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected a not-a-directory error, got: %v", err)
	}
}

func TestInit_MakesTheDirectoryResolvableAsAVault(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()

	if _, _, err := runConfig(t, "init", root); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, _, err := runConfig(t, "--vault", root, "config")
	if err != nil {
		t.Fatalf("config after init: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, filepath.Join(root, config.FileName)) {
		t.Errorf("expected the new config path in output, got:\n%s", out)
	}
}
