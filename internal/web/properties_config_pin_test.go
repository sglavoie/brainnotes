package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/properties"
)

// propertyTestConfigTOMLRewritten declares the same properties under a
// different allow-list. A note written under it after being validated against
// propertyTestConfigTOML would carry a value nothing ever checked.
const propertyTestConfigTOMLRewritten = `[frontmatter.properties.status]
type = "string"
allowed = ["draft", "wip"]

[frontmatter.properties.tags]
type = "list"
items = "string"

[frontmatter.properties.reviewed]
type = "date"
`

// rewriteConfigDuringCommit replaces brainnotes.toml from inside the commit
// window, which is the interleaving the configuration pin exists to catch: the
// writability check has already passed and the note bytes are already planned.
func rewriteConfigDuringCommit(t *testing.T) func(root string) {
	t.Helper()

	var once sync.Once
	return func(root string) {
		once.Do(func() {
			path := filepath.Join(root, config.FileName)
			if err := os.WriteFile(path, []byte(propertyTestConfigTOMLRewritten), 0o644); err != nil {
				t.Errorf("rewrite config: %v", err)
			}
		})
	}
}

// createOverlayDuringCommit writes brainnotes.local.toml from inside the same
// window. The overlay did not exist when the write was planned, so its
// appearance is the change only an expectation of *absence* can catch: it is the
// other half of the configuration the note bytes were validated against, and
// nothing has read what it says.
func createOverlayDuringCommit(t *testing.T) func(root string) {
	t.Helper()

	var once sync.Once
	return func(root string) {
		once.Do(func() {
			path := filepath.Join(root, config.LocalFileName)
			if err := os.WriteFile(path, []byte("[open]\neditor = \"hx\"\n"), 0o644); err != nil {
				t.Errorf("create overlay: %v", err)
			}
		})
	}
}

func TestPropertyValue_RejectsConfigChangeDuringCommit(t *testing.T) {
	ts, _, root := newPropertyHookServer(t, propertyTestConfigTOML, propertyTestNotes(), rewriteConfigDuringCommit(t))
	before := readVaultFile(t, root, "Alpha.md")

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if got := readVaultFile(t, root, "Alpha.md"); got != before {
		t.Fatalf("the note must not be written under the replaced configuration, got %q", got)
	}
}

func TestPropertiesApply_RejectsConfigChangeDuringCommit(t *testing.T) {
	ts, _, root := newPropertyHookServer(t, propertyTestConfigTOML, propertyTestNotes(), rewriteConfigDuringCommit(t))
	before := map[string]string{
		"Alpha.md": readVaultFile(t, root, "Alpha.md"),
		"Beta.md":  readVaultFile(t, root, "Beta.md"),
	}

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	for rel, want := range before {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("%s must not be written under the replaced configuration, got %q", rel, got)
		}
	}
}

func TestPropertyValue_RejectsOverlayAppearingDuringCommit(t *testing.T) {
	ts, _, root := newPropertyHookServer(t, propertyTestConfigTOML, propertyTestNotes(), createOverlayDuringCommit(t))
	before := readVaultFile(t, root, "Alpha.md")

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if got := readVaultFile(t, root, "Alpha.md"); got != before {
		t.Fatalf("the note must not be written under a configuration that gained a layer, got %q", got)
	}
}

func TestPropertiesApply_RejectsOverlayAppearingDuringCommit(t *testing.T) {
	ts, _, root := newPropertyHookServer(t, propertyTestConfigTOML, propertyTestNotes(), createOverlayDuringCommit(t))
	before := map[string]string{
		"Alpha.md": readVaultFile(t, root, "Alpha.md"),
		"Beta.md":  readVaultFile(t, root, "Beta.md"),
	}

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	for rel, want := range before {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("%s must not be written under a configuration that gained a layer, got %q", rel, got)
		}
	}
}
