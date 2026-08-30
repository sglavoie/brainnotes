package config

import (
	"strings"
	"testing"
)

const autosavePatchSample = `# Brain Notes configuration.
ignore = ["Attachments"]

[templates]
dir = "Templates"

[autosave]
# How often the watcher may commit on its own.
min_interval = "1h"   # deliberately quiet

[serve]
addr = "127.0.0.1:8080"
`

func setAutosave(t *testing.T, source, value string) PatchResult {
	t.Helper()
	result, err := PatchAutosaveMinInterval([]byte(source), value)
	if err != nil {
		t.Fatalf("PatchAutosaveMinInterval(%q): %v", value, err)
	}
	return result
}

// assertOnlyLinesChanged pins byte preservation: every line except the ones
// named by changed must survive the patch unaltered, in order.
func assertOnlyLinesChanged(t *testing.T, source string, result PatchResult, changed ...string) {
	t.Helper()
	before := strings.Split(source, "\n")
	after := strings.Split(string(result.Patched), "\n")
	isChanged := func(line string) bool {
		for _, c := range changed {
			if line == c {
				return true
			}
		}
		return false
	}
	var keptBefore, keptAfter []string
	for _, line := range before {
		if !isChanged(line) {
			keptBefore = append(keptBefore, line)
		}
	}
	for _, line := range after {
		if !isChanged(line) {
			keptAfter = append(keptAfter, line)
		}
	}
	if strings.Join(keptBefore, "\n") != strings.Join(keptAfter, "\n") {
		t.Fatalf("unrelated lines changed:\n--- before ---\n%s\n--- after ---\n%s",
			strings.Join(keptBefore, "\n"), strings.Join(keptAfter, "\n"))
	}
}

// assertDiffReproducesPatch replays the reported diff against the source and
// requires it to rebuild the patched bytes exactly, so the preview a caller
// shows is the change it makes.
func assertDiffReproducesPatch(t *testing.T, source string, result PatchResult) {
	t.Helper()
	replay := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	removed := 0
	for _, line := range result.Diff {
		if line.Kind != PatchLineRemoved {
			continue
		}
		idx := line.Line - 1 - removed
		if idx < 0 || idx >= len(replay) {
			t.Fatalf("removed line %d out of range", line.Line)
		}
		if replay[idx] != line.Text {
			t.Fatalf("removed line %d = %q, diff says %q", line.Line, replay[idx], line.Text)
		}
		replay = append(replay[:idx], replay[idx+1:]...)
		removed++
	}
	for _, line := range result.Diff {
		if line.Kind != PatchLineAdded {
			continue
		}
		idx := line.Line - 1
		if idx < 0 || idx > len(replay) {
			t.Fatalf("added line %d out of range", line.Line)
		}
		replay = append(replay[:idx], append([]string{line.Text}, replay[idx:]...)...)
	}
	want := strings.Split(strings.TrimSuffix(string(result.Patched), "\n"), "\n")
	if strings.Join(replay, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diff replay does not reproduce the patched file:\n--- replay ---\n%s\n--- patched ---\n%s",
			strings.Join(replay, "\n"), strings.Join(want, "\n"))
	}
}

func TestPatchAutosaveUpdatesOnlyTheAssignment(t *testing.T) {
	result := setAutosave(t, autosavePatchSample, "45m")
	if !result.Changed {
		t.Fatal("a new value should change the file")
	}
	patched := string(result.Patched)
	if !strings.Contains(patched, `min_interval = "45m" # deliberately quiet`) {
		t.Fatalf("inline comment should survive the rewrite:\n%s", patched)
	}
	if strings.Contains(patched, `"1h"`) {
		t.Fatalf("old value still present:\n%s", patched)
	}
	assertOnlyLinesChanged(t, autosavePatchSample, result,
		`min_interval = "1h"   # deliberately quiet`,
		`min_interval = "45m" # deliberately quiet`)
	assertDiffReproducesPatch(t, autosavePatchSample, result)
	if !strings.HasSuffix(patched, "\n") {
		t.Fatal("trailing newline should be preserved")
	}
}

