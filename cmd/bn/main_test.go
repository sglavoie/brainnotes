package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cli"
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

func TestRunErrorPrintsAndReturnsTwo(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return errors.New("boom") })

	if code := run(stderr); code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
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
	stubChildExecute(t, func() error {
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
	stubChildExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "missing-editor", Err: exec.ErrNotFound})
	})

	if code := run(stderr); code != 127 {
		t.Fatalf("run() exit code = %d, want 127", code)
	}
	if got := stderr.String(); !strings.Contains(got, `bn: wrapped: exec: "missing-editor": executable file not found in $PATH`) {
		t.Fatalf("stderr = %q, want command-not-found error", got)
	}
}

func TestRunMapsMissingCommandPathTo127(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{
			Name: "./missing-editor",
			Err:  &os.PathError{Op: "stat", Path: "./missing-editor", Err: fs.ErrNotExist},
		})
	})

	if code := run(stderr); code != 127 {
		t.Fatalf("run() exit code = %d, want 127", code)
	}
	if got := stderr.String(); !strings.Contains(got, `bn: wrapped: exec: "./missing-editor": stat ./missing-editor:`) {
		t.Fatalf("stderr = %q, want missing-path error", got)
	}
}

func TestRunMapsNonExecutableCommandTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "non-executable", Err: fs.ErrPermission})
	})

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); !strings.Contains(got, `bn: wrapped: exec: "non-executable": permission denied`) {
		t.Fatalf("stderr = %q, want permission-denied error", got)
	}
}

func TestRunMapsExecFormatExecErrorTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "bad-binary", Err: syscall.ENOEXEC})
	})

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); got != "bn: wrapped: exec: \"bad-binary\": exec format error\n" {
		t.Fatalf("stderr = %q, want exec-format error", got)
	}
}

func TestRunMapsWrappedErrDotTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error {
		return fmt.Errorf("wrapped: %w", &exec.Error{Name: "./editor", Err: exec.ErrDot})
	})

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); !strings.Contains(got, "wrapped") || !strings.Contains(got, "./editor") || !strings.Contains(got, exec.ErrDot.Error()) {
		t.Fatalf("stderr = %q, want wrapped ErrDot error", got)
	}
}

func TestExitCodeMapsForkExecPermissionDeniedTo126(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &os.PathError{
		Op:   "fork/exec",
		Path: "/vault/scripts/non-executable",
		Err:  fs.ErrPermission,
	})

	if code := exitCode(&cli.ChildProcessError{Err: err}); code != 126 {
		t.Fatalf("exitCode() = %d, want 126", code)
	}
}

func TestExitCodeMapsExecMissingPathTo127(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &os.PathError{
		Op:   "exec",
		Path: "/vault/bin/missing-editor",
		Err:  fs.ErrNotExist,
	})

	if code := exitCode(&cli.ChildProcessError{Err: err}); code != 127 {
		t.Fatalf("exitCode() = %d, want 127", code)
	}
}

func TestExitCodeMapsExecFormatErrorTo126(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &os.PathError{
		Op:   "exec",
		Path: "/vault/bin/bad-editor",
		Err:  syscall.ENOEXEC,
	})

	if code := exitCode(&cli.ChildProcessError{Err: err}); code != 126 {
		t.Fatalf("exitCode() = %d, want 126", code)
	}
}

func TestRunMapsWrappedExecErrnoNotFoundTo127(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error { return fmt.Errorf("open: %w", syscall.ENOENT) })

	if code := run(stderr); code != 127 {
		t.Fatalf("run() exit code = %d, want 127", code)
	}
	if got := stderr.String(); got != "bn: open: no such file or directory\n" {
		t.Fatalf("stderr = %q, want wrapped syscall errno message", got)
	}
}

func TestRunMapsWrappedExecErrnoPermissionTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error { return fmt.Errorf("open: %w", syscall.EACCES) })

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); got != "bn: open: permission denied\n" {
		t.Fatalf("stderr = %q, want wrapped syscall errno message", got)
	}
}

