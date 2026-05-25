package cli

import (
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

func TestRunFZFHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FZF_HELPER") != "1" {
		return
	}
	code := os.Getenv("FZF_HELPER_EXIT_CODE")
	switch code {
	case "1":
		os.Exit(1)
	default:
		os.Exit(2)
	}
}
