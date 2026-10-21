package web

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
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

func TestVaultState_SerialHoldsAcrossUnchangedRebuilds(t *testing.T) {
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

	// N rebuilds over an unchanged vault: the walk reruns once per TTL (loadedAt
	// advances) but the serial that keys the derived caches must not move.
	for i := 0; i < 5; i++ {
		now = now.Add(2 * time.Second)
		rebuilt, err := state.currentSnapshot()
		if err != nil {
			t.Fatalf("rebuild %d: %v", i, err)
		}
		if rebuilt.serial != first.serial {
			t.Fatalf("rebuild %d serial = %d, want %d (unchanged vault must not bump serial)", i, rebuilt.serial, first.serial)
		}
		if !rebuilt.loadedAt.Equal(now) {
			t.Fatalf("rebuild %d loadedAt = %s, want %s (TTL throttle must still refresh)", i, rebuilt.loadedAt, now)
		}
	}

	// A single edit bumps the serial exactly once, no matter how many rebuilds
	// observe it afterwards.
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}
	now = now.Add(2 * time.Second)
	edited, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("edited snapshot: %v", err)
	}
	if edited.serial != first.serial+1 {
		t.Fatalf("edited serial = %d, want %d", edited.serial, first.serial+1)
	}

	for i := 0; i < 3; i++ {
		now = now.Add(2 * time.Second)
		settled, err := state.currentSnapshot()
		if err != nil {
			t.Fatalf("post-edit rebuild %d: %v", i, err)
		}
		if settled.serial != edited.serial {
			t.Fatalf("post-edit rebuild %d serial = %d, want %d (one edit must bump exactly once)", i, settled.serial, edited.serial)
		}
	}
}

func TestVaultState_SerialHoldsAcrossRefreshSnapshot(t *testing.T) {
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

	first, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// refreshSnapshot invalidates and re-stats everything; when nothing changed on
	// disk the content-addressed cockpit flows want the same serial back.
	for i := 0; i < 3; i++ {
		refreshed, err := state.refreshSnapshot()
		if err != nil {
			t.Fatalf("refreshSnapshot %d: %v", i, err)
		}
		if refreshed.serial != first.serial {
			t.Fatalf("refreshSnapshot %d serial = %d, want %d", i, refreshed.serial, first.serial)
		}
	}
	if _, ok := state.quarantined[notePath]; ok {
		t.Fatalf("healthy note quarantined: %#v", state.quarantined)
	}

	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nBody v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}
	edited, err := state.refreshSnapshot()
	if err != nil {
		t.Fatalf("edited refreshSnapshot: %v", err)
	}
	if edited.serial != first.serial+1 {
		t.Fatalf("edited serial = %d, want %d", edited.serial, first.serial+1)
	}
}

func TestVaultState_DerivedCachesSurviveUnchangedRebuilds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("---\nstatus: active\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	now := time.Unix(0, 0)
	state.now = func() time.Time { return now }

	fm := &config.FrontmatterConfig{}
	first, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	firstIndex := state.propertyIndexFor(first, fm, 1)
	firstCorpus, err := state.relatedCorpusFor(first, nil, 1)
	if err != nil {
		t.Fatalf("first corpus: %v", err)
	}

	now = now.Add(2 * time.Second)
	rebuilt, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("rebuilt snapshot: %v", err)
	}
	if state.propertyIndexFor(rebuilt, fm, 1) != firstIndex {
		t.Fatalf("property index rebuilt for an unchanged vault")
	}
	rebuiltCorpus, err := state.relatedCorpusFor(rebuilt, nil, 1)
	if err != nil {
		t.Fatalf("rebuilt corpus: %v", err)
	}
	if rebuiltCorpus != firstCorpus {
		t.Fatalf("related corpus rebuilt for an unchanged vault")
	}
}

func TestVaultState_ReloadConfigGenerationStillForcesDerivedRebuild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("---\narea: \"[[Home]]\"\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Hour)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}

	snapshot, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("currentSnapshot: %v", err)
	}
	first := state.propertyIndexFor(snapshot, &config.FrontmatterConfig{}, 1)
	// Same snapshot serial, newer configuration generation: the other half of
	// derivedCacheKey must still force the rebuild.
	second := state.propertyIndexFor(snapshot, &config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{"area": {Type: config.KindWikilink}},
	}, 2)
	if first == second {
		t.Fatalf("property index reused across a configuration generation change")
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
