package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// writeLocalOverlay puts body in the vault's brainnotes.local.toml and returns
// its path. The overlay is not a vault-root marker, so it needs no isolation
// check of its own: root is already a fixture markVaultRoot admitted.
func writeLocalOverlay(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, config.LocalFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.LocalFileName, err)
	}
	return path
}

// loadConfigFixture loads the vault the test fixtures point $BNOTES_VAULT at.
func loadConfigFixture(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// A write is routed by the ownership table, not by where the value currently
// comes from. That is the difference between a command that behaves the same on
// every machine and one whose destination depends on which files a particular
// vault happens to have.
func TestResolveConfigTarget_RoutesEveryKeyToItsOwningFile(t *testing.T) {
	root, _ := configVault(t, testPropertyDefinitions)
	cfg := loadConfigFixture(t)

	for _, tc := range []struct {
		key   string
		layer config.Layer
	}{
		{"autosave.min_interval", config.LayerPortable},
		{"capture.heading", config.LayerPortable},
		{"views", config.LayerPortable},
		{"open.editor", config.LayerLocal},
		{"serve.addr", config.LayerLocal},
		{"serve.open_browser", config.LayerLocal},
		{"fzf.args", config.LayerLocal},
		{"agent.command", config.LayerLocal},
		{"cockpit.agent_timeout_seconds", config.LayerLocal},
	} {
		t.Run(tc.key, func(t *testing.T) {
			target, err := resolveConfigTarget("config set", cfg, tc.key)
			if err != nil {
				t.Fatalf("resolveConfigTarget(%q): %v", tc.key, err)
			}
			if target.layer != tc.layer {
				t.Fatalf("%s routes to %q, want %q", tc.key, target.layer, tc.layer)
			}
			if want := filepath.Join(root, tc.layer.FileName()); target.path != want {
				t.Fatalf("%s writes %q, want %q", tc.key, target.path, want)
			}
		})
	}
}

// The overlay's absence is a state the transaction has to expect, not a detail
// to paper over: an overlay that appears between the read and the commit is
// another writer's file, and ExpectMissing is what makes that a refusal.
func TestResolveConfigTarget_ExpectsTheOverlayItRead(t *testing.T) {
	root, _ := configVault(t, testPropertyDefinitions)

	target, err := resolveConfigTarget("config set", loadConfigFixture(t), "open.editor")
	if err != nil {
		t.Fatalf("resolveConfigTarget: %v", err)
	}
	if got := target.expectation(); got != vault.ExpectMissing() {
		t.Fatalf("expectation for an absent overlay = %+v, want ExpectMissing", got)
	}

	const overlay = "[open]\neditor = \"vim\"\n"
	writeLocalOverlay(t, root, overlay)
	target, err = resolveConfigTarget("config set", loadConfigFixture(t), "open.editor")
	if err != nil {
		t.Fatalf("resolveConfigTarget: %v", err)
	}
	if got, want := target.expectation(), vault.ExpectHash(vault.HashBytes([]byte(overlay))); got != want {
		t.Fatalf("expectation for an existing overlay = %+v, want the overlay's own hash", got)
	}
	if string(target.source()) != overlay {
		t.Fatalf("source() = %q, want the overlay's bytes", target.source())
	}
}

// The portable file is the vault-root marker, so it is always on disk. Reading
// it through the same seam as the overlay is what keeps an absent overlay
// distinguishable from an unreadable one.
func TestReadConfigSources_DistinguishesAnAbsentOverlayFromAnEmptyOne(t *testing.T) {
	root, _ := configVault(t, testPropertyDefinitions)

	src, err := readConfigSources("config set", root)
	if err != nil {
		t.Fatalf("readConfigSources: %v", err)
	}
	if src.LocalPresent() {
		t.Fatal("an absent overlay was reported as present")
	}

	writeLocalOverlay(t, root, "")
	src, err = readConfigSources("config set", root)
	if err != nil {
		t.Fatalf("readConfigSources: %v", err)
	}
	if !src.LocalPresent() {
		t.Fatal("an empty overlay was reported as absent")
	}
	if len(src.Local) != 0 {
		t.Fatalf("empty overlay read as %q", src.Local)
	}
}

// The worst shape a configuration command can have is success with no effect.
// The overlay wins wherever both files assign a key, so a portable write for a
// key the overlay assigns would report success while the value in force stayed
// the overlay's.
func TestConfigSet_RefusesAWriteTheOverlayWouldShadow(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)
	const overlay = "[autosave]\nmin_interval = \"1h\"\n"
	overlayPath := writeLocalOverlay(t, root, overlay)

	_, _, err := runConfig(t, "config", "set", "autosave.min_interval", "10m")
	if err == nil {
		t.Fatal("a shadowed write was accepted")
	}
	for _, want := range []string{
		config.LocalFileName + " assigns autosave.min_interval",
		"the overlay wins",
		"would not change what this vault does",
		"bn config migrate",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("a refused write still rewrote %s:\n%s", config.FileName, got)
	}
	if got := readConfigFile(t, overlayPath); got != overlay {
		t.Fatalf("a refused write rewrote the overlay:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// The mirror of the refusal above: the overlay only shadows a key it actually
// assigns, so a portable write for every other key goes through with an overlay
// present.
func TestConfigSet_WritesThePortableFileWhenTheOverlayDoesNotAssignTheKey(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)
	const overlay = "[open]\neditor = \"vim\"\n"
	overlayPath := writeLocalOverlay(t, root, overlay)

	if _, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	want := strings.Replace(commentedAutosaveConfig,
		`min_interval = "5m" # slow on purpose`,
		`min_interval = "10m" # slow on purpose`, 1)
	if got := readConfigFile(t, cfgPath); got != want {
		t.Fatalf("%s =\n%s\nwant\n%s", config.FileName, got, want)
	}
	if got := readConfigFile(t, overlayPath); got != overlay {
		t.Fatalf("a portable write touched the overlay:\n%s", got)
	}
	assertJournalEntries(t, root, 1)
}

// A candidate is validated against the pair, not against the file being
// written: what the vault loads is never one file on its own. Here the overlay
// is the half that does not load, so a patched portable file that is perfectly
// valid on its own must still be refused.
func TestConfigSet_RefusesACandidateTheOtherLayerBreaks(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)
	overlayPath := writeLocalOverlay(t, root, "[open]\neditor = \"vim\"\n")
	// The overlay is read after the load, so a file that changed in between is
	// the only way to reach this — which is what the read seam stands in for.
	stubConfigSource(t, func(path string) ([]byte, error) {
		if path == overlayPath {
			return []byte("[not_a_real_section]\nkey = 1\n"), nil
		}
		return os.ReadFile(path)
	})

	_, _, err := runConfig(t, "config", "set", "autosave.min_interval", "600s")
	if err == nil {
		t.Fatal("a candidate the overlay breaks was written")
	}
	for _, want := range []string{"would not load", "so it was not written", "unknown keys"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("the refused candidate still reached disk:\n%s", got)
	}
}
