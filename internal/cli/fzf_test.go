package cli

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestRunFZFExitCode1TreatsNoSelectionAsAbort(t *testing.T) {
	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_EXIT_CODE=1")
		return cmd
	}

	selected, err := RunFZF([]string{"a.md", "b.md"}, nil)
	if err != nil {
		t.Fatalf("RunFZF: %v", err)
	}
	if selected != nil {
		t.Fatalf("RunFZF should treat exit 1 as abort, got %v", selected)
	}
}

// TestRunFZFExitCode130ReturnsInterrupted verifies that a Ctrl-C / Escape
// cancellation (fzf exit code 130) returns ErrFZFInterrupted instead of the
// previous (nil, nil) so callers can propagate exit code 130.
func TestRunFZFExitCode130ReturnsInterrupted(t *testing.T) {
	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_EXIT_CODE=130")
		return cmd
	}

	selected, err := RunFZF([]string{"a.md", "b.md"}, nil)
	if selected != nil {
		t.Fatalf("RunFZF: expected nil selection on interrupt, got %v", selected)
	}
	if !errors.Is(err, ErrFZFInterrupted) {
		t.Fatalf("RunFZF: expected ErrFZFInterrupted, got %v", err)
	}
}

// TestErrFZFNotFoundWrapsExecErrNotFound verifies that ErrFZFNotFound wraps
// exec.ErrNotFound so that the main exit-code mapper can detect it and return
// exit code 127 instead of 1.
func TestErrFZFNotFoundWrapsExecErrNotFound(t *testing.T) {
	if !errors.Is(ErrFZFNotFound, exec.ErrNotFound) {
		t.Fatalf("ErrFZFNotFound should wrap exec.ErrNotFound so exitCode() maps it to 127; got: %v", ErrFZFNotFound)
	}
}

// TestRunFZFNotFoundReturnsErrFZFNotFound verifies that RunFZF returns
// ErrFZFNotFound (which wraps exec.ErrNotFound) when fzf is absent.
func TestRunFZFNotFoundReturnsErrFZFNotFound(t *testing.T) {
	origLookPath := fzfLookPath
	t.Cleanup(func() { fzfLookPath = origLookPath })
	fzfLookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	_, err := RunFZF([]string{"a.md"}, nil)
	if !errors.Is(err, ErrFZFNotFound) {
		t.Fatalf("RunFZF: expected ErrFZFNotFound, got %v", err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("RunFZF: ErrFZFNotFound should wrap exec.ErrNotFound, got %v", err)
	}
}

func TestRunFZFHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FZF_HELPER") != "1" {
		return
	}
	code := os.Getenv("FZF_HELPER_EXIT_CODE")
	switch code {
	case "1":
		os.Exit(1)
	case "130":
		os.Exit(130)
	default:
		os.Exit(2)
	}
}
