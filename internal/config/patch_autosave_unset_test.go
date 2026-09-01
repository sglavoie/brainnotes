package config

import (
	"strings"
	"testing"
)

// Unset removes the assignment, and a multi-line value is one assignment: the
// lines its value occupies go with it, and nothing around them moves.
func TestUnsetAutosaveRemovesAMultiLineValueWhole(t *testing.T) {
	source := "[autosave]\n# documented\nmin_interval = \"\"\"\n[serve]\n1h\"\"\" # quiet\n\n[capture]\nheading = \"## Captures\"\n"
	result := UnsetAutosaveMinInterval([]byte(source))
	want := "[autosave]\n# documented\n\n[capture]\nheading = \"## Captures\"\n"
	if string(result.Patched) != want {
		t.Fatalf("brainnotes.toml =\n%q\nwant\n%q", result.Patched, want)
	}
	cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
	if err != nil {
		t.Fatalf("patched config should still parse: %v\n%s", err, result.Patched)
	}
	if cfg.Autosave.MinInterval != DefaultAutosaveMinInterval {
		t.Fatalf("MinInterval = %q, want the restored default %q", cfg.Autosave.MinInterval, DefaultAutosaveMinInterval)
	}
}

func TestUnsetAutosaveRemovesOnlyTheAssignment(t *testing.T) {
	result := UnsetAutosaveMinInterval([]byte(autosavePatchSample))
	if !result.Changed {
		t.Fatal("removing the assignment is a change")
	}
	patched := string(result.Patched)
	if strings.Contains(patched, "min_interval =") {
		t.Fatalf("assignment should be gone:\n%s", patched)
	}
	if !strings.Contains(patched, "[autosave]\n# How often the watcher may commit on its own.\n\n[serve]") {
		t.Fatalf("table and surrounding comments should stay:\n%s", patched)
	}
	assertOnlyLinesChanged(t, autosavePatchSample, result, `min_interval = "1h"   # deliberately quiet`)
	assertDiffReproducesPatch(t, autosavePatchSample, result)

	cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
	if err != nil {
		t.Fatalf("patched config should still parse: %v\n%s", err, patched)
	}
	if cfg.Autosave.MinInterval != DefaultAutosaveMinInterval {
		t.Fatalf("unset config should fall back to %q, got %q",
			DefaultAutosaveMinInterval, cfg.Autosave.MinInterval)
	}
}

func TestUnsetAutosaveOfAnAbsentKeyIsANoOp(t *testing.T) {
	for _, source := range []string{
		"[templates]\ndir = \"Templates\"\n",
		"[autosave]\n# nothing set here\n",
		"",
	} {
		result := UnsetAutosaveMinInterval([]byte(source))
		if result.Changed {
			t.Fatalf("unsetting an absent key should change nothing:\n%q", result.Patched)
		}
		if len(result.Diff) != 0 {
			t.Fatalf("no-op unset should produce no diff, got %v", result.Diff)
		}
		if string(result.Patched) != source {
			t.Fatalf("no-op unset returned %q, want %q", result.Patched, source)
		}
	}
}

func TestUnsetAutosaveRemovesADottedRootKey(t *testing.T) {
	source := "autosave.min_interval = \"1h\"\n\n[serve]\naddr = \"x\"\n"
	result := UnsetAutosaveMinInterval([]byte(source))
	if string(result.Patched) != "\n[serve]\naddr = \"x\"\n" {
		t.Fatalf("dotted key should be removed and nothing else:\n%q", result.Patched)
	}
}

func TestPatchAutosaveSetThenUnsetRestoresTheOriginalBytes(t *testing.T) {
	source := "[templates]\ndir = \"Templates\"\n\n[autosave]\n# documented\nmin_interval = \"1h\"\n"
	set := setAutosave(t, source, "10m")
	back := UnsetAutosaveMinInterval(set.Patched)
	if string(back.Patched) != "[templates]\ndir = \"Templates\"\n\n[autosave]\n# documented\n" {
		t.Fatalf("unset should leave the table and its comment behind:\n%q", back.Patched)
	}
}