func TestPatchAutosaveWritesTheCanonicalValue(t *testing.T) {
	result := setAutosave(t, autosavePatchSample, "300s")
	if !strings.Contains(string(result.Patched), `min_interval = "5m" # deliberately quiet`) {
		t.Fatalf("value should be normalized before it is written:\n%s", result.Patched)
	}

	// The stored value already means the requested one, so re-writing it is a
	// semantic no-op even though the two spellings differ.
	noop, err := PatchAutosaveMinInterval(result.Patched, "5m0s")
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if noop.Changed {
		t.Fatalf("re-writing the same policy should change nothing:\n%s", noop.Patched)
	}
	if len(noop.Diff) != 0 {
		t.Fatalf("no-op patch should produce no diff, got %v", noop.Diff)
	}
}

// The no-op rule is drawn at the policy, not at the file: a patch declines
// whenever the body already means what was asked for, however it spells it and
// whether it spells it at all. That is what keeps a converging script — one
// that reasserts the same floor on every run — from writing a config mutation
// on every run.
func TestPatchAutosaveDeclinesAPolicyAlreadyInForce(t *testing.T) {
	for _, tc := range []struct{ name, source, value string }{
		{"the canonical spelling of the stored value", "[autosave]\nmin_interval = \"300s\"\n", "5m"},
		{"another non-canonical spelling of it", "[autosave]\nmin_interval = \"300s\"\n", "5m0s"},
		{"the spelling already stored", "[autosave]\nmin_interval = \"1h\"\n", "1h"},
		{"a quoted key holding a literal string", "[autosave]\n  \"min_interval\" = '300s'\n", "5m"},
		{"an inline comment beside the value", "[autosave]\nmin_interval = \"300s\" # quiet\n", "5m"},
		{"a dotted root key", "autosave.min_interval = \"300s\"\n\n[serve]\naddr = \"x\"\n", "5m"},
		{"off over off", "[autosave]\nmin_interval = \"off\"\n", "off"},
		{"the default over an absent assignment", "[templates]\ndir = \"Templates\"\n", DefaultAutosaveMinInterval},
		{"the default over an empty file", "", DefaultAutosaveMinInterval},
		{"the default over the starter template", DefaultTemplate(), DefaultAutosaveMinInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := setAutosave(t, tc.source, tc.value)
			if result.Changed {
				t.Fatalf("the policy is unchanged, so the patch should report no change:\n%s", result.Patched)
			}
			if len(result.Diff) != 0 {
				t.Fatalf("a no-op patch should produce no diff, got %v", result.Diff)
			}
			if string(result.Patched) != tc.source {
				t.Fatalf("a no-op patch returned %q, want the source unchanged", result.Patched)
			}
		})
	}
}

// The other side of that rule: a policy that genuinely moves is written, and it
// is written canonically even when the value it replaces was not.
func TestPatchAutosaveRewritesANonCanonicalSpellingWhenThePolicyMoves(t *testing.T) {
	const source = "[autosave]\nmin_interval = \"300s\" # quiet\n"
	result := setAutosave(t, source, "600s")
	if !result.Changed {
		t.Fatal("5m to 10m is a change")
	}
	if got := string(result.Patched); got != "[autosave]\nmin_interval = \"10m\" # quiet\n" {
		t.Fatalf("brainnotes.toml =\n%q\nwant the canonical spelling of the new policy", got)
	}
}

// A stored value that cannot be read as a policy is not a policy in force, so
// there is nothing to decline against and the assignment is rewritten. Every
// one of these bodies is a config the loader already refuses; rewriting the one
// line this patcher owns is what makes the file say something again.
func TestPatchAutosaveRewritesAnUnreadableStoredValue(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"a number", "[autosave]\nmin_interval = 300\n"},
		{"an empty string", "[autosave]\nmin_interval = \"\"\n"},
		{"a string that is not a duration", "[autosave]\nmin_interval = \"soon\"\n"},
		{"an array", "[autosave]\nmin_interval = [\"5m\"]\n"},
		{"a multi-line string", "[autosave]\nmin_interval = \"\"\"5m\"\"\"\n"},
	} {
		source := tc.source
		t.Run(tc.name, func(t *testing.T) {
			result := setAutosave(t, source, DefaultAutosaveMinInterval)
			if !result.Changed {
				t.Fatalf("an unreadable value should be rewritten, not accepted as %q", DefaultAutosaveMinInterval)
			}
			if !strings.Contains(string(result.Patched), `min_interval = "5m"`) {
				t.Fatalf("the canonical value should have been written:\n%s", result.Patched)
			}
			if strings.Count(string(result.Patched), "min_interval") != 1 {
				t.Fatalf("exactly one assignment should remain:\n%s", result.Patched)
			}
		})
	}
}

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
