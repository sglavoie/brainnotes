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
//
// "However it spells it" is the full TOML answer and not a list of the
// spellings a scan happens to recognize: the comparison decodes the body the
// way config loading does, so every encoding that loads as the same string is
// the same policy. A triple-quoted value, a literal string, and a Unicode
// escape are as much a no-op as a bare "300s".
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
		{"a triple-quoted basic string", "[autosave]\nmin_interval = \"\"\"5m\"\"\"\n", "5m"},
		{"a triple-quoted literal string", "[autosave]\nmin_interval = '''300s'''\n", "5m"},
		{"a triple-quoted string opened by a newline", "[autosave]\nmin_interval = \"\"\"\n300s\"\"\"\n", "5m0s"},
		{"a triple-quoted string continued by a backslash", "[autosave]\nmin_interval = \"\"\"5m\\\n\"\"\"\n", "5m"},
		{"a Unicode escape", "[autosave]\nmin_interval = \"5\\u006d\"\n", "5m"},
		{"an escaped value beside a comment", "[autosave]\nmin_interval = \"\\u0033\\u0030\\u0030s\" # quiet\n", "5m"},
		{"a multi-line value below a dotted root key", "autosave.min_interval = \"\"\"\n300s\"\"\"\n\n[serve]\naddr = \"x\"\n", "5m"},
		// The loader reads an empty value as "unset", which is the default policy
		// and not an unreadable one: applyDefaults turns it into "5m" and the
		// config loads. Rewriting it would be a commit that moves no floor.
		{"an empty string", "[autosave]\nmin_interval = \"\"\n", DefaultAutosaveMinInterval},
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
// one of these bodies is a config the loader already refuses; rewriting the
// assignment this patcher owns is what makes the file say something again.
//
// "Unreadable" is now about the value's *type and range*, not about its
// encoding: a spelling the decoder resolves to a duration is read whatever
// quotes it uses, and only a value no policy can come out of lands here.
func TestPatchAutosaveRewritesAnUnreadableStoredValue(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"a number", "[autosave]\nmin_interval = 300\n"},
		{"a boolean", "[autosave]\nmin_interval = false\n"},
		{"a string that is not a duration", "[autosave]\nmin_interval = \"soon\"\n"},
		{"a duration below the bound", "[autosave]\nmin_interval = \"1s\"\n"},
		{"a triple-quoted value that is not a duration", "[autosave]\nmin_interval = \"\"\"soon\"\"\"\n"},
		{"an array", "[autosave]\nmin_interval = [\"5m\"]\n"},
		{"an array spanning several lines", "[autosave]\nmin_interval = [\n  \"5m\", # not a policy\n]\n"},
		{"an inline table", "[autosave]\nmin_interval = { value = \"5m\" }\n"},
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
			// The repair has to leave a body the vault can load: an assignment
			// rewritten in place of a value that spanned lines must take every one
			// of those lines with it.
			cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
			if err != nil {
				t.Fatalf("the repaired config should load: %v\n%s", err, result.Patched)
			}
			if cfg.Autosave.MinInterval != DefaultAutosaveMinInterval {
				t.Fatalf("MinInterval = %q, want %q", cfg.Autosave.MinInterval, DefaultAutosaveMinInterval)
			}
		})
	}
}

// The span of a multi-line value is part of the assignment, so moving the
// policy off one rewrites the whole thing into a single canonical assignment
// rather than leaving the rest of the old value behind as unparseable text.
func TestPatchAutosaveRewritesAMultiLineValueWhenThePolicyMoves(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{
			"a triple-quoted basic string",
			"[autosave]\nmin_interval = \"\"\"\n1h\"\"\"\n\n[serve]\naddr = \"x\"\n",
			"[autosave]\nmin_interval = \"10m\"\n\n[serve]\naddr = \"x\"\n",
		},
		{
			"a triple-quoted literal string carrying a comment",
			"[autosave]\nmin_interval = '''\n1h''' # quiet\n\n[serve]\naddr = \"x\"\n",
			"[autosave]\nmin_interval = \"10m\" # quiet\n\n[serve]\naddr = \"x\"\n",
		},
		{
			"a bracketed line inside the value",
			"[autosave]\nmin_interval = \"\"\"\n[serve]\n1h\"\"\"\n\n[capture]\nheading = \"## Captures\"\n",
			"[autosave]\nmin_interval = \"10m\"\n\n[capture]\nheading = \"## Captures\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := setAutosave(t, tc.source, "10m")
			if !result.Changed {
				t.Fatal("the policy moves, so the assignment should be rewritten")
			}
			if got := string(result.Patched); got != tc.want {
				t.Fatalf("brainnotes.toml =\n%q\nwant\n%q", got, tc.want)
			}
			assertDiffReproducesPatch(t, tc.source, result)
			cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
			if err != nil {
				t.Fatalf("patched config should still parse: %v\n%s", err, result.Patched)
			}
			if cfg.Autosave.MinInterval != "10m" {
				t.Fatalf("MinInterval = %q, want %q", cfg.Autosave.MinInterval, "10m")
			}
		})
	}
}
