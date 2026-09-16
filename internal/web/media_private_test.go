package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestPrivateMediaPath_CoversBothConfigurationLayers(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{config.FileName, true},
		{config.LocalFileName, true},
		{"BrainNotes.Local.TOML", true},
		{"Attachments/" + config.LocalFileName, true},
		{".bnotes/sync/enrollment.json", true},
		{"Attachments/image.png", false},
		{"brainnotes.local.md", false},
		{"notes/brainnotes-local-toml.png", false},
	}
	for _, tc := range cases {
		if got := privateMediaPath(tc.rel); got != tc.want {
			t.Errorf("privateMediaPath(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// TestResolveContainedRegularFile_RefusesAliasesOfTheLocalConfig covers the
// half a lexical check cannot: a link whose own name is innocent and whose
// target is the machine-local configuration. Both layers are checked before and
// after symlink resolution, so neither spelling of the request reaches the file.
func TestResolveContainedRegularFile_RefusesAliasesOfTheLocalConfig(t *testing.T) {
	root := aliasedConfigVault(t)

	for _, rel := range []string{config.LocalFileName, "alias.png"} {
		resolved, ok, err := resolveContainedRegularFile(root, rel)
		if err != nil {
			t.Fatalf("resolveContainedRegularFile(%q): %v", rel, err)
		}
		if ok || resolved != "" {
			t.Errorf("%q resolved to %q; the machine-local configuration is never an attachment", rel, resolved)
		}
	}

	// The guard must not swallow ordinary attachments, including aliased ones.
	for _, rel := range []string{"real.png", "ok.png"} {
		if _, ok, err := resolveContainedRegularFile(root, rel); err != nil || !ok {
			t.Errorf("resolveContainedRegularFile(%q) = %v, %v; want a served attachment", rel, ok, err)
		}
	}
}

// aliasedConfigVault builds a vault root holding both configuration layers, one
// ordinary attachment, a symlink named like an image whose target is the
// machine-local configuration, and a symlink to the real attachment — the pair
// that separates "refuses the alias" from "refuses every symlink".
func aliasedConfigVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{config.FileName, config.LocalFileName} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("[serve]\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "real.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, config.LocalFileName), filepath.Join(root, "alias.png")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real.png"), filepath.Join(root, "ok.png")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return root
}
