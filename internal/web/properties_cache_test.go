package web

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

func newPropertyCacheState(t *testing.T) *vaultState {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, root, "Notes/Alpha.md", "---\nstatus: active\n---\nBody\n")
	_, v := testVaultOpen(t, root, "127.0.0.1:0")
	state, err := newVaultState(v, time.Minute)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	return state
}

func TestPropertyIndexFor_ReusesIndexForSameSnapshotAndGeneration(t *testing.T) {
	state := newPropertyCacheState(t)
	fm := &config.FrontmatterConfig{}

	snapshot, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("currentSnapshot: %v", err)
	}
	first := state.propertyIndexFor(snapshot, fm, 1)
	second := state.propertyIndexFor(snapshot, fm, 1)

	if first != second {
		t.Fatalf("property index rebuilt for an unchanged snapshot and generation")
	}
}

func TestPropertyIndexFor_RebuildsOnNoteReload(t *testing.T) {
	state := newPropertyCacheState(t)
	fm := &config.FrontmatterConfig{}

	snapshot, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("currentSnapshot: %v", err)
	}
	first := state.propertyIndexFor(snapshot, fm, 1)

	writeTestFile(t, state.vault.Root, "Notes/Beta.md", "---\nstatus: draft\n---\nBody\n")
	reloaded, err := state.refreshSnapshot()
	if err != nil {
		t.Fatalf("refreshSnapshot: %v", err)
	}
	if reloaded.serial == snapshot.serial {
		t.Fatalf("snapshot serial did not advance after a reload")
	}

	second := state.propertyIndexFor(reloaded, fm, 1)
	if first == second {
		t.Fatalf("property index reused across a note reload")
	}
	if second.TotalNotes != 2 {
		t.Fatalf("TotalNotes = %d, want 2", second.TotalNotes)
	}
}

func TestPropertyIndexFor_RebuildsOnConfigurationGeneration(t *testing.T) {
	state := newPropertyCacheState(t)

	snapshot, err := state.currentSnapshot()
	if err != nil {
		t.Fatalf("currentSnapshot: %v", err)
	}
	first := state.propertyIndexFor(snapshot, &config.FrontmatterConfig{}, 1)
	second := state.propertyIndexFor(snapshot, &config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{"area": {Type: config.KindWikilink}},
	}, 2)

	if first == second {
		t.Fatalf("property index reused across a configuration generation change")
	}
	if _, ok := second.Property("area"); !ok {
		t.Fatalf("rebuilt index does not know the newly configured property")
	}
	if _, ok := first.Property("area"); ok {
		t.Fatalf("the previously published index was mutated; it must stay immutable")
	}
}
