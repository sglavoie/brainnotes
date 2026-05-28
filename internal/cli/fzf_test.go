package cli

import (
	"errors"
	"fmt"
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
	if output := os.Getenv("FZF_HELPER_OUTPUT"); output != "" {
		fmt.Fprint(os.Stdout, output)
		os.Exit(0)
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

func TestRunFZFLines_NotFound(t *testing.T) {
	origLookPath := fzfLookPath
	t.Cleanup(func() { fzfLookPath = origLookPath })
	fzfLookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	_, err := RunFZFLines([]string{"/a.md\t1\ta.md:1:text"}, nil)
	if !errors.Is(err, ErrFZFNotFound) {
		t.Fatalf("RunFZFLines: expected ErrFZFNotFound, got %v", err)
	}
}

func TestRunFZFLines_ExitCode1(t *testing.T) {
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

	selected, err := RunFZFLines([]string{"/a.md\t1\ta.md:1:text"}, nil)
	if err != nil {
		t.Fatalf("RunFZFLines: expected nil error on exit 1, got %v", err)
	}
	if selected != nil {
		t.Fatalf("RunFZFLines: expected nil selection on exit 1, got %v", selected)
	}
}

func TestRunFZFLines_ExitCode130(t *testing.T) {
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

	selected, err := RunFZFLines([]string{"/a.md\t1\ta.md:1:text"}, nil)
	if selected != nil {
		t.Fatalf("RunFZFLines: expected nil selection on interrupt, got %v", selected)
	}
	if !errors.Is(err, ErrFZFInterrupted) {
		t.Fatalf("RunFZFLines: expected ErrFZFInterrupted, got %v", err)
	}
}

func TestRunFZFLines_DeduplicatesPaths(t *testing.T) {
	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	// Two records pointing to the same abs path; fzf "selects" both.
	fzfOutput := "/vault/a.md\t1\ta.md:1:line one\n/vault/a.md\t5\ta.md:5:line two\n"

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_OUTPUT="+fzfOutput)
		return cmd
	}

	selected, err := RunFZFLines([]string{
		"/vault/a.md\t1\ta.md:1:line one",
		"/vault/a.md\t5\ta.md:5:line two",
	}, nil)
	if err != nil {
		t.Fatalf("RunFZFLines: unexpected error: %v", err)
	}
	if len(selected) != 1 || selected[0] != "/vault/a.md" {
		t.Fatalf("RunFZFLines: expected [/vault/a.md], got %v", selected)
	}
}

func TestRunFZFLines_InjectsDelimiter(t *testing.T) {
	origLookPath := fzfLookPath
	origCommand := fzfCommand
	t.Cleanup(func() {
		fzfLookPath = origLookPath
		fzfCommand = origCommand
	})

	var capturedArgs []string

	fzfLookPath = func(string) (string, error) { return "fzf", nil }
	fzfCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string(nil), args...)
		cmdArgs := []string{"-test.run=TestRunFZFHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_FZF_HELPER=1", "FZF_HELPER_EXIT_CODE=1")
		return cmd
	}

	// Without extraArgs: --delimiter, --with-nth, and --nth must be injected.
	_, _ = RunFZFLines([]string{"/a.md\t1\ta.md:1:text"}, nil)
	has := func(flag string) bool {
		for _, a := range capturedArgs {
			if a == flag {
				return true
			}
		}
		return false
	}
	count := func(flag string) int {
		n := 0
		for _, a := range capturedArgs {
			if a == flag {
				n++
			}
		}
		return n
	}
	if !has("--delimiter") {
		t.Fatal("expected --delimiter to be injected when absent from extraArgs")
	}
	if !has("--with-nth") {
		t.Fatal("expected --with-nth to be injected when absent from extraArgs")
	}
	if !has("--nth") {
		t.Fatal("expected --nth to be injected when absent from extraArgs")
	}

	// With extraArgs already containing --delimiter, --with-nth, and --nth: must not duplicate.
	_, _ = RunFZFLines([]string{"/a.md\t1\ta.md:1:text"}, []string{"--delimiter", ",", "--with-nth", "2", "--nth", "3"})
	if count("--delimiter") != 1 {
		t.Fatalf("--delimiter should appear exactly once when already in extraArgs, got %d", count("--delimiter"))
	}
	if count("--with-nth") != 1 {
		t.Fatalf("--with-nth should appear exactly once when already in extraArgs, got %d", count("--with-nth"))
	}
	if count("--nth") != 1 {
		t.Fatalf("--nth should appear exactly once when already in extraArgs, got %d", count("--nth"))
	}

	// A caller-provided --with-nth changes the transformed row shape, so RunFZFLines
	// should not inject a default --nth that may target the wrong field.
	_, _ = RunFZFLines([]string{"/a.md\t1\ta.md\ttext"}, []string{"--with-nth", "3"})
	if count("--nth") != 0 {
		t.Fatalf("--nth should not be injected with caller-provided --with-nth, got %d", count("--nth"))
	}
}
