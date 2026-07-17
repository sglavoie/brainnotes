package web

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// newBlockingLoader returns a noteLoader that blocks indefinitely on reads of
// any path in blocked (until t.Cleanup releases the goroutines) and loads every
// other note normally. callCount reports how many times each path's load was
// attempted, so tests can prove a quarantined path is never retried.
func newBlockingLoader(t *testing.T, blocked ...string) (load noteLoader, callCount func(string) int) {
	t.Helper()

	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })

	blockedSet := make(map[string]struct{}, len(blocked))
	for _, p := range blocked {
		blockedSet[p] = struct{}{}
	}

	var mu sync.Mutex
	calls := map[string]int{}

	load = func(n *vault.Note) error {
		mu.Lock()
		calls[n.Path]++
		mu.Unlock()
		if _, ok := blockedSet[n.Path]; ok {
			<-gate // block until test cleanup releases this goroutine
			return nil
		}
		return n.Load()
	}
	callCount = func(p string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[p]
	}
	return load, callCount
}

func TestVaultState_PartialSnapshotWhenLoaderBlocks(t *testing.T) {
	root := t.TempDir()
	healthyPath := filepath.Join(root, "Healthy.md")
	blockedPath := filepath.Join(root, "Blocked.md")
	if err := os.WriteFile(healthyPath, []byte("---\ntitle: Healthy\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write healthy: %v", err)
	}
	if err := os.WriteFile(blockedPath, []byte("---\ntitle: Blocked\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write blocked: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }
	load, callCount := newBlockingLoader(t, blockedPath)
	state.load = load
	state.loadTimeout = 50 * time.Millisecond

	snap, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("currentSnapshot: %v", err)
	}

	if len(snap.notes) != 1 {
		t.Fatalf("healthy notes = %d, want 1; snapshot=%#v", len(snap.notes), snap)
	}
	if title := snap.notes[0].Title(); title != "Healthy" {
		t.Fatalf("surviving note = %q, want Healthy", title)
	}
	if len(snap.skipped) != 1 || snap.skipped[0].Path != "Blocked.md" {
		t.Fatalf("skipped notes = %#v, want Blocked.md", snap.skipped)
	}
	if len(snap.parseErrors) != 0 {
		t.Fatalf("timeout must not be recorded as a parse error: %#v", snap.parseErrors)
	}
	if _, ok := state.quarantined[blockedPath]; !ok {
		t.Fatalf("timed-out path not quarantined: %#v", state.quarantined)
	}
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("blocked loader called %d times, want 1", got)
	}
}

func TestVaultState_QuarantineSkipsWithoutRetry(t *testing.T) {
	root := t.TempDir()
	healthyPath := filepath.Join(root, "Healthy.md")
	blockedPath := filepath.Join(root, "Blocked.md")
	if err := os.WriteFile(healthyPath, []byte("---\ntitle: Healthy\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write healthy: %v", err)
	}
	if err := os.WriteFile(blockedPath, []byte("---\ntitle: Blocked\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write blocked: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }
	load, callCount := newBlockingLoader(t, blockedPath)
	state.load = load
	state.loadTimeout = 50 * time.Millisecond

	if _, err := state.currentSnapshot(); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("first refresh loader calls = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	refreshed, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("refreshed snapshot: %v", err)
	}
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("quarantined path retried: loader calls = %d, want 1", got)
	}
	if len(refreshed.notes) != 1 || refreshed.notes[0].Title() != "Healthy" {
		t.Fatalf("refreshed notes = %#v, want only Healthy", refreshed.notes)
	}
	if len(refreshed.skipped) != 1 || refreshed.skipped[0].Path != "Blocked.md" {
		t.Fatalf("refreshed skipped = %#v, want Blocked.md", refreshed.skipped)
	}
}

func TestVaultState_QuarantinePreservedAcrossInvalidate(t *testing.T) {
	root := t.TempDir()
	healthyPath := filepath.Join(root, "Healthy.md")
	blockedPath := filepath.Join(root, "Blocked.md")
	if err := os.WriteFile(healthyPath, []byte("---\ntitle: Healthy\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write healthy: %v", err)
	}
	if err := os.WriteFile(blockedPath, []byte("---\ntitle: Blocked\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write blocked: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Hour)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }
	load, callCount := newBlockingLoader(t, blockedPath)
	state.load = load
	state.loadTimeout = 50 * time.Millisecond

	if _, err := state.currentSnapshot(); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	state.invalidate()

	if _, ok := state.quarantined[blockedPath]; !ok {
		t.Fatalf("invalidate cleared the quarantine: %#v", state.quarantined)
	}

	refreshed, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("refreshed snapshot: %v", err)
	}
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("quarantined path re-read after invalidate: loader calls = %d, want 1", got)
	}
	if len(refreshed.notes) != 1 || refreshed.notes[0].Title() != "Healthy" {
		t.Fatalf("refreshed notes = %#v, want only Healthy", refreshed.notes)
	}
}

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

func TestVaultState_InvalidateRefreshesBeforeTTL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v1\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Hour)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}

	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }

	if _, err := state.currentSnapshot(); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}

	state.invalidate()

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
