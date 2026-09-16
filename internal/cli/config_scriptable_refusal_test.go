package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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

// The same race on the layer the write does *not* touch. Whether this patch is
// even worth writing was decided by reading the pair — an overlay assigning
// autosave.min_interval makes the portable write a no-op the user would be told
// succeeded — so an overlay that appeared under the command has to be a refusal
// too. Only an expectation of absence can catch it.
func TestConfigSet_RefusesWhenAnOverlayAppearedUnderIt(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)
	localPath := filepath.Join(root, config.LocalFileName)
	const concurrent = "[autosave]\nmin_interval = \"45s\"\n"
	stubBeforeConfigCommit(t, func() {
		if err := os.WriteFile(localPath, []byte(concurrent), 0o644); err != nil {
			t.Fatalf("simulate a concurrent writer: %v", err)
		}
	})

	_, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s")
	if err == nil {
		t.Fatal("a patch was committed under an overlay nothing had read")
	}
	for _, want := range []string{"changed on disk", "nothing was written", "re-run it"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("the portable file was written anyway:\n%s", got)
	}
	if got := readConfigFile(t, localPath); got != concurrent {
		t.Fatalf("the other writer's overlay was disturbed:\n%s", got)
	}
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