func TestRunMapsWrappedExecErrnoExecFormatTo126(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubChildExecute(t, func() error { return fmt.Errorf("open: %w", syscall.ENOEXEC) })

	if code := run(stderr); code != 126 {
		t.Fatalf("run() exit code = %d, want 126", code)
	}
	if got := stderr.String(); got != "bn: open: exec format error\n" {
		t.Fatalf("stderr = %q, want wrapped syscall errno message", got)
	}
}

func TestRunMapsFZFNotFoundTo127(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return cli.ErrFZFNotFound })

	if code := run(stderr); code != 127 {
		t.Fatalf("run() exit code = %d, want 127 for ErrFZFNotFound", code)
	}
	// The error message must be printed so the user knows why bn exited.
	if got := stderr.String(); !strings.Contains(got, "fzf not found") {
		t.Fatalf("stderr = %q, want fzf-not-found message", got)
	}
}

func TestRunMapsFZFInterruptedTo130Silently(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return cli.ErrFZFInterrupted })

	if code := run(stderr); code != 130 {
		t.Fatalf("run() exit code = %d, want 130 for ErrFZFInterrupted", code)
	}
	// Ctrl-C is user-initiated; no error message should be printed.
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty (interrupt should be silent)", got)
	}
}

func TestRunMapsContextCanceledTo130Silently(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return context.Canceled })

	if code := run(stderr); code != 130 {
		t.Fatalf("run() exit code = %d, want 130 for context cancellation", code)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty (cancellation should be silent)", got)
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

// Exit 1 is reserved for a run that completed with a negative answer, so a
// script can tell it from an error (exit 2). Each is still printed.
func TestRunMapsNegativeOutcomesToOne(t *testing.T) {
	for name, err := range map[string]error{
		"lint":     &cli.LintError{Count: 3},
		"selected": fmt.Errorf("open: %w", cli.ErrNothingSelected),
		"canceled": fmt.Errorf("remove: %w", cli.ErrCanceled),
		"check":    fmt.Errorf("views check: %w", cli.ErrCheckFailed),
	} {
		t.Run(name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			stubExecute(t, func() error { return err })
			if code := run(stderr); code != 1 {
				t.Fatalf("run() exit code = %d, want 1", code)
			}
			if stderr.Len() == 0 {
				t.Fatal("a negative outcome must still say why")
			}
		})
	}
}

// stubChildExecute is stubExecute for a failure of a user-visible child (the
// editor, fzf, the `checkpoint run` command), which cli marks as such.
func stubChildExecute(t *testing.T, fn func() error) {
	t.Helper()
	stubExecute(t, func() error {
		if err := fn(); err != nil {
			return &cli.ChildProcessError{Err: err}
		}
		return nil
	})
}

// A subprocess bn runs for itself (git, npm) is not the user's child: its exit
// status must not leak out as bn's. Git's 128 would otherwise read as a signal.
func TestRunDoesNotLeakInternalSubprocessExitCode(t *testing.T) {
	for name, err := range map[string]error{
		"git exit":    fmt.Errorf("git preflight: %w", shellExitError(t, "exit 128")),
		"git missing": fmt.Errorf("git: %w", &exec.Error{Name: "git", Err: exec.ErrNotFound}),
		"bare errno":  fmt.Errorf("open note: %w", syscall.ENOENT),
	} {
		t.Run(name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			stubExecute(t, func() error { return err })
			if code := run(stderr); code != 2 {
				t.Fatalf("run() exit code = %d, want 2", code)
			}
		})
	}
}

func TestRunMapsChildExitCodeThroughWrapping(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error {
		return fmt.Errorf("checkpoint run: %w", &cli.ChildProcessError{Err: shellExitError(t, "exit 7")})
	})
	if code := run(stderr); code != 7 {
		t.Fatalf("run() exit code = %d, want 7", code)
	}
}

func TestRunMapsNoMatchToOne(t *testing.T) {
	stderr := &bytes.Buffer{}
	stubExecute(t, func() error { return fmt.Errorf("search: %w", cli.ErrNoMatch) })
	if code := run(stderr); code != 1 {
		t.Fatalf("run() exit code = %d, want 1", code)
	}
}
