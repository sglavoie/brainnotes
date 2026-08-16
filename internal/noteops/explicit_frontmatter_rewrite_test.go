package noteops

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestRenameTraversesExplicitWikilinksInEveryFrontmatterProperty(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\n" +
		"arbitrary: [[Old]]\n" +
		"nested:\n" +
		"  refs:\n" +
		"    - [[Folder/Old.md#Heading|Alias]]\n" +
		"    - before [[Old]] after\n" +
		"plain: Old\n" +
		"---\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	changes := ScanFrontmatterRename("Source.md", &doc.Front, "Old", "New")
	wantChanges := []Change{
		{File: "Source.md", Kind: ChangeFrontmatter, Key: "arbitrary", OldText: "[[Old]]", NewText: "[[New]]"},
		{File: "Source.md", Kind: ChangeFrontmatter, Key: "nested", OldText: "[[Folder/Old.md#Heading|Alias]]", NewText: "[[Folder/New.md#Heading|Alias]]"},
		{File: "Source.md", Kind: ChangeFrontmatter, Key: "nested", OldText: "before [[Old]] after", NewText: "before [[New]] after"},
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("ScanFrontmatterRename changes = %#v, want %#v", changes, wantChanges)
	}

	RewriteFrontmatterRename(&doc.Front, "Old", "New", &config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"configured_only": {Type: config.KindWikilink},
		},
	})
	assertNoteopsFrontmatterValue(t, &doc.Front, "arbitrary", "[[New]]")
	assertNoteopsFrontmatterValue(t, &doc.Front, "nested", map[string]any{
		"refs": []any{"[[Folder/New.md#Heading|Alias]]", "before [[New]] after"},
	})
	assertNoteopsFrontmatterValue(t, &doc.Front, "plain", "Old")
}

func TestMovePreviewAndApplyShareRecursiveFrontmatterTraversal(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\n" +
		"nested:\n" +
		"  refs: [ [[Old/Thing]], [[Old/Thing.md#Part|Alias]] ]\n" +
		"plain: Old/Thing\n" +
		"---\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	changes := ScanFrontmatterMove("Source.md", &doc.Front, "Old/Thing", "New/Thing")
	RewriteFrontmatterMove(&doc.Front, "Old/Thing", "New/Thing", nil)
	if len(changes) != 2 {
		t.Fatalf("ScanFrontmatterMove returned %d changes, want 2: %#v", len(changes), changes)
	}
	nested, _ := doc.Front.Get("nested")
	wantNested := map[string]any{"refs": []any{"[[New/Thing]]", "[[New/Thing.md#Part|Alias]]"}}
	if !reflect.DeepEqual(nested, wantNested) {
		t.Fatalf("applied nested value = %#v, want %#v", nested, wantNested)
	}
	for i, change := range changes {
		refs := wantNested["refs"].([]any)
		if change.NewText != refs[i] {
			t.Fatalf("preview change %d = %#v, applied value = %#v", i, change, refs[i])
		}
	}
	assertNoteopsFrontmatterValue(t, &doc.Front, "plain", "Old/Thing")
}

func assertNoteopsFrontmatterValue(t *testing.T, front *frontmatter.Map, key string, want any) {
	t.Helper()
	got, ok := front.Get(key)
	if !ok {
		t.Fatalf("missing frontmatter key %q", key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontmatter[%s] = %#v, want %#v", key, got, want)
	}
}
