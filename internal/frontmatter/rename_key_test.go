package frontmatter

import (
	"errors"
	"testing"
)

func TestRenameKey_Success(t *testing.T) {
	m := NewMap()
	m.Set("kind", "article")
	m.Set("tags", []any{"go"})

	changed, err := m.RenameKey("kind", "type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if m.Has("kind") {
		t.Error("old key should be gone")
	}
	v, ok := m.Get("type")
	if !ok {
		t.Fatal("new key should exist")
	}
	if v != "article" {
		t.Errorf("value = %v, want article", v)
	}
}

func TestRenameKey_AbsentOldKey(t *testing.T) {
	m := NewMap()
	m.Set("status", "done")

	changed, err := m.RenameKey("kind", "type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false for absent key")
	}
}

func TestRenameKey_Conflict(t *testing.T) {
	m := NewMap()
	m.Set("kind", "article")
	m.Set("type", "reference")

	changed, err := m.RenameKey("kind", "type")
	if !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("expected ErrKeyConflict, got %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on conflict")
	}
	if v, _ := m.Get("kind"); v != "article" {
		t.Error("old key should be unchanged after conflict")
	}
	if v, _ := m.Get("type"); v != "reference" {
		t.Error("existing new key should be unchanged after conflict")
	}
}

func TestRenameKey_PreservesKeyOrder(t *testing.T) {
	m := NewMap()
	m.Set("alpha", 1)
	m.Set("kind", "article")
	m.Set("zeta", 3)

	if _, err := m.RenameKey("kind", "type"); err != nil {
		t.Fatal(err)
	}

	keys := m.Keys()
	want := []string{"alpha", "type", "zeta"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i, k := range keys {
		if k != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}
