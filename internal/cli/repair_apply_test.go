package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func writeRepairNote(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestApplyRepairs_NoActions(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\nstatus: WIP\n---\nBody.\n")
	original := readNote(t, path)

	n := &vault.Note{Path: path}
	changed, err := applyRepairs(n, schema.RepairResult{}, nil, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for empty actions")
	}
	got := readNote(t, path)
	if got != original {
		t.Fatalf("file should be untouched:\n%s", got)
	}
}

func TestApplyRepairs_SingleAction(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\nstatus: wip\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
		},
	}
	changed, err := applyRepairs(n, result, &config.FrontmatterConfig{}, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}
	got := readNote(t, path)
	if !strings.Contains(got, "status: WIP") {
		t.Fatalf("expected status: WIP, got:\n%s", got)
	}
	if !strings.Contains(got, "Body.") {
		t.Fatalf("body should be preserved, got:\n%s", got)
	}
}

func TestApplyRepairs_MultipleActions(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md",
		"---\ndraft: true\nstatus: wip\ntags:\n  - go\n  - go\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "draft", Kind: "type_coerce", OldValue: "true", NewValue: true},
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
			{Property: "tags", Kind: "list_dedup", OldValue: []any{"go", "go"}, NewValue: []any{"go"}},
		},
	}
	changed, err := applyRepairs(n, result, &config.FrontmatterConfig{}, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}
	got := readNote(t, path)
	if !strings.Contains(got, "draft: true") {
		t.Fatalf("expected draft: true, got:\n%s", got)
	}
	if !strings.Contains(got, "status: WIP") {
		t.Fatalf("expected status: WIP, got:\n%s", got)
	}
	if strings.Count(got, "- go") != 1 {
		t.Fatalf("expected single 'go' tag, got:\n%s", got)
	}
}

func TestApplyRepairs_TypeCoerceDate(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\ncreated: \"2024-06-15\"\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{
				Property: "created",
				Kind:     "type_coerce",
				OldValue: "2024-06-15",
				NewValue: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	changed, err := applyRepairs(n, result, &config.FrontmatterConfig{}, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}
	got := readNote(t, path)
	if !strings.Contains(got, "created: 2024-06-15") {
		t.Fatalf("expected created: 2024-06-15, got:\n%s", got)
	}
}

func TestApplyRepairs_WikilinkWrap(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\nparent: Books\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "parent", Kind: "wikilink_wrap", OldValue: "Books", NewValue: "[[Books]]"},
		},
	}
	fmCfg := &config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"parent": {Type: config.KindWikilink},
		},
	}
	changed, err := applyRepairs(n, result, fmCfg, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}
	got := readNote(t, path)
	if !strings.Contains(got, "[[Books]]") {
		t.Fatalf("expected [[Books]] in output, got:\n%s", got)
	}
}

func TestApplyRepairs_DryRunEmitsDiff(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\nstatus: wip\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
		},
	}

	var buf bytes.Buffer
	dry := &DryRun{Out: &buf}
	changed, err := applyRepairs(n, result, nil, dir, dry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change reported in dry-run")
	}

	out := buf.String()
	if !strings.Contains(out, "--- note.md") {
		t.Fatalf("expected file header, got %q", out)
	}
	if !strings.Contains(out, "frontmatter[status]: wip -> WIP") {
		t.Fatalf("expected canonicalize change line, got %q", out)
	}

	got := readNote(t, path)
	if !strings.Contains(got, "status: wip") {
		t.Fatalf("dry-run should not modify file, got:\n%s", got)
	}
}

func TestApplyRepairs_DryRunMultipleActions(t *testing.T) {
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md",
		"---\nstatus: wip\ntags:\n  - go\n  - go\n---\nBody.\n")

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
			{Property: "tags", Kind: "list_dedup",
				OldValue: []any{"go", "go"},
				NewValue: []any{"go"}},
		},
	}

	var buf bytes.Buffer
	dry := &DryRun{Out: &buf}
	changed, err := applyRepairs(n, result, nil, dir, dry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}

	out := buf.String()
	if !strings.Contains(out, "frontmatter[status]: wip -> WIP") {
		t.Fatalf("missing status change line, got %q", out)
	}
	if !strings.Contains(out, "frontmatter[tags]: [go, go] -> [go]") {
		t.Fatalf("missing tags change line, got %q", out)
	}
}

func TestApplyRepairs_BodyPreserved(t *testing.T) {
	body := "\n## Heading\n\nSome content with [[links]] and `code`.\n"
	dir := t.TempDir()
	path := writeRepairNote(t, dir, "note.md", "---\nstatus: wip\n---\n"+body)

	n := &vault.Note{Path: path}
	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
		},
	}
	changed, err := applyRepairs(n, result, &config.FrontmatterConfig{}, dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected change")
	}
	got := readNote(t, path)
	if !strings.Contains(got, body) {
		t.Fatalf("body should be preserved verbatim, got:\n%s", got)
	}
}

func TestApplyRepairs_Deterministic(t *testing.T) {
	dir := t.TempDir()
	content := "---\nstatus: wip\ntags:\n  - go\n  - go\n---\nBody.\n"

	result := schema.RepairResult{
		Actions: []schema.RepairAction{
			{Property: "status", Kind: "canonicalize", OldValue: "wip", NewValue: "WIP"},
			{Property: "tags", Kind: "list_dedup",
				OldValue: []any{"go", "go"},
				NewValue: []any{"go"}},
		},
	}
	fmCfg := &config.FrontmatterConfig{}

	var outputs []string
	for i := 0; i < 3; i++ {
		path := writeRepairNote(t, dir, "note.md", content)
		n := &vault.Note{Path: path}
		if _, err := applyRepairs(n, result, fmCfg, dir, nil); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		outputs = append(outputs, readNote(t, path))
	}
	for i := 1; i < len(outputs); i++ {
		if outputs[i] != outputs[0] {
			t.Fatalf("non-deterministic output:\nrun 0:\n%s\nrun %d:\n%s", outputs[0], i, outputs[i])
		}
	}
}
