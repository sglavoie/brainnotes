package config

import (
	"strings"
	"testing"
)

const patchSample = `# Brain Notes configuration.
ignore = ["Attachments"]

[templates]
dir = "Templates"

[frontmatter.properties.tags]
# element kind matters for ` + "`bn set`" + ` splitting
type = "list"
items = "string"

[frontmatter.properties.status]
type = "string"
allowed = ["draft", "done"]

[serve]
addr = "127.0.0.1:8080"
`

func applyPatch(t *testing.T, source string, patches ...PropertyPatch) PatchResult {
	t.Helper()
	result, err := PatchProperties([]byte(source), patches)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	return result
}

func TestPatchPropertiesUpsertRewritesOnlyTargetBlock(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "status",
		Definition: PropertyDefinition{Type: KindString, Required: true, Allowed: []string{"draft", "done", "archived"}},
	})
	if !result.Changed {
		t.Fatal("upsert should change the file")
	}
	patched := string(result.Patched)

	want := "[frontmatter.properties.status]\ntype = \"string\"\nrequired = true\nallowed = [\"draft\", \"done\", \"archived\"]\n"
	if !strings.Contains(patched, want) {
		t.Fatalf("rewritten block missing:\n%s", patched)
	}
	for _, unrelated := range []string{
		"# Brain Notes configuration.",
		"ignore = [\"Attachments\"]",
		"[templates]\ndir = \"Templates\"",
		"[serve]\naddr = \"127.0.0.1:8080\"",
		"# element kind matters for `bn set` splitting",
		"[frontmatter.properties.tags]\n",
	} {
		if !strings.Contains(patched, unrelated) {
			t.Fatalf("unrelated content lost: %q\n%s", unrelated, patched)
		}
	}
	if !strings.HasSuffix(patched, "\n") {
		t.Fatal("trailing newline should be preserved")
	}
}

func TestPatchPropertiesDiffPreviewMatchesWrittenBytes(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "status",
		Definition: PropertyDefinition{Type: KindString, Required: true},
	})

	before := strings.Split(strings.TrimSuffix(patchSample, "\n"), "\n")
	after := strings.Split(strings.TrimSuffix(string(result.Patched), "\n"), "\n")

	// Replay the diff against the source: removing every removed line and
	// inserting every added line must reproduce the patched bytes exactly.
	replay := append([]string(nil), before...)
	removedOffsets := 0
	for _, line := range result.Diff {
		if line.Kind != PatchLineRemoved {
			continue
		}
		idx := line.Line - 1 - removedOffsets
		if idx < 0 || idx >= len(replay) {
			t.Fatalf("removed line %d out of range", line.Line)
		}
		if replay[idx] != line.Text {
			t.Fatalf("removed line %d = %q, diff says %q", line.Line, replay[idx], line.Text)
		}
		replay = append(replay[:idx], replay[idx+1:]...)
		removedOffsets++
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
	if strings.Join(replay, "\n") != strings.Join(after, "\n") {
		t.Fatalf("diff replay does not reproduce the patched file:\n--- replay ---\n%s\n--- patched ---\n%s",
			strings.Join(replay, "\n"), strings.Join(after, "\n"))
	}
}

func TestPatchPropertiesUpsertAppendsNewBlockAfterExistingOnes(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "area",
		Definition: PropertyDefinition{Type: KindWikilink},
	})
	patched := string(result.Patched)
	if !strings.Contains(patched, "[frontmatter.properties.area]\ntype = \"wikilink\"") {
		t.Fatalf("new block missing:\n%s", patched)
	}
	statusAt := strings.Index(patched, "[frontmatter.properties.status]")
	areaAt := strings.Index(patched, "[frontmatter.properties.area]")
	serveAt := strings.Index(patched, "[serve]")
	if !(statusAt < areaAt && areaAt < serveAt) {
		t.Fatalf("new block should sit between the last property and [serve]:\n%s", patched)
	}
}

func TestPatchPropertiesUpsertIntoFileWithNoPropertiesAppendsAtEnd(t *testing.T) {
	source := "[serve]\naddr = \"127.0.0.1:8080\"\n"
	result := applyPatch(t, source, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "tags",
		Definition: PropertyDefinition{Type: KindList, Items: KindString},
	})
	patched := string(result.Patched)
	if !strings.HasPrefix(patched, source) {
		t.Fatalf("existing content should be preserved verbatim:\n%s", patched)
	}
	if !strings.Contains(patched, "[frontmatter.properties.tags]\ntype = \"list\"\nitems = \"string\"") {
		t.Fatalf("new block missing:\n%s", patched)
	}
}

func TestPatchPropertiesDeleteRemovesBlockWithoutGluingSections(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{Op: PatchDelete, Key: "tags"})
	patched := string(result.Patched)
	if strings.Contains(patched, "[frontmatter.properties.tags]") {
		t.Fatalf("deleted block still present:\n%s", patched)
	}
	if strings.Contains(patched, "items = \"string\"") {
		t.Fatalf("deleted block body still present:\n%s", patched)
	}
	if !strings.Contains(patched, "[templates]\ndir = \"Templates\"\n\n[frontmatter.properties.status]") {
		t.Fatalf("sections should stay separated:\n%s", patched)
	}
	if !strings.Contains(patched, "[serve]\naddr = \"127.0.0.1:8080\"") {
		t.Fatalf("unrelated section lost:\n%s", patched)
	}
}

