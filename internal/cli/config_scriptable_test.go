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

// The boundary of the no-op rule, and the reason it is drawn at the file rather
// than at the policy: a stored spelling that is not the canonical one is
// rewritten even when the requested value names the same policy. `set` says what
// the file will read afterwards, so leaving `300s` in place after
// `bn config set ... 5m` would be the command not doing what it was told. Run it
// twice and the second call is the no-op above.
func TestConfigSet_NormalizesANonCanonicalStoredSpelling(t *testing.T) {
	const stored = "[autosave]\nmin_interval = \"300s\"\n"
	root, cfgPath := configVault(t, stored)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "5m"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if got := readConfigFile(t, cfgPath); got != "[autosave]\nmin_interval = \"5m\"\n" {
		t.Fatalf("brainnotes.toml =\n%q\nwant the canonical spelling", got)
	}
	assertJournalEntries(t, root, 1)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "300s"); err != nil {
		t.Fatalf("second config set: %v", err)
	}
	assertJournalEntries(t, root, 1)
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
// "nothing to do" answer is the same whether or not --dry-run was passed.
func TestConfigSet_DryRunOfANoOpPrintsNothing(t *testing.T) {
	root, _ := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "set", "autosave.min_interval", "5m0s", "--dry-run")
	if err != nil {
		t.Fatalf("config set --dry-run: %v", err)
	}
	if out != "" {
		t.Fatalf("dry-run output = %q, want nothing", out)
	}
	assertNoMutationArtifacts(t, root)
}

func TestConfigSet_RejectsValuesTheVaultCannotHold(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"10s", "must be between 30s and 24h inclusive"},
		{"48h", "must be between 30s and 24h inclusive"},
		{"Off", `must be spelled "off" in lower case`},
		{"soon", `must be "off" or a Go duration`},
		{"", "requires a value"},
		{"   ", "requires a value"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			root, cfgPath := configVault(t, commentedAutosaveConfig)

			_, _, err := runConfig(t, "config", "set", "autosave.min_interval", tc.value)
			if err == nil {
				t.Fatalf("config set %q was accepted", tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
			if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
				t.Fatalf("a rejected value still rewrote the file:\n%s", got)
			}
			assertNoMutationArtifacts(t, root)
		})
	}
}

