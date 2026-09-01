package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// commentedAutosaveConfig is the fixture most of these tests run against: an
// assignment with a comment above it and another on the line itself, plus an
// unrelated section on either side. Every "preserved every other byte" claim is
// measured against it.
const commentedAutosaveConfig = `[frontmatter.properties.tags]
type  = "list"
items = "string"

# The watcher may not commit more often than this.
[autosave]
min_interval = "5m" # slow on purpose

[capture]
inbox_note = "Inbox.md"
`

// configVault marks a temp dir as a vault holding body and returns the root and
// the config path.
func configVault(t *testing.T, body string) (string, string) {
	t.Helper()
	resetConfigEnv(t)
	root := t.TempDir()
	return root, useVaultRoot(t, root, body)
}

func readConfigFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// assertNoMutationArtifacts is how every "wrote nothing" claim is checked. A
// command that got as far as a recorder, a lock, or a transaction leaves
// .bnotes behind even when it wrote no note, so its absence is the strongest
// available statement that nothing started.
func assertNoMutationArtifacts(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf(".bnotes exists under %s (stat err = %v); the command left an artifact behind", root, err)
	}
}

func assertJournalEntries(t *testing.T, root string, want int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".bnotes", "journal"))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(entries) != want {
		t.Fatalf("journal holds %d entries, want %d", len(entries), want)
	}
}

func TestConfigGet_PrintsDefaultWhenUnassigned(t *testing.T) {
	root, _ := configVault(t, testPropertyDefinitions)

	out, _, err := runConfig(t, "config", "get", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if out != config.DefaultAutosaveMinInterval+"\n" {
		t.Fatalf("config get printed %q, want %q", out, config.DefaultAutosaveMinInterval+"\n")
	}
	assertNoMutationArtifacts(t, root)
}

// The stored spelling is not the printed one: `get` prints the policy, so a
// script comparing its output against a canonical value never has to know how
// the file happens to be written.
func TestConfigGet_NormalizesTheStoredSpelling(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{`"300s"`, "5m"},
		{`"5m0s"`, "5m"},
		{`"24h0m0s"`, "24h"},
		{`"90s"`, "1m30s"},
		{`"off"`, "off"},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			configVault(t, "[autosave]\nmin_interval = "+tc.stored+"\n")

			out, _, err := runConfig(t, "config", "get", "autosave.min_interval")
			if err != nil {
				t.Fatalf("config get: %v", err)
			}
			if out != tc.want+"\n" {
				t.Fatalf("config get printed %q, want %q", out, tc.want+"\n")
			}
		})
	}
}

