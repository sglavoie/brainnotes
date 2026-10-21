package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// The status describes a pair of files, so it reports both. source_hash keeps
// naming the portable file alone — it is how a caller identifies *which vault* a
// server loaded — and composite_hash is the one value that answers "did the
// configuration change", because either layer changing changes it.
func TestConfigStatus_ReportsBothConfigurationLayers(t *testing.T) {
	ts, _, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	_, boot := getConfigStatus(t, ts.URL)
	assertOverlayAbsent(t, boot, vault.HashBytes([]byte(reloadableConfigTOML)))

	const overlay = "[open]\neditor = \"hx\"\n"
	writeLocalOverlay(t, root, overlay)

	_, after := getConfigStatus(t, ts.URL)
	if after.SourceHash != boot.SourceHash {
		t.Fatalf("the portable file was untouched, source_hash = %q want %q", after.SourceHash, boot.SourceHash)
	}
	if !after.LocalPresent {
		t.Fatal("local_present must report the overlay that appeared")
	}
	if want := vault.HashBytes([]byte(overlay)); after.LocalSourceHash != want {
		t.Fatalf("local_source_hash = %q, want %q", after.LocalSourceHash, want)
	}
	if after.CompositeHash == boot.CompositeHash {
		t.Fatal("an overlay that appeared must change composite_hash")
	}
	assertConfigGeneration(t, after, boot.Generation+1, "after an overlay appeared")
}

// An overlay edit with brainnotes.toml untouched is a configuration change, and
// it has to invalidate everything a portable edit does. The generation is what
// every preview receipt and cached plan is judged against, so it is the field
// asserted at each step.
func TestConfigStatus_OverlayEditAloneRepublishes(t *testing.T) {
	ts, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	writeLocalOverlay(t, root, "[open]\neditor = \"hx\"\n")
	_, first := getConfigStatus(t, ts.URL)
	assertServedEditor(t, srv, "hx")

	writeLocalOverlay(t, root, "[open]\neditor = \"nvim\"\n")
	_, second := getConfigStatus(t, ts.URL)
	assertConfigGeneration(t, second, first.Generation+1, "after an overlay edit")
	if second.SourceHash != first.SourceHash {
		t.Fatal("an overlay edit must not appear to change the portable file")
	}
	assertServedEditor(t, srv, "nvim")

	// An unchanged pair republishes nothing, the same way an unchanged portable
	// file does not.
	_, again := getConfigStatus(t, ts.URL)
	assertConfigGeneration(t, again, second.Generation, "with the pair unchanged")

	removeLocalOverlay(t, root)
	_, third := getConfigStatus(t, ts.URL)
	assertConfigGeneration(t, third, second.Generation+1, "after the overlay was removed")
	assertOverlayAbsent(t, third, first.SourceHash)
	// The portable file assigns no editor, so removing the overlay takes the key
	// out of force entirely rather than uncovering a value underneath it.
	assertServedEditor(t, srv, "")

	// Present-and-empty is not absent: the file exists and could gain a key at
	// any moment, so it is a state of its own all the way into the composite.
	writeLocalOverlay(t, root, "")
	_, fourth := getConfigStatus(t, ts.URL)
	assertConfigGeneration(t, fourth, third.Generation+1, "after an empty overlay appeared")
	if !fourth.LocalPresent {
		t.Fatal("an empty overlay is still present")
	}
	if fourth.CompositeHash == third.CompositeHash {
		t.Fatal("an empty overlay must not hash the same as no overlay at all")
	}
}

// The invalid-configuration contract is about the pair, not about one file: an
// overlay the loader refuses leaves the last valid configuration published and
// property writes disabled, and repairing it recovers without a restart.
func TestConfigStatus_InvalidOverlayKeepsLastValidConfigAndDisablesPropertyWrites(t *testing.T) {
	ts, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})
	_, boot := getConfigStatus(t, ts.URL)

	writeLocalOverlay(t, root, "[open\neditor = \"hx\"\n")

	_, payload := getConfigStatus(t, ts.URL)
	if payload.Status != ReloadInvalid {
		t.Fatalf("an unparseable overlay is an invalid configuration: %#v", payload)
	}
	if payload.Writable {
		t.Fatal("an invalid configuration must disable property writes")
	}
	if payload.Error == "" {
		t.Fatal("invalid status should carry the validation error")
	}
	assertConfigGeneration(t, payload, boot.Generation, "with an invalid overlay")
	if def, ok := srv.config().Frontmatter.Properties["status"]; !ok || def.Type != config.KindString {
		t.Fatalf("the last valid configuration should stay published: %#v", srv.config().Frontmatter.Properties)
	}

	assertPropertyWriteRefused(t, ts.URL, root)

	removeLocalOverlay(t, root)
	_, fixed := getConfigStatus(t, ts.URL)
	if fixed.Status != ReloadOK || !fixed.Writable {
		t.Fatalf("removing the bad overlay should recover: %#v", fixed)
	}
	assertConfigGeneration(t, fixed, boot.Generation+1, "after the repair")
	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("property write after repair: got %d, want 200; body=%q", resp.StatusCode, body)
	}
}

// assertPropertyWriteRefused asserts the whole refusal, not just its status: a
// write that is turned away has to say why and has to leave the note alone, and
// a 409 that still edited the file would be the worse of the two failures.
func assertPropertyWriteRefused(t *testing.T, baseURL, root string) {
	t.Helper()
	resp := postFrontmatterSetRequest(t, baseURL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("property write status = %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	if !strings.Contains(body, "property writes are disabled") {
		t.Fatalf("refusal should explain itself, got %q", body)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note must be untouched, got:\n%s", got)
	}
}

func TestConfigManager_ConcurrentRefreshAndSnapshot(t *testing.T) {
	_, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})

	var wg sync.WaitGroup
	const readers = 8
	wg.Add(readers + 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")
			if err := srv.reloadConfig(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
			rewriteConfig(t, root, reloadableConfigTOML)
			if err := srv.reloadConfig(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
		}
	}()
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				cfg := srv.config()
				if cfg == nil {
					t.Error("snapshot must never be nil")
					return
				}
				_ = cfg.Frontmatter.Properties["status"]
				_ = srv.configState.Status()
			}
		}()
	}
	wg.Wait()

	if srv.configState.Status().Status != ReloadOK {
		t.Fatalf("final status = %#v", srv.configState.Status())
	}
}

func TestConfigManager_UndoOfConfigFileRepublishes(t *testing.T) {
	_, srv, root := newReloadableServer(t, reloadableConfigTOML, Options{CSRFToken: "csrf-token"})
	boot := srv.configState.Status()

	rewriteConfig(t, root, reloadableConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")
	if err := srv.reloadConfig(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := srv.config().Frontmatter.Properties["area"]; !ok {
		t.Fatal("edit should be published")
	}

	// Simulate undo restoring the previous brainnotes.toml bytes.
	rewriteConfig(t, root, reloadableConfigTOML)
	if err := srv.reloadConfig(); err != nil {
		t.Fatalf("reload after restore: %v", err)
	}
	restored := srv.configState.Status()
	if restored.SourceHash != boot.SourceHash {
		t.Fatalf("restored source hash = %q, want %q", restored.SourceHash, boot.SourceHash)
	}
	if _, ok := srv.config().Frontmatter.Properties["area"]; ok {
		t.Fatal("restored config should no longer declare the reverted property")
	}
	if restored.Generation <= boot.Generation {
		t.Fatalf("each republication bumps the generation, got %d after %d", restored.Generation, boot.Generation)
	}
}
