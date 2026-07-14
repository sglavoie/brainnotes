package recall

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestExtractDocumentBuildsDeterministicReadOnlyModel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	decomposedTitle := "Cafe\u0301"
	body := strings.Join([]string{
		"# Héading *bold*",
		"",
		"Paragraph with **nested** text, [label](https://example.com), and `inlineCode`.",
		"",
		"> Quoted *idea*.",
		">",
		"> Second line.",
		"",
		"- first item",
		"- second with **nested** item",
		"",
		"| Topic | Detail |",
		"| --- | --- |",
		"| Recall | Context |",
		"",
		"```go",
		"fencedSecret()",
		"```",
		"",
		"    indentedSecret()",
		"",
		"<div>",
		"htmlSecret",
		"</div>",
		"",
	}, "\n")
	source := strings.Join([]string{
		"---",
		"created: 2024-01-02",
		"categories:",
		"  - '[[Library/Café.md#Part|Display]]'",
		"  - Science",
		"tags: [Go, go, 7, true]",
		"ignored: do-not-index",
		"---",
		body,
	}, "\n")
	path := writeRecallNote(t, root, decomposedTitle+".md", source)
	note := &vault.Note{Path: path}
	options := ExtractOptions{MetadataProperties: []string{"tags", "categories", "tags"}}

	document, err := ExtractDocument(root, note, options)
	if err != nil {
		t.Fatalf("ExtractDocument: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after extraction: %v", err)
	}
	if string(before) != source {
		t.Fatal("extraction modified the note")
	}

	if document.Path != "Café.md" {
		t.Errorf("Path = %q, want NFC-normalized Café.md", document.Path)
	}
	if document.Title != "Café" {
		t.Errorf("Title = %q, want NFC-normalized Café", document.Title)
	}
	if document.Body != body {
		t.Fatalf("Body was not preserved verbatim\n got: %q\nwant: %q", document.Body, body)
	}
	wantCreated := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	if !document.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", document.CreatedAt, wantCreated)
	}
	wantMetadata := []MetadataValue{
		{Property: "categories", Value: "café"},
		{Property: "categories", Value: "science"},
		{Property: "tags", Value: "7"},
		{Property: "tags", Value: "go"},
		{Property: "tags", Value: "true"},
	}
	if !reflect.DeepEqual(document.Metadata, wantMetadata) {
		t.Errorf("Metadata = %#v, want %#v", document.Metadata, wantMetadata)
	}

	if len(document.Fields) == 0 || document.Fields[0] != (TextField{
		Kind: TextTitle, Text: "Café", Weight: TitleWeight, BlockIndex: -1,
	}) {
		t.Fatalf("title field = %#v", document.Fields)
	}
	for index, field := range document.Fields[1:] {
		if field.BlockIndex != index {
			t.Errorf("field %d BlockIndex = %d, want %d", index+1, field.BlockIndex, index)
		}
		if field.Kind == TextHeading && field.Weight != HeadingWeight {
			t.Errorf("heading weight = %v, want %v", field.Weight, HeadingWeight)
		}
		if field.Kind == TextProse && field.Weight != ProseWeight {
			t.Errorf("prose weight = %v, want %v", field.Weight, ProseWeight)
		}
	}

	allText := fieldText(document.Fields)
	for _, present := range []string{"Héading bold", "nested text", "inlineCode", "Quoted idea", "first item", "Recall Context"} {
		if !strings.Contains(allText, present) {
			t.Errorf("weighted text %q does not contain %q", allText, present)
		}
	}
	for _, excluded := range []string{"fencedSecret", "indentedSecret", "htmlSecret", "do-not-index"} {
		if strings.Contains(allText, excluded) {
			t.Errorf("weighted text %q unexpectedly contains %q", allText, excluded)
		}
	}

	assertBlockSource(t, document, BlockHeading, "# Héading *bold*")
	assertBlockSource(t, document, BlockTable, "| Topic | Detail |\n| --- | --- |\n| Recall | Context |")
	for index, block := range document.Blocks {
		sourceBlock, ok := document.BlockSource(index)
		if !ok {
			t.Fatalf("BlockSource(%d) rejected %#v", index, block.Location)
		}
		if sourceBlock == "" {
			t.Errorf("BlockSource(%d) is empty", index)
		}
		if block.Location.StartLine < 1 || block.Location.EndLine < block.Location.StartLine {
			t.Errorf("block %d has invalid lines: %#v", index, block.Location)
		}
	}

	again, err := ExtractDocument(root, note, options)
	if err != nil {
		t.Fatalf("second ExtractDocument: %v", err)
	}
	if !reflect.DeepEqual(document, again) {
		t.Errorf("repeated extraction differs\nfirst:  %#v\nsecond: %#v", document, again)
	}
}

func TestExtractDocumentBodyAndFrontmatterEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		wantBody   string
		wantText   string
		wantBlocks int
	}{
		{
			name:       "body only",
			source:     "Body-only **note**.\n",
			wantBody:   "Body-only **note**.\n",
			wantText:   "Body-only note.",
			wantBlocks: 1,
		},
		{
			name:       "body only with BOM",
			source:     "\ufeff# Heading with BOM\n",
			wantBody:   "\ufeff# Heading with BOM\n",
			wantText:   "Heading with BOM",
			wantBlocks: 1,
		},
		{
			name:       "CRLF frontmatter and body",
			source:     "---\r\ncreated: 2025-05-06\r\n---\r\n# Heading\r\n\r\nProse.\r\n",
			wantBody:   "# Heading\r\n\r\nProse.\r\n",
			wantText:   "Heading | Prose.",
			wantBlocks: 2,
		},
		{
			name:       "empty note",
			source:     "",
			wantBody:   "",
			wantText:   "Edge",
			wantBlocks: 0,
		},
		{
			name:       "unterminated frontmatter is body",
			source:     "---\ntags: [still, body]\n",
			wantBody:   "---\ntags: [still, body]\n",
			wantText:   "tags: [still, body]",
			wantBlocks: 1,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			path := writeRecallNote(t, root, "Edge.md", test.source)
			document, err := ExtractDocument(root, &vault.Note{Path: path}, ExtractOptions{})
			if err != nil {
				t.Fatalf("ExtractDocument: %v", err)
			}
			if document.Body != test.wantBody {
				t.Errorf("Body = %q, want %q", document.Body, test.wantBody)
			}
			if len(document.Blocks) != test.wantBlocks {
				t.Errorf("len(Blocks) = %d, want %d: %#v", len(document.Blocks), test.wantBlocks, document.Blocks)
			}
			if got := fieldText(document.Fields); !strings.Contains(got, test.wantText) {
				t.Errorf("field text = %q, want it to contain %q", got, test.wantText)
			}
		})
	}
}

func TestExtractDocumentRejectsMalformedFrontmatterWithoutWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := "---\ntags: [unterminated\n---\nBody\n"
	path := writeRecallNote(t, root, "Broken.md", source)
	_, err := ExtractDocument(root, &vault.Note{Path: path}, ExtractOptions{MetadataProperties: []string{"tags"}})
	if err == nil || !strings.Contains(err.Error(), "parse yaml") {
		t.Fatalf("ExtractDocument error = %v, want malformed YAML error", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != source {
		t.Fatalf("malformed extraction changed file: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(statErr) {
		t.Fatalf("read-only extraction created .bnotes: %v", statErr)
	}
}

func TestExtractCorpusSortsByNormalizedPathAndFailsClosed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	noteB := &vault.Note{Path: writeRecallNote(t, root, "B.md", "B body\n")}
	noteA := &vault.Note{Path: writeRecallNote(t, root, "A.md", "A body\n")}
	documents, err := ExtractCorpus(root, []*vault.Note{noteB, noteA}, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractCorpus: %v", err)
	}
	if got := []string{documents[0].Path, documents[1].Path}; !reflect.DeepEqual(got, []string{"A.md", "B.md"}) {
		t.Errorf("paths = %v, want stable sorted paths", got)
	}

	outside := &vault.Note{Path: writeRecallNote(t, t.TempDir(), "Outside.md", "body\n")}
	documents, err = ExtractCorpus(root, []*vault.Note{noteA, outside}, ExtractOptions{})
	if err == nil || !strings.Contains(err.Error(), "outside vault root") {
		t.Fatalf("ExtractCorpus error = %v, want outside-root error", err)
	}
	if documents != nil {
		t.Errorf("failed corpus = %#v, want nil rather than partial output", documents)
	}
}

func TestExtractMetadataHonorsExplicitDisableAndCanonicalizesScalars(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := strings.Join([]string{
		"---",
		"created: '2024-03-04'",
		"topics: 'See [[Mixed/Link]]'",
		"score: 3.0",
		"nested:",
		"  name: ignored-map",
		"---",
		"body",
	}, "\n")
	path := writeRecallNote(t, root, "Metadata.md", source)

	disabled, err := ExtractDocument(root, &vault.Note{Path: path}, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDocument disabled metadata: %v", err)
	}
	if disabled.Metadata != nil {
		t.Errorf("empty metadata config = %#v, want nil", disabled.Metadata)
	}
	if disabled.CreatedAt.Format("2006-01-02") != "2024-03-04" {
		t.Errorf("quoted CreatedAt = %v, want 2024-03-04", disabled.CreatedAt)
	}

	enabled, err := ExtractDocument(root, &vault.Note{Path: path}, ExtractOptions{
		MetadataProperties: []string{"topics", "score", "nested"},
	})
	if err != nil {
		t.Fatalf("ExtractDocument enabled metadata: %v", err)
	}
	want := []MetadataValue{
		{Property: "score", Value: "3.0"},
		{Property: "topics", Value: "see [[mixed/link]]"},
	}
	if !reflect.DeepEqual(enabled.Metadata, want) {
		t.Errorf("Metadata = %#v, want %#v", enabled.Metadata, want)
	}
}

func writeRecallNote(t *testing.T, root, relativePath, source string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func fieldText(fields []TextField) string {
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		values = append(values, field.Text)
	}
	return strings.Join(values, " | ")
}

func assertBlockSource(t *testing.T, document Document, kind BlockKind, want string) {
	t.Helper()
	for index, block := range document.Blocks {
		if block.Kind != kind {
			continue
		}
		got, ok := document.BlockSource(index)
		if !ok {
			t.Fatalf("BlockSource(%d) rejected %#v", index, block.Location)
		}
		if got != want {
			t.Errorf("%s source = %q, want %q", kind, got, want)
		}
		return
	}
	t.Errorf("no %s block found in %#v", kind, document.Blocks)
}
