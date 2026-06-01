package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVault_EditorSeamCalled(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origExec := vaultExec
	t.Cleanup(func() { vaultExec = origExec })

	var gotEditor, gotPath string
	vaultExec = func(editor, path string) error {
		gotEditor = editor
		gotPath = path
		return nil
	}
	t.Setenv("EDITOR", "true")

	if _, _, err := runOpen(t, vaultRoot, "vault"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotEditor != "true" {
		t.Fatalf("editor: got %q, want %q", gotEditor, "true")
	}
	if gotPath != vaultRoot {
		t.Fatalf("path: got %q, want %q", gotPath, vaultRoot)
	}
}

func TestVault_ConfigEditorTakesPriority(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origExec := vaultExec
	t.Cleanup(func() { vaultExec = origExec })

	var gotEditor string
	vaultExec = func(editor, path string) error {
		gotEditor = editor
		return nil
	}
	t.Setenv("EDITOR", "other-editor")

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+vaultRoot+"\"\n[open]\neditor = \"configured-editor\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"--config", cfgPath, "vault"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotEditor != "configured-editor" {
		t.Fatalf("editor: got %q, want %q (config should take priority over $EDITOR)", gotEditor, "configured-editor")
	}
}

func TestVault_MissingEditor(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origExec := vaultExec
	t.Cleanup(func() { vaultExec = origExec })

	vaultExec = func(editor, path string) error {
		return errors.New("seam should not be called")
	}
	t.Setenv("EDITOR", "")

	_, _, err := runOpen(t, vaultRoot, "vault")
	if err == nil {
		t.Fatalf("expected error when neither config editor nor $EDITOR is set")
	}
	if !strings.Contains(err.Error(), "[open].editor") || !strings.Contains(err.Error(), "$EDITOR") {
		t.Fatalf("error should mention both [open].editor and $EDITOR; got %v", err)
	}
}

func TestVault_RejectsArgs(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origExec := vaultExec
	t.Cleanup(func() { vaultExec = origExec })

	vaultExec = func(editor, path string) error {
		t.Fatalf("seam should not be called when args are rejected")
		return nil
	}
	t.Setenv("EDITOR", "true")

	_, _, err := runOpen(t, vaultRoot, "vault", "extra")
	if err == nil {
		t.Fatalf("expected error for positional arg")
	}
}
