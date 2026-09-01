package config

import (
	"strings"
	"testing"
)

func TestPatchAutosaveInsertsIntoAnExistingTable(t *testing.T) {
	source := "[templates]\ndir = \"Templates\"\n\n[autosave]\n# the floor is documented here\n\n[serve]\naddr = \"x\"\n"
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	want := "[autosave]\n# the floor is documented here\nmin_interval = \"10m\"\n\n[serve]"
	if !strings.Contains(patched, want) {
		t.Fatalf("assignment should be appended inside the table, below its comments:\n%s", patched)
	}
	assertOnlyLinesChanged(t, source, result, `min_interval = "10m"`)
	assertDiffReproducesPatch(t, source, result)
}

func TestPatchAutosaveInsertsTheTableAtEndOfFile(t *testing.T) {
	source := "# header comment\n[templates]\ndir = \"Templates\"\n"
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	if !strings.HasPrefix(patched, source) {
		t.Fatalf("existing content should be preserved verbatim:\n%s", patched)
	}
	if !strings.HasSuffix(patched, "\n[autosave]\nmin_interval = \"10m\"\n") {
		t.Fatalf("table should be appended in the template-consistent position:\n%s", patched)
	}
	cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
	if err != nil {
		t.Fatalf("patched config should still parse: %v\n%s", err, patched)
	}
	if cfg.Autosave.MinInterval != "10m" {
		t.Fatalf("MinInterval = %q, want %q", cfg.Autosave.MinInterval, "10m")
	}
}

func TestPatchAutosaveInsertsIntoAnEmptyFile(t *testing.T) {
	result := setAutosave(t, "", "off")
	if string(result.Patched) != "[autosave]\nmin_interval = \"off\"\n" {
		t.Fatalf("unexpected body for an empty source:\n%q", result.Patched)
	}
	if !result.Changed {
		t.Fatal("writing into an empty file is a change")
	}
}

func TestPatchAutosavePreservesMissingFinalNewline(t *testing.T) {
	source := "[templates]\ndir = \"Templates\""
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	if strings.HasSuffix(patched, "\n") {
		t.Fatalf("a file with no final newline should not gain one:\n%q", patched)
	}
	if !strings.HasPrefix(patched, source+"\n\n[autosave]\n") {
		t.Fatalf("existing bytes should be preserved:\n%q", patched)
	}
}

func TestPatchAutosavePreservesCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(autosavePatchSample, "\n", "\r\n")
	result := setAutosave(t, crlf, "45m")
	patched := string(result.Patched)
	if strings.Count(patched, "\n") != strings.Count(patched, "\r\n") {
		t.Fatalf("every line ending should stay CRLF:\n%q", patched)
	}
	if !strings.Contains(patched, "min_interval = \"45m\" # deliberately quiet\r\n") {
		t.Fatalf("rewritten line lost its CRLF:\n%q", patched)
	}

	insert := "[templates]\r\ndir = \"Templates\"\r\n"
	added := setAutosave(t, insert, "10m")
	if string(added.Patched) != insert+"\r\n[autosave]\r\nmin_interval = \"10m\"\r\n" {
		t.Fatalf("inserted lines should use the file's CRLF endings:\n%q", added.Patched)
	}
}

func TestPatchAutosaveUpdatesADottedRootKey(t *testing.T) {
	source := "autosave.min_interval = \"1h\"\n\n[serve]\naddr = \"x\"\n"
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	if !strings.HasPrefix(patched, "autosave.min_interval = \"10m\"\n") {
		t.Fatalf("dotted key should be updated in place:\n%s", patched)
	}
	if strings.Contains(patched, "[autosave]") {
		t.Fatalf("a duplicate table must not be added for a key TOML already binds:\n%s", patched)
	}
	if _, err := Decode("/vault", VaultOriginFlag, result.Patched); err != nil {
		t.Fatalf("patched config should still parse: %v\n%s", err, patched)
	}
}

func TestPatchAutosaveIgnoresLookalikeKeys(t *testing.T) {
	source := strings.Join([]string{
		`[cockpit]`,
		`min_interval = "1h"`,
		`autosave.min_interval = "1h"`,
		`# min_interval = "2h"`,
		``,
	}, "\n")
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	if !strings.HasPrefix(patched, source) {
		t.Fatalf("keys belonging to another table must not be touched:\n%s", patched)
	}
	if !strings.Contains(patched, "[autosave]\nmin_interval = \"10m\"") {
		t.Fatalf("a real [autosave] table should be added:\n%s", patched)
	}
	if strings.Count(patched, `min_interval = "10m"`) != 1 {
		t.Fatalf("exactly one assignment should be written:\n%s", patched)
	}
}

func TestPatchAutosaveUpdatesAQuotedKey(t *testing.T) {
	source := "[autosave]\n  \"min_interval\" = '1h'\n"
	result := setAutosave(t, source, "10m")
	patched := string(result.Patched)
	if patched != "[autosave]\n  min_interval = \"10m\"\n" {
		t.Fatalf("quoted key should be rewritten in place, keeping its indent:\n%q", patched)
	}
}

func TestPatchAutosaveRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", "requires a value"},
		{"whitespace", "  ", "requires a value"},
		{"below the bound", "1s", "between 30s and 24h inclusive"},
		{"bad casing", "OFF", "lower case"},
		{"bad syntax", "soon", "Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := PatchAutosaveMinInterval([]byte(autosavePatchSample), tc.value)
			if err == nil {
				t.Fatalf("expected an error, got %q", result.Patched)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
			if result.Patched != nil || result.Changed {
				t.Fatalf("a rejected patch must not produce a body: %+v", result)
			}
		})
	}
}