func TestConfigCommands_RejectUnsupportedKeys(t *testing.T) {
	configVault(t, testPropertyDefinitions)

	for _, args := range [][]string{
		{"config", "get", "autosave.interval"},
		{"config", "get", "AUTOSAVE.MIN_INTERVAL"},
		{"config", "set", "serve.addr", "127.0.0.1:9000"},
		{"config", "unset", "frontmatter.properties.tags"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			_, _, err := runConfig(t, args...)
			if err == nil {
				t.Fatal("expected an unsupported-key error")
			}
			for _, want := range []string{"unsupported key", args[2], "the only scriptable key is autosave.min_interval"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestConfigCommands_RejectWrongArity(t *testing.T) {
	configVault(t, testPropertyDefinitions)

	for _, tc := range []struct {
		name  string
		args  []string
		usage string
	}{
		{"get without a key", []string{"config", "get"}, "usage: bn config get autosave.min_interval"},
		{"get with two keys", []string{"config", "get", "autosave.min_interval", "extra"}, "usage: bn config get autosave.min_interval"},
		{"set without a value", []string{"config", "set", "autosave.min_interval"}, "usage: bn config set autosave.min_interval 5m"},
		{"set with a spare word", []string{"config", "set", "autosave.min_interval", "1m", "extra"}, "usage: bn config set autosave.min_interval 5m"},
		{"unset without a key", []string{"config", "unset"}, "usage: bn config unset autosave.min_interval"},
		{"unset with two keys", []string{"config", "unset", "autosave.min_interval", "extra"}, "usage: bn config unset autosave.min_interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runConfig(t, tc.args...)
			if err == nil {
				t.Fatal("expected an arity error")
			}
			if !strings.Contains(err.Error(), tc.usage) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.usage)
			}
		})
	}
}

func TestConfigSet_WritesTheCanonicalValueAndNothingElse(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s")
	if err != nil {
		t.Fatalf("config set: %v", err)
	}
	if out != "" {
		t.Fatalf("config set printed %q, want nothing on success", out)
	}

	got := readConfigFile(t, cfgPath)
	want := strings.Replace(commentedAutosaveConfig,
		`min_interval = "5m" # slow on purpose`,
		`min_interval = "10m" # slow on purpose`, 1)
	if got != want {
		t.Fatalf("brainnotes.toml =\n%s\nwant\n%s", got, want)
	}

	// The write is a journaled mutation, not a bare file edit.
	assertJournalEntries(t, root, 1)
}

// Setting a value that names the stored policy is a success that records
// nothing: `bn config set ... 300s` over a stored `5m` is the same policy
// spelled differently, so there is no change to journal or commit.
func TestConfigSet_SemanticNoOpWritesAndRecordsNothing(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "set", "autosave.min_interval", "300s")
	if err != nil {
		t.Fatalf("config set: %v", err)
	}
	if out != "" {
		t.Fatalf("a no-op config set printed %q, want nothing", out)
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("a no-op config set rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// The no-op rule is drawn at the policy, not at the file: a stored spelling
// that is not the canonical one is left exactly as it is when the requested
// value names the same policy. `set` says what the vault's floor will be, and a
// vault storing `300s` is already on the floor `bn config set ... 5m` asks for,
// so normalizing the spelling would be a commit that changes nothing the
// watcher does.
func TestConfigSet_LeavesAnEquivalentStoredSpellingAlone(t *testing.T) {
	const stored = "[autosave]\nmin_interval = \"300s\"\n"

	for _, value := range []string{"5m", "300s", "5m0s"} {
		t.Run(value, func(t *testing.T) {
			root, cfgPath := configVault(t, stored)

			out, _, err := runConfig(t, "config", "set", "autosave.min_interval", value)
			if err != nil {
				t.Fatalf("config set: %v", err)
			}
			if out != "" {
				t.Fatalf("a no-op config set printed %q, want nothing", out)
			}
			if got := readConfigFile(t, cfgPath); got != stored {
				t.Fatalf("brainnotes.toml =\n%q\nwant the stored spelling left alone", got)
			}
			assertNoMutationArtifacts(t, root)
		})
	}
}

// autosaveEncodings are TOML spellings that all decode to the 5m policy. The
// loader accepts every one of them, so `bn config set ... 5m` has nothing to do
// against any of them — the no-op is judged on the value the file *means*, not
// on the bytes it uses to say it.
var autosaveEncodings = []struct{ name, stored string }{
	{"a basic string", "[autosave]\nmin_interval = \"300s\"\n"},
	{"a literal string", "[autosave]\nmin_interval = '5m'\n"},
	{"a triple-quoted basic string", "[autosave]\nmin_interval = \"\"\"5m\"\"\"\n"},
	{"a triple-quoted literal string", "[autosave]\nmin_interval = '''300s'''\n"},
	{"a triple-quoted string opened by a newline", "[autosave]\nmin_interval = \"\"\"\n5m0s\"\"\"\n"},
	{"a Unicode escape", "[autosave]\nmin_interval = \"5\\u006d\"\n"},
	{"an escape beside a comment", "[autosave]\nmin_interval = \"\\u0033\\u0030\\u0030s\" # quiet\n"},
}

// Every encoding above is one the config loader reads as the 5m floor, which is
// what makes rewriting it a commit that moves nothing. `get` answering `5m` for
// each is the premise; the no-op below is the consequence.
func TestConfigGet_ReadsEveryValidEncodingOfThePolicy(t *testing.T) {
	for _, tc := range autosaveEncodings {
		t.Run(tc.name, func(t *testing.T) {
			configVault(t, tc.stored)

			out, _, err := runConfig(t, "config", "get", "autosave.min_interval")
			if err != nil {
				t.Fatalf("config get: %v", err)
			}
			if out != config.DefaultAutosaveMinInterval+"\n" {
				t.Fatalf("config get printed %q, want %q", out, config.DefaultAutosaveMinInterval+"\n")
			}
		})
	}
}

// The finding this closes: a stored value the loader resolves to the requested
// policy was still rewritten when the patcher could not read its *spelling*, so
// a converging script produced a commit on every run against a vault that
// happened to quote its floor unusually. Apply and dry run both have nothing to
// do here, and neither leaves an artifact behind.
func TestConfigSet_LeavesEveryValidEncodingOfThePolicyAlone(t *testing.T) {
	for _, tc := range autosaveEncodings {
		for _, args := range [][]string{
			{"config", "set", "autosave.min_interval", config.DefaultAutosaveMinInterval},
			{"config", "set", "autosave.min_interval", "300s"},
			{"config", "set", "autosave.min_interval", config.DefaultAutosaveMinInterval, "--dry-run"},
		} {
			t.Run(tc.name+"/"+strings.Join(args[3:], " "), func(t *testing.T) {
				root, cfgPath := configVault(t, tc.stored)

				out, _, err := runConfig(t, args...)
				if err != nil {
					t.Fatalf("bn %s: %v", strings.Join(args, " "), err)
				}
				if out != "" {
					t.Fatalf("bn %s printed %q, want nothing", strings.Join(args, " "), out)
				}
				if got := readConfigFile(t, cfgPath); got != tc.stored {
					t.Fatalf("brainnotes.toml =\n%q\nwant the stored encoding left alone", got)
				}
				assertNoMutationArtifacts(t, root)
			})
		}
	}
}

// The other side of it: the policy moving is still a real mutation, and what
// lands is one canonical assignment even when the value it replaces spanned
// several lines.
func TestConfigSet_RewritesAMultiLineValueWhenThePolicyMoves(t *testing.T) {
	const stored = "[autosave]\nmin_interval = \"\"\"\n1h\"\"\" # quiet\n\n[capture]\ninbox_note = \"Inbox.md\"\n"
	root, cfgPath := configVault(t, stored)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	want := "[autosave]\nmin_interval = \"10m\" # quiet\n\n[capture]\ninbox_note = \"Inbox.md\"\n"
	if got := readConfigFile(t, cfgPath); got != want {
		t.Fatalf("brainnotes.toml =\n%q\nwant\n%q", got, want)
	}
	assertJournalEntries(t, root, 1)

	printed, _, err := runConfig(t, "config", "get", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if printed != "10m\n" {
		t.Fatalf("config get printed %q, want the new policy", printed)
	}
}

// The same file, and a value that does move the floor: the write happens, and
// what lands is the canonical spelling of the new policy rather than the
// non-canonical shape of the one it replaced.
func TestConfigSet_WritesCanonicallyOverANonCanonicalStoredSpelling(t *testing.T) {
	const stored = "[autosave]\nmin_interval = \"300s\"\n"
	root, cfgPath := configVault(t, stored)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if got := readConfigFile(t, cfgPath); got != "[autosave]\nmin_interval = \"10m\"\n" {
		t.Fatalf("brainnotes.toml =\n%q\nwant the canonical spelling of the new policy", got)
	}
	assertJournalEntries(t, root, 1)
}

// The absent-assignment half of the same rule, and the one a converging script
// hits: a vault with no assignment is already on the default, so asking for the
// default writes nothing at all — no assignment appears, and with `--dry-run`
// there is nothing to preview either.
func TestConfigSet_TheDefaultOverAnAbsentAssignmentRecordsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"config", "set", "autosave.min_interval", config.DefaultAutosaveMinInterval},
		{"config", "set", "autosave.min_interval", "300s"},
		{"config", "set", "autosave.min_interval", config.DefaultAutosaveMinInterval, "--dry-run"},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			root, cfgPath := configVault(t, testPropertyDefinitions)

			out, _, err := runConfig(t, args...)
			if err != nil {
				t.Fatalf("bn %s: %v", strings.Join(args, " "), err)
			}
			if out != "" {
				t.Fatalf("bn %s printed %q, want nothing", strings.Join(args, " "), out)
			}
			if got := readConfigFile(t, cfgPath); got != testPropertyDefinitions {
				t.Fatalf("the default over an absent assignment rewrote the file:\n%s", got)
			}
			assertNoMutationArtifacts(t, root)

			printed, _, err := runConfig(t, "config", "get", "autosave.min_interval")
			if err != nil {
				t.Fatalf("config get: %v", err)
			}
			if printed != config.DefaultAutosaveMinInterval+"\n" {
				t.Fatalf("config get printed %q, want the unchanged default %q", printed, config.DefaultAutosaveMinInterval)
			}
		})
	}
}

