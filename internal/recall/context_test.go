package recall

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildContextBundleSelectsBoundedRankedOriginalExcerpts(t *testing.T) {
	t.Parallel()

	source := contextDocumentFixture(
		"Source.md",
		"Source",
		"Source studies quasar evidence and bridge systems.\n",
	)
	source.Metadata = []MetadataValue{
		{Property: "topic", Value: "systems"},
		{Property: "area", Value: "research"},
	}
	first := contextDocumentFixture(
		"First.md",
		"First",
		strings.Join([]string{
			"Opening without useful overlap.",
			"",
			"## Bridge systems",
			"",
			"Quasar evidence appears in this **original** Markdown block.",
			"",
			"A final unrelated paragraph.",
		}, "\n"),
	)
	longBlock := strings.Repeat("界", MaxExcerptRunes+25)
	second := contextDocumentFixture("Second.md", "Second", longBlock)
	third := contextDocumentFixture("Third.md", "Third", "Third candidate body")
	fourth := contextDocumentFixture("Fourth.md", "Fourth", "Fourth candidate body")
	fifth := contextDocumentFixture("Fifth.md", "Fifth", "Fifth candidate body")
	sixth := contextDocumentFixture("Sixth.md", "Sixth", "Sixth candidate body")
	documents := []Document{sixth, third, source, first, fifth, second, fourth}
	results := []Result{
		contextResultFixture(first, 0.9, RelationshipTwoHop, "two hops via Bridge"),
		contextResultFixture(second, 0.8, RelationshipDirect, "direct link"),
		contextResultFixture(third, 0.7, RelationshipDirect, "direct link"),
		contextResultFixture(fourth, 0.6, RelationshipDirect, "direct link"),
		contextResultFixture(fifth, 0.5, RelationshipDirect, "direct link"),
		contextResultFixture(sixth, 0.4, RelationshipDirect, "direct link"),
	}

	options := DefaultContextBundleOptions()
	options.Purpose = ContextPurpose{Name: "synthesis", Instructions: "Find tensions and missing evidence."}
	bundle, err := BuildContextBundle(source, documents, results, options)
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if got := bundle.Source.Body; got != source.Body {
		t.Fatalf("source body = %q, want verbatim %q", got, source.Body)
	}
	if got := len(bundle.Related); got != DefaultBundleRelatedLimit {
		t.Fatalf("related count = %d, want %d", got, DefaultBundleRelatedLimit)
	}
	if got := bundle.Related[0].Identity.Path; got != first.Path {
		t.Fatalf("first result path = %q, want %q", got, first.Path)
	}
	if got := len(bundle.Related[0].Metadata); got != 0 {
		t.Errorf("first metadata count = %d, want 0", got)
	}
	if got := len(bundle.Related[0].Excerpts); got != MaxExcerptsPerRelatedNote {
		t.Fatalf("first excerpts = %d, want %d", got, MaxExcerptsPerRelatedNote)
	}
	selectedMarkdown := bundle.Related[0].Excerpts[0].Markdown + "\n" + bundle.Related[0].Excerpts[1].Markdown
	if !strings.Contains(selectedMarkdown, "Quasar evidence") {
		t.Errorf("selected excerpts = %q, want distinctive source-term block", selectedMarkdown)
	}
	if !strings.Contains(selectedMarkdown, "Bridge systems") {
		t.Errorf("selected excerpts = %q, want relationship-reason block", selectedMarkdown)
	}
	for _, related := range bundle.Related {
		if len(related.Excerpts) > MaxExcerptsPerRelatedNote {
			t.Errorf("%s has %d excerpts", related.Identity.Path, len(related.Excerpts))
		}
		for _, excerpt := range related.Excerpts {
			if excerpt.Path != related.Identity.Path {
				t.Errorf("excerpt path = %q, want %q", excerpt.Path, related.Identity.Path)
			}
			if len(excerpt.Reasons) == 0 {
				t.Errorf("excerpt %#v has no reason provenance", excerpt)
			}
			if utf8.RuneCountInString(excerpt.Markdown) > MaxExcerptRunes {
				t.Errorf("excerpt has %d runes", utf8.RuneCountInString(excerpt.Markdown))
			}
			if !strings.Contains(relatedDocument(documents, related.Identity.Path).Body, excerpt.Markdown) {
				t.Errorf("excerpt %q is not preserved source text", excerpt.Markdown)
			}
		}
	}
	if got := utf8.RuneCountInString(bundle.Related[1].Excerpts[0].Markdown); got != MaxExcerptRunes {
		t.Errorf("long excerpt runes = %d, want %d", got, MaxExcerptRunes)
	}
	if !bundle.Related[1].Excerpts[0].Truncated {
		t.Error("long excerpt was not marked truncated")
	}
	if got, want := bundle.Source.Metadata, []ContextMetadataValue{
		{Property: "area", Value: "research"},
		{Property: "topic", Value: "systems"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("source metadata = %#v, want %#v", got, want)
	}
	if got := bundle.Related[4].Identity.Path; got != fifth.Path {
		t.Errorf("last included path = %q, want %q", got, fifth.Path)
	}
}

func TestBuildContextBundleZeroValueOptionsUseDefaults(t *testing.T) {
	t.Parallel()

	source := contextDocumentFixture("Source.md", "Source", "shared source")
	documents := []Document{source}
	results := make([]Result, 0, DefaultBundleRelatedLimit+1)
	for index := 0; index < DefaultBundleRelatedLimit+1; index++ {
		document := contextDocumentFixture(
			"Related"+string(rune('A'+index))+".md",
			"Related",
			"shared related",
		)
		documents = append(documents, document)
		results = append(results, contextResultFixture(document, 1, RelationshipDirect, "direct link"))
	}
	bundle, err := BuildContextBundle(source, documents, results, ContextBundleOptions{})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if got := len(bundle.Related); got != DefaultBundleRelatedLimit {
		t.Errorf("zero-value related count = %d, want %d", got, DefaultBundleRelatedLimit)
	}
	if bundle.Manifest.MaxBytes != DefaultBundleMaxBytes {
		t.Errorf("zero-value max bytes = %d, want %d", bundle.Manifest.MaxBytes, DefaultBundleMaxBytes)
	}

	zeroOptions := ContextBundleOptions{RelatedLimitSet: true}
	zeroBundle, err := BuildContextBundle(source, documents, results, zeroOptions)
	if err != nil {
		t.Fatalf("zero-related BuildContextBundle: %v", err)
	}
	if got := len(zeroBundle.Related); got != 0 {
		t.Errorf("explicit-zero related count = %d, want 0", got)
	}
}

func TestContextRenderersAreBoundedEquivalentAndByteIdentical(t *testing.T) {
	t.Parallel()

	source := contextDocumentFixture("Source.md", "Source", "# Source\n\nBody <kept> unchanged.\n")
	candidate := contextDocumentFixture("Related.md", "Related", "Related **Markdown** body without newline")
	result := contextResultFixture(candidate, 0.75, RelationshipDirect, "direct link")
	options := DefaultContextBundleOptions()
	options.Purpose = ContextPurpose{Name: "questions", Instructions: "Identify unanswered questions."}

	first, err := BuildContextBundle(source, []Document{candidate, source}, []Result{result}, options)
	if err != nil {
		t.Fatalf("first BuildContextBundle: %v", err)
	}
	second, err := BuildContextBundle(source, []Document{source, candidate}, []Result{result}, options)
	if err != nil {
		t.Fatalf("second BuildContextBundle: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("repeated bundle differs\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if got := first.Related[0].Excerpts[0].Markdown; got == candidate.Body {
		t.Fatal("related note body was included wholesale")
	}

	markdownA, err := RenderContextMarkdown(first)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	markdownB, err := RenderContextMarkdown(second)
	if err != nil {
		t.Fatalf("second RenderContextMarkdown: %v", err)
	}
	jsonA, err := RenderContextJSON(first)
	if err != nil {
		t.Fatalf("RenderContextJSON: %v", err)
	}
	jsonB, err := RenderContextJSON(second)
	if err != nil {
		t.Fatalf("second RenderContextJSON: %v", err)
	}
	if !reflect.DeepEqual(markdownA, markdownB) || !reflect.DeepEqual(jsonA, jsonB) {
		t.Error("repeated rendering was not byte-identical")
	}
	if len(markdownA) > options.MaxBytes || len(jsonA) > options.MaxBytes {
		t.Errorf("render sizes Markdown=%d JSON=%d exceed %d", len(markdownA), len(jsonA), options.MaxBytes)
	}
	if !strings.Contains(string(markdownA), source.Body) {
		t.Error("Markdown does not preserve complete source body")
	}
	if strings.Contains(string(markdownA), "generated_at") || strings.Contains(string(jsonA), "generated_at") {
		t.Error("renderer included a generation timestamp")
	}

	var decoded ContextBundle
	if err := json.Unmarshal(jsonA, &decoded); err != nil {
		t.Fatalf("Unmarshal JSON: %v", err)
	}
	if decoded.Source.Body != source.Body {
		t.Errorf("JSON source body = %q, want %q", decoded.Source.Body, source.Body)
	}
	if !reflect.DeepEqual(decoded.Related[0].Excerpts, first.Related[0].Excerpts) {
		t.Errorf("JSON excerpts differ\n got: %#v\nwant: %#v", decoded.Related[0].Excerpts, first.Related[0].Excerpts)
	}
	if decoded.Manifest.ByteCount != len(jsonA) {
		t.Errorf("JSON manifest byte count = %d, want %d", decoded.Manifest.ByteCount, len(jsonA))
	}
	if first.Manifest.ByteCount != len(markdownA) {
		t.Errorf("bundle Markdown byte count = %d, want %d", first.Manifest.ByteCount, len(markdownA))
	}
	if got := len(first.Manifest.Excerpts); got != len(first.Related[0].Excerpts) {
		t.Errorf("manifest excerpts = %d, want %d", got, len(first.Related[0].Excerpts))
	}
}

func TestBuildContextBundleBudgetsAtRuneBoundaries(t *testing.T) {
	t.Parallel()

	source := contextDocumentFixture("Source.md", "Source", "source overlap\n")
	candidate := contextDocumentFixture(
		"Related.md",
		"Related",
		"overlap "+strings.Repeat("🙂", MaxExcerptRunes+100),
	)
	result := contextResultFixture(candidate, 1, RelationshipDirect, "direct link")

	baseOptions := DefaultContextBundleOptions()
	baseOptions.RelatedLimit = 0
	baseOptions.RelatedLimitSet = true
	baseOptions.MaxBytes = DefaultBundleMaxBytes
	base, err := BuildContextBundle(source, []Document{source, candidate}, []Result{result}, baseOptions)
	if err != nil {
		t.Fatalf("base BuildContextBundle: %v", err)
	}
	baseMarkdown, _ := RenderContextMarkdown(base)
	baseJSON, _ := RenderContextJSON(base)
	limit := max(len(baseMarkdown), len(baseJSON)) + 900

	options := DefaultContextBundleOptions()
	options.RelatedLimit = 1
	options.MaxBytes = limit
	bundle, err := BuildContextBundle(source, []Document{source, candidate}, []Result{result}, options)
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if len(bundle.Related) != 1 || len(bundle.Related[0].Excerpts) != 1 {
		t.Fatalf("budgeted related = %#v, want one excerpt", bundle.Related)
	}
	excerpt := bundle.Related[0].Excerpts[0]
	if !excerpt.Truncated {
		t.Error("budgeted excerpt was not marked truncated")
	}
	if !utf8.ValidString(excerpt.Markdown) {
		t.Error("budgeted excerpt split UTF-8")
	}
	if !strings.HasPrefix(candidate.Body, excerpt.Markdown) {
		t.Errorf("budgeted excerpt is not an original prefix: %q", excerpt.Markdown)
	}
	markdown, err := RenderContextMarkdown(bundle)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	jsonData, err := RenderContextJSON(bundle)
	if err != nil {
		t.Fatalf("RenderContextJSON: %v", err)
	}
	if len(markdown) > limit || len(jsonData) > limit {
		t.Errorf("sizes Markdown=%d JSON=%d exceed limit %d", len(markdown), len(jsonData), limit)
	}
}

func TestBuildContextBundleRejectsOversizedSourceAndInvalidInputs(t *testing.T) {
	t.Parallel()

	source := contextDocumentFixture("Source.md", "Source", strings.Repeat("é", 10))
	options := DefaultContextBundleOptions()
	options.MaxBytes = len(source.Body) - 1
	_, err := BuildContextBundle(source, []Document{source}, nil, options)
	if !errors.Is(err, ErrSourceBodyTooLarge) {
		t.Fatalf("oversized source error = %v, want ErrSourceBodyTooLarge", err)
	}

	options = DefaultContextBundleOptions()
	options.RelatedLimit = -1
	_, err = BuildContextBundle(source, []Document{source}, nil, options)
	if err == nil || !strings.Contains(err.Error(), "related limit") {
		t.Errorf("negative related limit error = %v", err)
	}

	options = DefaultContextBundleOptions()
	invalid := source
	invalid.Body = string([]byte{0xff})
	_, err = BuildContextBundle(invalid, []Document{invalid}, nil, options)
	if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Errorf("invalid UTF-8 error = %v", err)
	}

	candidate := contextDocumentFixture("Related.md", "Related", "body")
	result := contextResultFixture(candidate, 1, RelationshipDirect, "direct link")
	_, err = BuildContextBundle(source, []Document{source}, []Result{result}, options)
	if err == nil || !strings.Contains(err.Error(), "not in the document corpus") {
		t.Errorf("missing result document error = %v", err)
	}
}

func contextDocumentFixture(path, title, body string) Document {
	blocks, fields := extractMarkdown(body)
	fields = append([]TextField{{Kind: TextTitle, Text: title, Weight: TitleWeight, BlockIndex: -1}}, fields...)
	return Document{
		Path:   path,
		Title:  title,
		Body:   body,
		Fields: fields,
		Blocks: blocks,
	}
}

func contextResultFixture(document Document, score float64, relationship RelationshipClass, detail string) Result {
	return Result{
		Path:         document.Path,
		Title:        document.Title,
		Score:        score,
		Relationship: relationship,
		Reasons: []Reason{{
			Kind:         ReasonGraph,
			Contribution: score,
			Detail:       detail,
		}},
	}
}

func relatedDocument(documents []Document, path string) Document {
	for _, document := range documents {
		if document.Path == path {
			return document
		}
	}
	return Document{}
}
