package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestRunSuccess(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return nil })

	if code := run(stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunErrorPrintsAndReturnsOne(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return errors.New("boom") })

	if code := run(stderr); code != 1 {
		t.Fatalf("run() exit code = %d, want 1", code)
	}
	if got := stderr.String(); got != "bn: boom\n" {
		t.Fatalf("stderr = %q, want %q", got, "bn: boom\n")
	}
}

func TestRunBrokenPipeIsSilent(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return fmt.Errorf("write stdout: %w", syscall.EPIPE) })

	if code := run(stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunPreservesSubprocessExitCode(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", shellExitError(t, "exit 23"))
	})

	if code := run(stderr); code != 23 {
		t.Fatalf("run() exit code = %d, want 23", code)
	}
	if got := stderr.String(); !strings.Contains(got, "bn: wrapped: exit status 23\n") {
		t.Fatalf("stderr = %q, want wrapped subprocess failure", got)
	}
}

func TestRunMapsCommandNotFoundTo127(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "missing-editor", Err: exec.ErrNotFound})
	})

	if code := run(stderr); code != 127 {
		t.Fatalf("run() exit code = %d, want 127", code)
	}
	if got := stderr.String(); !strings.Contains(got, `bn: wrapped: exec: "missing-editor": executable file not found in $PATH`) {
		t.Fatalf("stderr = %q, want command-not-found error", got)
	}
}

func TestRunMapsNonExecutableCommandTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "non-executable", Err: fs.ErrPermission})
	})

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); !strings.Contains(got, `bn: wrapped: exec: "non-executable": permission denied`) {
		t.Fatalf("stderr = %q, want permission-denied error", got)
	}
}

func TestExitCodeMapsForkExecPermissionDeniedTo126(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &os.PathError{
		Op:   "fork/exec",
		Path: "/vault/scripts/non-executable",
		Err:  fs.ErrPermission,
	})

	if code := exitCode(err); code != 126 {
		t.Fatalf("exitCode() = %d, want 126", code)
	}
}

func stubExecute(t *testing.T, fn func() error) {
	t.Helper()
	prev := execute
	execute = fn
	t.Cleanup(func() { execute = prev })
}

func shellExitError(t *testing.T, script string) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected shell command to fail")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	return err
}
