package cli

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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
