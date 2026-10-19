package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func buildJSONVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	return buildVaultFromFiles(t, map[string]string{
		"Notes/Alpha.md": "---\ntitle: Alpha\ntags:\n  - x\n  - y\n---\nthe keyword is here\nsee [[Beta]]\nkeyword again\n",
		"Notes/Beta.md":  "---\ntitle: Beta\ntags:\n  - x\n---\nanother keyword usage\n",
		"Notes/Gamma.md": "---\ntitle: Gamma\n---\nlinks to [[Alpha]]\n",
	})
}

// runJSONCommand runs bn against root and decodes stdout into v. stdout must
// be valid JSON whatever the error.
func runJSONCommand(t *testing.T, root string, v any, args ...string) error {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"--vault", root}, args...))
	err := cmd.Execute()
	if decodeErr := json.Unmarshal(out.Bytes(), v); decodeErr != nil {
		t.Fatalf("%v: stdout is not JSON (%v): %q (err=%v)", args, decodeErr, out.String(), err)
	}
	return err
}

func requireNoteRecord(t *testing.T, got noteJSON, rel, title string) {
	t.Helper()
	if got.Path != rel || got.Title != title || !strings.HasSuffix(got.AbsPath, filepath.FromSlash("/"+rel)) ||
		!filepath.IsAbs(got.AbsPath) {
		t.Fatalf("record = %+v, want path %q (vault-relative), title %q, absolute abs_path", got, rel, title)
	}
}

// find, links and backlinks share one record: vault-relative path, the
// absolute path the plain listing prints, and the title.
func TestPathListingJSONSharesNoteRecord(t *testing.T) {
	root := buildJSONVault(t)

	var found []noteJSON
	if err := runJSONCommand(t, root, &found, "find", "--json", "--tag", "x"); err != nil {
		t.Fatalf("find --json: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("find --json = %+v", found)
	}
	requireNoteRecord(t, found[0], "Notes/Alpha.md", "Alpha")

	var links []noteJSON
	if err := runJSONCommand(t, root, &links, "links", "--json", "Alpha"); err != nil {
		t.Fatalf("links --json: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links --json = %+v", links)
	}
	requireNoteRecord(t, links[0], "Notes/Beta.md", "Beta")

	var backlinks []noteJSON
	if err := runJSONCommand(t, root, &backlinks, "backlinks", "--json", "Alpha"); err != nil {
		t.Fatalf("backlinks --json: %v", err)
	}
	if len(backlinks) != 1 {
		t.Fatalf("backlinks --json = %+v", backlinks)
	}
	requireNoteRecord(t, backlinks[0], "Notes/Gamma.md", "Gamma")

	var none []noteJSON
	if err := runJSONCommand(t, root, &none, "backlinks", "--json", "Gamma"); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("backlinks --json with none = %#v, err %v; want []", none, err)
	}
}