// Creating the assignment is the one case that adds lines rather than rewriting
// one, and the table it appends is the position `bn init`'s template already
// puts it in.
func TestConfigSet_CreatesTheTableWhenAbsent(t *testing.T) {
	_, cfgPath := configVault(t, testPropertyDefinitions)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "off"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	got := readConfigFile(t, cfgPath)
	if want := testPropertyDefinitions + "\n[autosave]\nmin_interval = \"off\"\n"; got != want {
		t.Fatalf("brainnotes.toml =\n%q\nwant\n%q", got, want)
	}

	out, _, err := runConfig(t, "config", "get", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if out != "off\n" {
		t.Fatalf("config get printed %q, want %q", out, "off\n")
	}
}

func TestConfigSet_DryRunPrintsThePatchDiffAndWritesNothing(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s", "--dry-run")
	if err != nil {
		t.Fatalf("config set --dry-run: %v", err)
	}
	want := "--- " + config.FileName + "\n" +
		"- 7: min_interval = \"5m\" # slow on purpose\n" +
		"+ 7: min_interval = \"10m\" # slow on purpose\n"
	if out != want {
		t.Fatalf("dry-run output =\n%q\nwant\n%q", out, want)
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("a dry run rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// A dry run of a change that is not a change prints nothing at all, so the
// "nothing to do" answer is the same whether or not --dry-run was passed — and
// it is the same for every spelling of the policy already in force, because the
// preview is the patcher's own and the patcher judged the policy.
func TestConfigSet_DryRunOfANoOpPrintsNothing(t *testing.T) {
	for _, tc := range []struct{ name, stored, value string }{
		{"the canonical spelling", commentedAutosaveConfig, "5m0s"},
		{"a non-canonical stored spelling", "[autosave]\nmin_interval = \"300s\"\n", "5m"},
		{"no assignment at all", testPropertyDefinitions, "5m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cfgPath := configVault(t, tc.stored)

			out, _, err := runConfig(t, "config", "set", "autosave.min_interval", tc.value, "--dry-run")
			if err != nil {
				t.Fatalf("config set --dry-run: %v", err)
			}
			if out != "" {
				t.Fatalf("dry-run output = %q, want nothing", out)
			}
			if got := readConfigFile(t, cfgPath); got != tc.stored {
				t.Fatalf("a dry run rewrote the file:\n%s", got)
			}
			assertNoMutationArtifacts(t, root)
		})
	}
}

func TestConfigUnset_DryRunPrintsThePatchDiffAndWritesNothing(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "unset", "autosave.min_interval", "--dry-run")
	if err != nil {
		t.Fatalf("config unset --dry-run: %v", err)
	}
	want := "--- " + config.FileName + "\n" +
		"- 7: min_interval = \"5m\" # slow on purpose\n"
	if out != want {
		t.Fatalf("dry-run output =\n%q\nwant\n%q", out, want)
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("a dry run rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}