func TestConfigUnset_RemovesTheAssignmentAndRestoresTheDefault(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "unset", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config unset: %v", err)
	}
	if out != "" {
		t.Fatalf("config unset printed %q, want nothing on success", out)
	}

	got := readConfigFile(t, cfgPath)
	want := strings.Replace(commentedAutosaveConfig, "min_interval = \"5m\" # slow on purpose\n", "", 1)
	if got != want {
		t.Fatalf("brainnotes.toml =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(got, "# The watcher may not commit more often than this.") {
		t.Fatalf("unset removed the comment documenting the key:\n%s", got)
	}
	assertJournalEntries(t, root, 1)

	printed, _, err := runConfig(t, "config", "get", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if printed != config.DefaultAutosaveMinInterval+"\n" {
		t.Fatalf("config get printed %q, want the restored default %q", printed, config.DefaultAutosaveMinInterval)
	}
}

func TestConfigUnset_AbsentAssignmentWritesAndRecordsNothing(t *testing.T) {
	root, cfgPath := configVault(t, testPropertyDefinitions)

	out, _, err := runConfig(t, "config", "unset", "autosave.min_interval")
	if err != nil {
		t.Fatalf("config unset: %v", err)
	}
	if out != "" {
		t.Fatalf("a no-op config unset printed %q, want nothing", out)
	}
	if got := readConfigFile(t, cfgPath); got != testPropertyDefinitions {
		t.Fatalf("a no-op config unset rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
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

// A root-level dotted key is the spelling that would produce a duplicate if the
// patcher appended a table instead of finding it, so the round trip through both
// commands is worth pinning end to end.
func TestConfigSetUnset_HandleARootLevelDottedKey(t *testing.T) {
	const dotted = "autosave.min_interval = \"1m\"\n\n[frontmatter.properties.tags]\ntype  = \"list\"\nitems = \"string\"\n"
	_, cfgPath := configVault(t, dotted)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "120s"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	got := readConfigFile(t, cfgPath)
	if want := strings.Replace(dotted, `autosave.min_interval = "1m"`, `autosave.min_interval = "2m"`, 1); got != want {
		t.Fatalf("brainnotes.toml =\n%q\nwant\n%q", got, want)
	}

	if _, _, err := runConfig(t, "config", "unset", "autosave.min_interval"); err != nil {
		t.Fatalf("config unset: %v", err)
	}
	if got := readConfigFile(t, cfgPath); strings.Contains(got, "min_interval") {
		t.Fatalf("unset left the dotted key behind:\n%s", got)
	}
}

// A candidate the vault could not start from is refused while it is still bytes
// in memory. The patcher validates the one line it writes, so reaching this at
// all takes a source that changed under the command — which is what the read
// seam stands in for.
func TestConfigSet_RefusesACandidateThatWouldNotLoad(t *testing.T) {
	root, cfgPath := configVault(t, testPropertyDefinitions)
	stubConfigSource(t, func(string) ([]byte, error) {
		return []byte("[not_a_real_section]\nkey = 1\n"), nil
	})

	_, _, err := runConfig(t, "config", "set", "autosave.min_interval", "1m")
	if err == nil {
		t.Fatal("a candidate that cannot be decoded was written")
	}
	for _, want := range []string{"would not load", "so it was not written", "unknown keys"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != testPropertyDefinitions {
		t.Fatalf("the refused candidate still reached disk:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// The expected hash is the whole reason the source is read rather than
// re-rendered from the loaded Config: another writer between the read and the
// commit has to become a refusal, never a silent overwrite of their edit.
func TestConfigSet_RefusesWhenTheFileChangedUnderIt(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)
	const concurrent = "[autosave]\nmin_interval = \"45s\"\n"
	stubBeforeConfigCommit(t, func() {
		if err := os.WriteFile(cfgPath, []byte(concurrent), 0o644); err != nil {
			t.Fatalf("simulate a concurrent writer: %v", err)
		}
	})

	_, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s")
	if err == nil {
		t.Fatal("a stale patch was committed over a concurrent edit")
	}
	for _, want := range []string{"changed on disk", "nothing was written", "re-run it"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != concurrent {
		t.Fatalf("the other writer's file was overwritten:\n%s", got)
	}
	// The refusal happens while staging, before anything is captured, so the
	// conflict leaves no undo entry to explain a change that never landed.
	if entries, err := os.ReadDir(filepath.Join(root, ".bnotes", "journal")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused config set recorded %d journal entries", len(entries))
	}
}

// A configuration the loader rejects stops every one of these commands in
// PersistentPreRunE, exactly as it always has. Nothing here is a repair path:
// the file has to be edited, which is what `bn config edit` is for, and the two
// report the same failure so neither looks like the way out of it.
func TestConfigCommands_ReportTheLoadFailureOnAnInvalidConfig(t *testing.T) {
	const broken = "[autosave]\nmin_interval = \"soon\"\n"
	root, cfgPath := configVault(t, broken)
	t.Setenv("EDITOR", "vim")
	stubExecReplace(t)

	const want = `config: autosave.min_interval "soon" must be "off" or a Go duration such as "5m"`
	for _, args := range [][]string{
		{"config", "get", "autosave.min_interval"},
		{"config", "set", "autosave.min_interval", "1m"},
		{"config", "unset", "autosave.min_interval"},
		{"config", "edit"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			_, _, err := runConfig(t, args...)
			if err == nil {
				t.Fatal("expected the loader's refusal")
			}
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
	if got := readConfigFile(t, cfgPath); got != broken {
		t.Fatalf("a command that could not load still rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// The `config` command's own output is the one thing these three children must
// not disturb: adding subcommands to a command that also runs on its own is
// exactly how a display command starts behaving like a group.
func TestConfigCmd_DisplayOutputIsUnchangedByTheNewChildren(t *testing.T) {
	root, cfgPath := configVault(t, testPropertyDefinitions)

	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	want := "Vault:       " + root + "\nConfig:      " + cfgPath + "\nResolved by: $BNOTES_VAULT\n"
	if out != want {
		t.Fatalf("config output =\n%q\nwant\n%q", out, want)
	}
	if _, _, err := runConfig(t, "config", "extra"); err == nil {
		t.Fatal("`bn config extra` was accepted")
	}
}

func TestConfigCmd_HelpListsTheScriptableChildren(t *testing.T) {
	configVault(t, testPropertyDefinitions)

	out, _, err := runConfig(t, "config", "--help")
	if err != nil {
		t.Fatalf("config --help: %v", err)
	}
	for _, want := range []string{"edit", "get", "set", "unset"} {
		if !strings.Contains(out, "\n  "+want+" ") {
			t.Fatalf("`bn config --help` does not list %q:\n%s", want, out)
		}
	}
}

// stubConfigSource replaces the read the patch is computed from.
func stubConfigSource(t *testing.T, read func(string) ([]byte, error)) {
	t.Helper()
	prev := readConfigSource
	readConfigSource = read
	t.Cleanup(func() { readConfigSource = prev })
}

// stubBeforeConfigCommit installs the hook that stands in for another writer
// landing between staging and commit.
func stubBeforeConfigCommit(t *testing.T, hook func()) {
	t.Helper()
	prev := beforeConfigCommit
	beforeConfigCommit = hook
	t.Cleanup(func() { beforeConfigCommit = prev })
}