func TestLinkListingJSONRefusals(t *testing.T) {
	root := buildJSONVault(t)
	for _, args := range [][]string{
		{"links", "--json", "--null", "Alpha"},
		{"backlinks", "--json", "-v", "Alpha"},
	} {
		_, err := runCLI(t, "", append([]string{"--vault", root}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "--json cannot be combined with") {
			t.Fatalf("%v: err = %v", args, err)
		}
	}
}

func TestSearchJSON(t *testing.T) {
	root := buildJSONVault(t)

	var got []searchJSONNote
	if err := runJSONCommand(t, root, &got, "search", "--json", "keyword"); err != nil {
		t.Fatalf("search --json: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("search --json = %+v", got)
	}
	requireNoteRecord(t, got[0].noteJSON, "Notes/Alpha.md", "Alpha")
	want := []searchJSONLine{{Line: 7, Text: "the keyword is here"}, {Line: 9, Text: "keyword again"}}
	if got[0].Count != 2 || !reflect.DeepEqual(got[0].Matches, want) {
		t.Fatalf("Alpha matches = %+v", got[0])
	}
}

func TestSearchJSONNoMatchIsEmptyArray(t *testing.T) {
	root := buildJSONVault(t)
	var none []searchJSONNote
	err := runJSONCommand(t, root, &none, "search", "--json", "absent-word")
	if !errors.Is(err, ErrNoMatch) || none == nil || len(none) != 0 {
		t.Fatalf("no-match search --json = %#v, err %v; want [] and exit 1", none, err)
	}
}

func TestSearchJSONRefusals(t *testing.T) {
	root := buildJSONVault(t)
	for _, flag := range []string{"--count", "--interactive", "--open", "--null"} {
		_, err := runCLI(t, "", "--vault", root, "search", "--json", flag, "keyword")
		if err == nil || !strings.Contains(err.Error(), "search: --json cannot be combined with "+flag) {
			t.Fatalf("search --json %s: err = %v", flag, err)
		}
	}
}

// Every text mode of search reports no match as grep does: exit 1.
func TestSearchNoMatchIsNegativeInEveryMode(t *testing.T) {
	root := buildJSONVault(t)
	for _, args := range [][]string{
		{"absent-word"},
		{"absent-word", "--count"},
		{"absent-word", "--context", "1"},
		{"absent-word", "--open"},
	} {
		out, err := runSearch(t, root, args...)
		if !errors.Is(err, ErrNoMatch) || !IsNegativeOutcome(err) {
			t.Fatalf("search %v: err = %v, want ErrNoMatch", args, err)
		}
		if out != "" {
			t.Fatalf("search %v printed %q", args, out)
		}
	}
}

func TestValuesJSON(t *testing.T) {
	root := buildJSONVault(t)
	var got struct {
		Property string            `json:"property"`
		Values   []valuesJSONEntry `json:"values"`
	}
	if err := runJSONCommand(t, root, &got, "tags", "--json"); err != nil {
		t.Fatalf("tags --json: %v", err)
	}
	if got.Property != "tags" || len(got.Values) != 2 {
		t.Fatalf("tags --json = %+v", got)
	}
	first := got.Values[0]
	if first.Value != "x" || first.Count != 2 || strings.Join(first.Files, ",") != "Notes/Alpha.md,Notes/Beta.md" {
		t.Fatalf("first value = %+v", first)
	}
}

func TestViewsAndSchemaJSONEmpty(t *testing.T) {
	root := buildJSONVault(t)
	var views []viewJSON
	if err := runJSONCommand(t, root, &views, "views", "--json"); err != nil || views == nil || len(views) != 0 {
		t.Fatalf("views --json = %#v, err %v; want []", views, err)
	}
	var schema []schemaJSONProperty
	if err := runJSONCommand(t, root, &schema, "schema", "--json"); err != nil || schema == nil || len(schema) != 0 {
		t.Fatalf("schema --json = %#v, err %v; want []", schema, err)
	}
}

func buildDeclaredJSONVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, `[frontmatter.properties.status]
type = "string"
required = true
allowed = ["wip", "done"]

[[views]]
name = "WIP"
query = "status:wip"
description = "in flight"
`)
	return root
}

func TestViewsJSON(t *testing.T) {
	root := buildDeclaredJSONVault(t)
	var views []viewJSON
	if err := runJSONCommand(t, root, &views, "views", "--json"); err != nil {
		t.Fatalf("views --json: %v", err)
	}
	want := []viewJSON{{Name: "WIP", Description: "in flight", Query: "status:wip", Status: "ok"}}
	if !reflect.DeepEqual(views, want) {
		t.Fatalf("views --json = %+v", views)
	}
}

func TestSchemaJSON(t *testing.T) {
	root := buildDeclaredJSONVault(t)
	var schema []schemaJSONProperty
	if err := runJSONCommand(t, root, &schema, "schema", "--json"); err != nil {
		t.Fatalf("schema --json: %v", err)
	}
	if len(schema) != 1 || schema[0].Name != "status" || schema[0].Type != "string" || !schema[0].Required ||
		strings.Join(schema[0].Allowed, ",") != "wip,done" {
		t.Fatalf("schema --json = %+v", schema)
	}
}
