package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigExplainSources(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	portable := useVaultRoot(t, root, "[serve]\naddr = '127.0.0.1:8090'\n[open]\neditor = 'vim'\n")
	overlay := filepath.Join(root, "brainnotes.local.toml")
	if err := os.WriteFile(overlay, []byte("[open]\neditor = 'nvim'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value, source string }{
		{"open.editor", "nvim", overlay},
		{"serve.addr", "127.0.0.1:8090", portable},
		{"autosave.min_interval", "5m", "built-in default"},
		{"fzf.args", "value = []", "built-in default"},
		{"new.routes", "value = {}", "built-in default"},
	} {
		out, _, err := runConfig(t, "config", "explain", tc.key)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{tc.key, tc.value, "Source:      " + tc.source} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
	}
}

func TestConfigSummaryOverlay(t *testing.T) {
	resetConfigEnv(t)
	root := t.TempDir()
	useVaultRoot(t, root, "")
	overlay := filepath.Join(root, "brainnotes.local.toml")
	if err := os.WriteFile(overlay, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runConfig(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Overlay:     "+overlay) {
		t.Fatalf("overlay missing: %s", out)
	}
	if err := os.Remove(overlay); err != nil {
		t.Fatal(err)
	}
	out, _, err = runConfig(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Overlay:     none") {
		t.Fatalf("absence missing: %s", out)
	}
}
