package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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

// TestFilterErrorsNameTheCommand pins the command prefix on refusals raised
// inside the shared filter surface and the config inspector, which used to
// reach the terminal as bare `--age: ...` or the wrong command's name.
func TestFilterErrorsNameTheCommand(t *testing.T) {
	root := buildErrorWordingVault(t)
	noViews := `unknown view "nope": no views are defined in ` + config.FileName
	runErrorWordingCases(t, root, []errorWordingCase{
		{"age without operator hints", []string{"find", "--age", "30d"}, `bn: find: --age: invalid age expression "30d": must start with >, <, or =; did you mean >30d?`},
		{"created bad unit has no hint", []string{"find", "--created", ">3x"}, `bn: find: --created: invalid age unit "x" in ">3x": must be one of s (seconds), m (minutes), h (hours), d (days)`},
		{"age missing unit", []string{"find", "--age", ">30"}, `bn: find: --age: invalid age expression ">30": missing unit; end it with one of s (seconds), m (minutes), h (hours), d (days), like >30d`},
		{"age fractional count", []string{"find", "--age", ">1.5d"}, `bn: find: --age: invalid age value in ">1.5d": "1.5" is not a whole number`},
		{"link bound range", []string{"find", "--min-links", "-3"}, `bn: find: --min-links must be 0 or more, or -1 to disable (got -3)`},
		{"link bounds crossed", []string{"graph", "--min-links", "3", "--max-links", "1"}, `bn: graph: --min-links (3) cannot exceed --max-links (1)`},
		{"find unknown view", []string{"find", "--view", "nope"}, "bn: find: " + noViews},
		{"search unknown view", []string{"search", "x", "--view", "nope"}, "bn: search: " + noViews},
		{"views show unknown view", []string{"views", "show", "nope"}, "bn: views show: " + noViews},
		{"config explain unknown key", []string{"config", "explain", "nope.key"}, `bn: config explain: unknown configuration key "nope.key"`},
	})

	// A parse error still renders as a caret block, under the same prefix.
	got := runCLIOutcome(t, root, "find", "-q", "ratign>3")
	if !strings.HasPrefix(got, "bn: find: query: unknown field") || !strings.Contains(got, "^") {
		t.Fatalf("parse error render:\n%s", got)
	}
}

// TestNoArgsLeafCommandsSayTheyTakeNoArguments pins the refusal of a stray
// positional argument on a leaf command: it takes none, rather than cobra's
// "unknown command", with a -q hint where -q exists. A parent command keeps
// "unknown command", because there the word really is a subcommand name.
func TestNoArgsLeafCommandsSayTheyTakeNoArguments(t *testing.T) {
	root := buildErrorWordingVault(t)
	runErrorWordingCases(t, root, []errorWordingCase{
		{"find hints -q", []string{"find", "tag:foo"}, `bn: find: takes no arguments (got "tag:foo"); did you mean -q tag:foo?; see 'bn find --help'`},
		{"lint hints -q with quoting", []string{"lint", "tag:a", "and", "tag:b"}, `bn: lint: takes no arguments (got "tag:a" "and" "tag:b"); did you mean -q "tag:a and tag:b"?; see 'bn lint --help'`},
		{"graph hints -q", []string{"graph", "x"}, `bn: graph: takes no arguments (got "x"); did you mean -q x?; see 'bn graph --help'`},
		{"history has no -q", []string{"history", "x"}, `bn: history: takes no arguments (got "x"); see 'bn history --help'`},
		{"nested leaf", []string{"views", "check", "x"}, `bn: views check: takes no arguments (got "x"); see 'bn views check --help'`},
		{"parent keeps unknown command", []string{"views", "nope"}, `bn: views: unknown command "nope" for "bn views"; see 'bn views --help'`},
	})
}

// TestRestoreMissingArchiveNamesWhatWasTyped pins `bn restore` on an archive
// that does not exist: the restore label, the path as typed, and where a
// relative path is looked up — not an lstat of an absolute path.
func TestRestoreMissingArchiveNamesWhatWasTyped(t *testing.T) {
	root := buildErrorWordingVault(t)
	missingAbs := filepath.Join(t.TempDir(), "gone.tar.gz")
	runErrorWordingCases(t, root, []errorWordingCase{
		{"relative", []string{"restore", "nope.tar"}, "bn: restore: no archive at nope.tar (relative paths resolve from the vault root; see backups/)"},
		{"relative dry run", []string{"restore", "--dry-run", "backups/nope.tar.gz"}, "bn: restore: no archive at backups/nope.tar.gz (relative paths resolve from the vault root; see backups/)"},
		{"absolute", []string{"restore", missingAbs}, "bn: restore: no archive at " + missingAbs},
	})
}

// TestSetUnsetReportUpdatedLikeTheOtherMutators pins `updated: <relpath>`,
// the verb-first, vault-relative form replace/relink/repair print, for set and
// unset too.
func TestSetUnsetReportUpdatedLikeTheOtherMutators(t *testing.T) {
	root := buildErrorWordingVault(t)
	runErrorWordingCases(t, root, []errorWordingCase{
		{"set", []string{"set", "Notes/Beta.md", "status=wip"}, "updated: Notes/Beta.md"},
		{"unset", []string{"unset", "Notes/Beta.md", "status"}, "updated: Notes/Beta.md"},
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
