package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVaultState_RefreshesAfterTTL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v1\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}

	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }

	first, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	firstBody, err := first.notes[0].Body()
	if err != nil {
		t.Fatalf("first body: %v", err)
	}
	if firstBody != "Body v1\n" {
		t.Fatalf("first body = %q, want %q", firstBody, "Body v1\n")
	}

	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}

	cached, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("cached snapshot: %v", err)
	}
	cachedBody, err := cached.notes[0].Body()
	if err != nil {
		t.Fatalf("cached body: %v", err)
	}
	if cachedBody != "Body v1\n" {
		t.Fatalf("cached body = %q, want %q", cachedBody, "Body v1\n")
	}

	now = now.Add(2 * time.Second)
	refreshed, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("refreshed snapshot: %v", err)
	}
	refreshedBody, err := refreshed.notes[0].Body()
	if err != nil {
		t.Fatalf("refreshed body: %v", err)
	}
	if refreshedBody != "Body v2\n" {
		t.Fatalf("refreshed body = %q, want %q", refreshedBody, "Body v2\n")
	}
}
