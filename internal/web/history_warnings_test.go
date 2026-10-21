package web

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/properties"
)

// One corrupt manifest used to abort the whole listing, which took every other
// undo record off the panel with it. Now the panel renders the valid entries and
// reports the damaged one beside them.
func TestHistoryAPI_ReportsCorruptEntryAsWarning(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionReplaceValue,
		Property: "status",
		Notes:    []string{"Alpha.md"},
		Match:    &properties.TypedValue{Kind: config.KindString, Text: "draft"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	status, applied := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("apply recorded no journal entry")
	}

	journalRoot := noteops.JournalRoot(root)
	broken := filepath.Join(journalRoot, "entry-broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatalf("mkdir broken entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(broken, "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) != 1 || history.Entries[0].ID != applied.JournalID {
		t.Fatalf("entries = %+v, want just the applied entry %q", history.Entries, applied.JournalID)
	}
	if history.WarningCount != 1 {
		t.Fatalf("warning_count = %d, want 1", history.WarningCount)
	}
	if len(history.Warnings) != 1 || history.Warnings[0].ID != "entry-broken" {
		t.Fatalf("warnings = %+v, want the broken entry named", history.Warnings)
	}

	// The undoable entry stays undoable: unrelated corruption disables nothing.
	code, body := postProperty(t, ts.URL, "/api/undo", propertyTestToken, map[string]string{"id": applied.JournalID})
	if code != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", code, body)
	}
}

// A request to undo the corrupt entry itself is still a direct error. The caller
// named this entry, so silently doing nothing — or undoing a different one —
// would be worse than failing.
func TestUndoAPI_CorruptEntryIsAnError(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	journalRoot := noteops.JournalRoot(root)
	broken := filepath.Join(journalRoot, "entry-broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatalf("mkdir broken entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(broken, "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	code, body := postProperty(t, ts.URL, "/api/undo", propertyTestToken, map[string]string{"id": "entry-broken"})
	if code == http.StatusOK {
		t.Fatalf("undo of a corrupt entry succeeded; body %q", body)
	}
}
