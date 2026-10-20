package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// buildErrorWordingVault is a two-note vault for the tests below that pin how a
// refusal is spelled: which command it names, and that it names what the user
// typed rather than an absolute path they never saw.
func buildErrorWordingVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	return buildVaultFromFiles(t, map[string]string{
		"Notes/Alpha.md": "---\ntitle: Alpha\ntags: [one]\n---\nAlpha links [[Beta]] and [[Missing]].\n",
		"Notes/Beta.md":  "---\ntitle: Beta\n---\nBeta body.\n",
	})
}

type errorWordingCase struct {
	name string
	args []string
	want string
}

func runErrorWordingCases(t *testing.T, root string, cases []errorWordingCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSuffix(runCLIOutcome(t, root, tc.args...), "\n")
			if got != tc.want {
				t.Fatalf("bn %s:\n got %q\nwant %q", strings.Join(tc.args, " "), got, tc.want)
			}
		})
	}
}

func TestErrorWordingNamesTheCommand(t *testing.T) {
	root := buildErrorWordingVault(t)
	runErrorWordingCases(t, root, []errorWordingCase{
		{
			name: "context missing note names context",
			args: []string{"context", "Nope"},
			want: `bn: context: no note titled "Nope", and no note at that path`,
		},
		{
			name: "related missing note names related",
			args: []string{"related", "Nope"},
			want: `bn: related: no note titled "Nope", and no note at that path`,
		},
		{
			name: "set unknown value type uses the enum wording",
			args: []string{"set", "Notes/Beta.md", "--value-type", "x", "k=v"},
			want: `bn: set: --value-type must be date, datetime, int, bool, float, or raw (got "x")`,
		},
	})
}

// TestMissingNoteErrorsNameWhatWasTyped pins one spelling for a note argument
// that names nothing, across the commands that take one: the command, then the
// argument as typed — never the vault package's sentinel, a bare stat of an
// absolute path, or a read error with no command at all.
func TestMissingNoteErrorsNameWhatWasTyped(t *testing.T) {
	root := buildErrorWordingVault(t)
	runErrorWordingCases(t, root, []errorWordingCase{
		{"rename title", []string{"rename", "Nope", "X"}, `bn: rename: no note titled "Nope", and no note at that path`},
		{"rename path", []string{"rename", "Notes/Nope.md", "X"}, `bn: rename: no note at Notes/Nope.md`},
		{"move title", []string{"move", "Nope", "Archive"}, `bn: move: no note titled "Nope", and no note at that path`},
		{"move path", []string{"move", "Notes/Nope.md", "Archive"}, `bn: move: no note at Notes/Nope.md`},
		{"set path", []string{"set", "Notes/Nope.md", "k=v"}, `bn: set: no note at Notes/Nope.md`},
		{"unset path", []string{"unset", "Notes/Nope.md", "k"}, `bn: unset: no note at Notes/Nope.md`},
		{"rename-key path", []string{"rename-key", "old", "new", "Notes/Nope.md"}, `bn: rename-key: no note at Notes/Nope.md`},
		{"replace path", []string{"replace", "k", "a", "b", "Notes/Nope.md"}, `bn: replace: no note at Notes/Nope.md`},
		{"set glob without match", []string{"set", "Nope/*.md", "k=v"}, `bn: set: no matches for glob "Nope/*.md"`},
	})
}

// TestResolveNoteInputsPrefixesEveryRefusal covers the refusals no command
// line above reaches: stdin input naming a missing note, and the two argument
// shapes refused before any path is resolved.
func TestResolveNoteInputsPrefixesEveryRefusal(t *testing.T) {
	root := buildErrorWordingVault(t)
	cases := []struct {
		name string
		args []string
		in   string
		want string
	}{
		{"stdin missing note", []string{"-"}, "Notes/Beta.md\nNotes/Nope.md\n", "set: no note at Notes/Nope.md"},
		{"dash among args", []string{"Notes/Beta.md", "-"}, "", "set: '-' may only be used as the sole note argument"},
		{"empty arg", []string{" "}, "", "set: empty note path argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveNoteInputs("set", root, tc.args, strings.NewReader(tc.in), false)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestMoveTargetErrorKeepsSentinel pins that move's refusal still wraps
// vault.ErrNoteNotFound rather than re-spelling rename's message as text.
func TestMoveTargetErrorKeepsSentinel(t *testing.T) {
	root := buildErrorWordingVault(t)
	v := openTestVault(t, root)
	_, err := resolveMoveTarget(v, "Nope")
	if !errors.Is(err, vault.ErrNoteNotFound) {
		t.Fatalf("errors.Is(err, vault.ErrNoteNotFound) = false for %v", err)
	}
}
