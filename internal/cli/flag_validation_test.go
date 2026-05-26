package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadOnlyCommandsRejectUnexpectedArgs(t *testing.T) {
	_ = buildSetFixture(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "find", args: []string{"find", "unexpected"}},
		{name: "graph", args: []string{"graph", "unexpected"}},
		{name: "lint", args: []string{"lint", "unexpected"}},
		{name: "schema", args: []string{"schema", "unexpected"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runCLI(t, "", tt.args...)
			if err == nil {
				t.Fatalf("expected unexpected argument error, got nil with output:\n%s", out)
			}
		})
	}
}

func TestOpenNullRequiresPrint(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)

	origPicker, origExec := openPicker, openExec
	t.Cleanup(func() { openPicker = origPicker; openExec = origExec })

	pickerCalled := false
	openPicker = func(paths []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return nil, nil
	}
	openExec = func(editor, file string) error { return nil }

	stdout, stderr, err := runOpen(t, vaultRoot, "open", "--null")
	if err == nil {
		t.Fatalf("expected --null/--print validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--null requires --print") {
		t.Fatalf("expected --null/--print error, got %v", err)
	}
	if pickerCalled {
		t.Fatal("picker should not run when flags are invalid")
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestLinksNullCannotBeVerbose(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, stderr, err := runLinks(t, root, "links", "Hub", "--null", "--verbose")
	if err == nil {
		t.Fatal("expected --null/--verbose validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--null cannot be used with --verbose") {
		t.Fatalf("expected --null/--verbose error, got %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestBacklinksNullCannotBeVerbose(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, stderr, err := runBacklinks(t, root, "backlinks", "Target", "--null", "--verbose")
	if err == nil {
		t.Fatal("expected --null/--verbose validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--null cannot be used with --verbose") {
		t.Fatalf("expected --null/--verbose error, got %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestSetNullRequiresStdinNoteInput(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	before := readNote(t, notePath)

	out, err := runCLI(t, "", "set", "--null", notePath, "status=done")
	if err == nil {
		t.Fatalf("expected --null/stdin validation error, got nil with output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--null requires '-'") {
		t.Fatalf("expected --null/stdin error, got %v", err)
	}
	if got := readNote(t, notePath); got != before {
		t.Fatalf("invalid --null invocation modified the note:\n%s", got)
	}
}

func TestUnsetNullRequiresStdinNoteInput(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	before := readNote(t, notePath)

	out, err := runCLI(t, "", "unset", "--null", notePath, "tags")
	if err == nil {
		t.Fatalf("expected --null/stdin validation error, got nil with output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--null requires '-'") {
		t.Fatalf("expected --null/stdin error, got %v", err)
	}
	if got := readNote(t, notePath); got != before {
		t.Fatalf("invalid --null invocation modified the note:\n%s", got)
	}
}

func TestMutationCommandsExposeGraphFindFlags(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{name: "replace", cmd: NewReplaceCmd},
		{name: "repair", cmd: NewRepairCmd},
		{name: "relink", cmd: NewRelinkCmd},
		{name: "rename-key", cmd: NewRenameKeyCmd},
	}
	flagNames := []string{"broken-links", "min-backlinks", "max-backlinks", "min-links", "max-links", "dead-end"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.cmd()
			for _, name := range flagNames {
				if cmd.Flag(name) == nil {
					t.Fatalf("expected %s to expose --%s", tt.name, name)
				}
			}
		})
	}
}