func TestPatchPropertiesRenameKeepsBody(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{Op: PatchRename, Key: "status", NewKey: "state"})
	patched := string(result.Patched)
	if !strings.Contains(patched, "[frontmatter.properties.state]\ntype = \"string\"\nallowed = [\"draft\", \"done\"]") {
		t.Fatalf("renamed block should keep its body:\n%s", patched)
	}
	if strings.Contains(patched, "[frontmatter.properties.status]") {
		t.Fatalf("old header still present:\n%s", patched)
	}
}

func TestPatchPropertiesQuotesKeysNeedingIt(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "my property",
		Definition: PropertyDefinition{Type: KindString},
	})
	if !strings.Contains(string(result.Patched), `[frontmatter.properties."my property"]`) {
		t.Fatalf("key needing quotes was not quoted:\n%s", result.Patched)
	}
}

func TestPatchPropertiesRoundTripsThroughDecode(t *testing.T) {
	result := applyPatch(t, patchSample,
		PropertyPatch{Op: PatchUpsert, Key: "area", Definition: PropertyDefinition{Type: KindWikilink}},
		PropertyPatch{Op: PatchDelete, Key: "tags"},
		PropertyPatch{Op: PatchRename, Key: "status", NewKey: "state"},
	)
	cfg, err := Decode("/vault", VaultOriginFlag, result.Patched)
	if err != nil {
		t.Fatalf("patched config should still parse: %v\n%s", err, result.Patched)
	}
	if def, ok := cfg.Frontmatter.Properties["area"]; !ok || def.Type != KindWikilink {
		t.Fatalf("area not decoded: %+v", cfg.Frontmatter.Properties)
	}
	if _, ok := cfg.Frontmatter.Properties["tags"]; ok {
		t.Fatal("tags should be gone")
	}
	if def, ok := cfg.Frontmatter.Properties["state"]; !ok || def.Type != KindString {
		t.Fatalf("state not decoded: %+v", cfg.Frontmatter.Properties)
	}
}

func TestPatchPropertiesNoOpLeavesBytesUnchanged(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{
		Op:         PatchUpsert,
		Key:        "tags",
		Definition: PropertyDefinition{Type: KindList, Items: KindString},
	})
	if !result.Changed {
		t.Fatal("comment inside the rewritten block is dropped, so this is a change")
	}

	again, err := PatchProperties(result.Patched, []PropertyPatch{{
		Op:         PatchUpsert,
		Key:        "tags",
		Definition: PropertyDefinition{Type: KindList, Items: KindString},
	}})
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if again.Changed {
		t.Fatalf("re-applying the same definition should be a no-op:\n%s", again.Patched)
	}
	if len(again.Diff) != 0 {
		t.Fatalf("no-op patch should produce no diff, got %v", again.Diff)
	}
}

func TestPatchPropertiesRejectsInvalidRequests(t *testing.T) {
	cases := []struct {
		name  string
		patch PropertyPatch
		want  string
	}{
		{"empty key", PropertyPatch{Op: PatchUpsert}, "requires a property name"},
		{"unknown op", PropertyPatch{Op: "nope", Key: "tags"}, "unknown patch operation"},
		{"delete missing", PropertyPatch{Op: PatchDelete, Key: "absent"}, "is not declared"},
		{"rename missing", PropertyPatch{Op: PatchRename, Key: "absent", NewKey: "other"}, "is not declared"},
		{"rename without target", PropertyPatch{Op: PatchRename, Key: "tags"}, "requires a new name"},
		{"rename onto existing", PropertyPatch{Op: PatchRename, Key: "tags", NewKey: "status"}, "already declared"},
		{"bad type", PropertyPatch{Op: PatchUpsert, Key: "tags", Definition: PropertyDefinition{Type: "nope"}}, "unsupported type"},
		{"list without items", PropertyPatch{Op: PatchUpsert, Key: "tags", Definition: PropertyDefinition{Type: KindList}}, "supported items kind"},
		{"items without list", PropertyPatch{Op: PatchUpsert, Key: "status", Definition: PropertyDefinition{Type: KindString, Items: KindString}}, "is not a list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PatchProperties([]byte(patchSample), []PropertyPatch{tc.patch})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestPatchPropertiesIgnoresLookalikeHeaders(t *testing.T) {
	source := strings.Join([]string{
		`[[frontmatter.properties.tags]]`,
		`type = "list"`,
		``,
		`[frontmatter.properties.tags]`,
		`type = "list"`,
		`items = "string"`,
		``,
	}, "\n")
	result := applyPatch(t, source, PropertyPatch{Op: PatchDelete, Key: "tags"})
	patched := string(result.Patched)
	if !strings.Contains(patched, `[[frontmatter.properties.tags]]`) {
		t.Fatalf("array-of-tables header must not be treated as a property block:\n%s", patched)
	}
	if strings.Contains(patched, "items = \"string\"") {
		t.Fatalf("real block should be gone:\n%s", patched)
	}
}

func TestPatchPropertiesRenameIsIdempotentForSameKey(t *testing.T) {
	result := applyPatch(t, patchSample, PropertyPatch{Op: PatchRename, Key: "tags", NewKey: "tags"})
	if result.Changed {
		t.Fatal("renaming a property to its own name should change nothing")
	}
}
