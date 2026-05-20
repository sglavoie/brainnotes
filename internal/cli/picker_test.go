package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func buildPickerVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Bravo.md":   "---\ntitle: Bravo\ntags:\n  - x\n---\nB.\n",
		"Alpha.md":   "---\ntitle: Alpha\ncategory: y\n---\nA.\n",
		"Charlie.md": "---\ntitle: Charlie\ntags:\n  - z\n---\nC.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func openVault(t *testing.T, root string) *vault.Vault {
	t.Helper()
	cfg := &config.Config{Vault: root}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

func TestCollectNotePaths(t *testing.T) {
	root := buildPickerVault(t)
	v := openVault(t, root)

	paths, err := collectNotePaths(v)
	if err != nil {
		t.Fatalf("collectNotePaths: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d", len(paths))
	}
	for i := 1; i < len(paths); i++ {
		if paths[i] < paths[i-1] {
			t.Fatalf("paths not sorted: %v", paths)
		}
	}
}

func TestCollectPropertyKeys(t *testing.T) {
	root := buildPickerVault(t)
	v := openVault(t, root)

	keys, err := collectPropertyKeys(v)
	if err != nil {
		t.Fatalf("collectPropertyKeys: %v", err)
	}
	want := []string{"category", "tags", "title"}
	if len(keys) != len(want) {
		t.Fatalf("expected %v, got %v", want, keys)
	}
	for i, k := range keys {
		if k != want[i] {
			t.Fatalf("keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestPickNotes_MultiFlag(t *testing.T) {
	root := buildPickerVault(t)
	v := openVault(t, root)
	cfg := &config.Config{Vault: root}

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })

	var capturedArgs []string
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		capturedArgs = append([]string(nil), extraArgs...)
		return []string{items[0]}, nil
	}

	_, err := pickNotes(v, cfg, true)
	if err != nil {
		t.Fatalf("pickNotes: %v", err)
	}
	found := false
	for _, a := range capturedArgs {
		if a == "--multi" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --multi in extraArgs, got %v", capturedArgs)
	}
}

func TestPickNotes_EmptyVault(t *testing.T) {
	root := t.TempDir()
	v := openVault(t, root)
	cfg := &config.Config{Vault: root}

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })

	pickerCalled := false
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return nil, nil
	}

	selected, err := pickNotes(v, cfg, false)
	if err != nil {
		t.Fatalf("pickNotes: %v", err)
	}
	if selected != nil {
		t.Fatalf("expected nil selection, got %v", selected)
	}
	if pickerCalled {
		t.Fatalf("picker should not be called on empty vault")
	}
}
