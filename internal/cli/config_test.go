package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// resetConfigEnv clears all env vars that influence config resolution and
// pins HOME to a temp dir so a stray ~/.config/bnotes/config.toml on the dev
// machine cannot leak into the tests.
func resetConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BNOTES_VAULT", "")
	t.Setenv("BNOTES_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EDITOR", "")
}

// runConfig executes the root command with the given args and returns
// (stdout, stderr, error). It saves and restores execReplace so tests can
// stub it without leaking state across tests.
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

func TestConfigCmd_PrintsActiveAndChain_WithBNotesConfig(t *testing.T) {
	resetConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", path)

	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}

	if !strings.Contains(out, "Active config: "+path) {
		t.Errorf("expected active line with path %q, got:\n%s", path, out)
	}
	if !strings.Contains(out, "$BNOTES_CONFIG") {
		t.Errorf("expected $BNOTES_CONFIG label, got:\n%s", out)
	}
	if !strings.Contains(out, "[active] [exists]") {
		t.Errorf("expected [active] [exists] tags on env line, got:\n%s", out)
	}
}

func TestConfigCmd_NoConfig_DefaultHome(t *testing.T) {
	resetConfigEnv(t)
	home := os.Getenv("HOME")
	wantPath := filepath.Join(home, ".config", "bnotes", "config.toml")

	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}

	if !strings.Contains(out, "Active config: none found") {
		t.Errorf("expected 'none found', got:\n%s", out)
	}
	if !strings.Contains(out, wantPath) {
		t.Errorf("expected default path %q in output, got:\n%s", wantPath, out)
	}
	if !strings.Contains(out, "[active] [missing]") {
		t.Errorf("expected [active] [missing] tags, got:\n%s", out)
	}
}

func TestConfigCmd_FlagBeatsEnv(t *testing.T) {
	resetConfigEnv(t)
	envPath := filepath.Join(t.TempDir(), "env.toml")
	if err := os.WriteFile(envPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", envPath)
	flagPath := "/tmp/does-not-exist/flag.toml"

	out, _, err := runConfig(t, "--config", flagPath, "config")
	if err != nil {
		t.Fatalf("execute: %v\nout: %s", err, out)
	}

	// The flag line should have [active] [missing].
	flagLineActive := false
	envLineActive := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "--config flag") && strings.Contains(line, flagPath) {
			if strings.Contains(line, "[active]") {
				flagLineActive = true
			}
		}
		if strings.Contains(line, "$BNOTES_CONFIG") && strings.Contains(line, envPath) {
			if strings.Contains(line, "[active]") {
				envLineActive = true
			}
		}
	}
	if !flagLineActive {
		t.Errorf("expected --config flag line to be [active], got:\n%s", out)
	}
	if envLineActive {
		t.Errorf("expected $BNOTES_CONFIG line NOT to be [active], got:\n%s", out)
	}
	if !strings.Contains(out, envPath) {
		t.Errorf("expected env path %q in output, got:\n%s", envPath, out)
	}
}

func TestConfigCmd_RunsWithoutVault(t *testing.T) {
	resetConfigEnv(t)

	_, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("expected success without vault, got: %v", err)
	}
}

func TestConfigEdit_OpensExisting(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("EDITOR", "vim")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const original = "hello"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", path)

	captured := stubExecReplace(t)

	_, _, err := runConfig(t, "config", "edit")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != original {
		t.Errorf("file content changed: got %q, want %q", data, original)
	}
	if len(*captured) != 2 || (*captured)[0] != "vim" || (*captured)[1] != path {
		t.Errorf("execReplace argv = %v, want [vim %s]", *captured, path)
	}
}

func TestConfigEdit_CreatesDefaultThenOpens(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("EDITOR", "vim")
	home := os.Getenv("HOME")
	wantPath := filepath.Join(home, ".config", "bnotes", "config.toml")

	captured := stubExecReplace(t)

	_, stderr, err := runConfig(t, "config", "edit")
	if err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr)
	}

	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("expected created file at %s: %v", wantPath, err)
	}
	if got, want := string(data), config.DefaultTemplate(); got != want {
		t.Errorf("created file content mismatch:\n got=%q\nwant=%q", got, want)
	}
	if len(*captured) != 2 || (*captured)[0] != "vim" || (*captured)[1] != wantPath {
		t.Errorf("execReplace argv = %v, want [vim %s]", *captured, wantPath)
	}
	if info, err := os.Stat(filepath.Dir(wantPath)); err != nil || !info.IsDir() {
		t.Errorf("expected parent dir to exist: err=%v", err)
	}
	if !strings.Contains(stderr, "created "+wantPath) {
		t.Errorf("expected 'created %s' notice on stderr, got: %q", wantPath, stderr)
	}
}

func TestConfigEdit_NoEditor(t *testing.T) {
	resetConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", path)
	// EDITOR is empty (resetConfigEnv set it).

	_, _, err := runConfig(t, "config", "edit")
	if err == nil {
		t.Fatal("expected error when $EDITOR is unset")
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("expected error mentioning $EDITOR, got: %v", err)
	}
}

func TestConfigEdit_RespectsConfigFlag(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("EDITOR", "vim")
	dir := t.TempDir()
	flagPath := filepath.Join(dir, "nested", "subdir", "custom.toml")

	captured := stubExecReplace(t)

	_, _, err := runConfig(t, "--config", flagPath, "config", "edit")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, err := os.ReadFile(flagPath)
	if err != nil {
		t.Fatalf("expected created file at %s: %v", flagPath, err)
	}
	if got, want := string(data), config.DefaultTemplate(); got != want {
		t.Errorf("created file content mismatch")
	}
	if len(*captured) != 2 || (*captured)[1] != flagPath {
		t.Errorf("execReplace argv = %v, want [vim %s]", *captured, flagPath)
	}
}

// errWriter is an io.Writer that always returns an error.
type errWriter struct{ err error }

func (ew errWriter) Write([]byte) (int, error) { return 0, ew.err }

func TestPrintResolution_PropagatesWriteError(t *testing.T) {
	want := errors.New("disk full")
	cands := []config.CandidatePath{
		{
			Label:  "--config flag",
			Path:   "/tmp/test.toml",
			Source: config.SourceFlag,
			Exists: true,
			Active: true,
		},
	}
	err := printResolution(errWriter{err: want}, cands)
	if err == nil {
		t.Fatal("expected error from broken writer, got nil")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped %v, got %v", want, err)
	}
}
