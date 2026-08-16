package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetConfigEnv clears the env vars that influence vault resolution and pins
// HOME to a temp dir so a stray vault on the dev machine cannot leak in.
func resetConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BNOTES_VAULT", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EDITOR", "")
}

// runConfig executes the root command with the given args and returns
// (stdout, stderr, error).
func runConfig(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// stubExecReplace replaces execReplace with a recording stub for the duration
// of the test. It returns a pointer to the captured argv (nil until called).
func stubExecReplace(t *testing.T) *[]string {
	t.Helper()
	var captured []string
	prev := execReplace
	execReplace = func(name string, argv []string, env []string) error {
		captured = append([]string(nil), argv...)
		return nil
	}
	t.Cleanup(func() { execReplace = prev })
	return &captured
}

func TestConfigCmd_ReportsVaultResolvedFromEnv(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	cfgPath := useVaultRoot(t, root, "")

	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, root) {
		t.Errorf("expected vault root %q in output, got:\n%s", root, out)
	}
	if !strings.Contains(out, cfgPath) {
		t.Errorf("expected config path %q in output, got:\n%s", cfgPath, out)
	}
	if !strings.Contains(out, "$BNOTES_VAULT") {
		t.Errorf("expected $BNOTES_VAULT origin, got:\n%s", out)
	}
}

func TestConfigCmd_ReportsVaultResolvedFromFlag(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")

	out, _, err := runConfig(t, "--vault", root, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "--vault flag") {
		t.Errorf("expected --vault origin, got:\n%s", out)
	}
}

func TestConfigCmd_ReportsVaultDiscoveredUpward(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	deep := filepath.Join(root, "Projects", "Active")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(deep)

	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "upward search") {
		t.Errorf("expected upward-search origin, got:\n%s", out)
	}
}

func TestConfigCmd_ErrorsWhenNoVaultFound(t *testing.T) {
	resetConfigEnv(t)
	t.Chdir(t.TempDir())

	_, _, err := runConfig(t, "config")
	if err == nil {
		t.Fatal("expected an error when no brainnotes.toml can be found")
	}
	if !strings.Contains(err.Error(), "brainnotes.toml") {
		t.Errorf("expected error naming brainnotes.toml, got: %v", err)
	}
}

func TestConfigCmd_ErrorsWhenVaultHasNoConfigFile(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()

	_, _, err := runConfig(t, "--vault", root, "config")
	if err == nil {
		t.Fatal("expected an error for a vault without brainnotes.toml")
	}
	if !strings.Contains(err.Error(), "bn init") {
		t.Errorf("expected error pointing at `bn init`, got: %v", err)
	}
}

func TestConfigEdit_OpensVaultConfig(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("EDITOR", "vim")
	root := t.TempDir()
	const original = "# hello\n"
	cfgPath := useVaultRoot(t, root, original)

	captured := stubExecReplace(t)

	if _, _, err := runConfig(t, "config", "edit"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != original {
		t.Errorf("file content changed: got %q, want %q", data, original)
	}
	if len(*captured) != 2 || (*captured)[0] != "vim" || (*captured)[1] != cfgPath {
		t.Errorf("execReplace argv = %v, want [vim %s]", *captured, cfgPath)
	}
}

func TestConfigEdit_NoEditor(t *testing.T) {
	resetConfigEnv(t)
	useVaultRoot(t, t.TempDir(), "")

	_, _, err := runConfig(t, "config", "edit")
	if err == nil {
		t.Fatal("expected error when $EDITOR is unset")
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("expected error mentioning $EDITOR, got: %v", err)
	}
}

func TestConfigEdit_EditorArgsSupported(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("EDITOR", "go env")
	cfgPath := useVaultRoot(t, t.TempDir(), "")

	captured := stubExecReplace(t)

	if _, _, err := runConfig(t, "config", "edit"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{"go", "env", cfgPath}
	if got := *captured; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("execReplace argv = %v, want %v", got, want)
	}
}
