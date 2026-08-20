package recall

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// minContextGrowthStart is a starting budget small enough that any real
// curated packet has to grow past it, which pins the growth to the mandatory
// minimum rather than to slack.
const minContextGrowthStart = 1024

func curatedAnchorFixture() Document {
	return contextDocumentFixture("Anchor.md", "Anchor", "Quasar evidence and bridge systems.\n")
}

func curatedSupporterFixture(name string) Document {
	return contextDocumentFixture(
		name+".md",
		name,
		strings.Join([]string{
			name + " opening without useful overlap.",
			"",
			"## Bridge systems",
			"",
			"Quasar evidence appears in " + name + " as **original** Markdown.",
		}, "\n"),
	)
}

func curatedSupporterResults(documents ...Document) []Result {
	results := make([]Result, 0, len(documents))
	for index, document := range documents {
		score := 0.1 * float64(index+1)
		results = append(results, contextResultFixture(document, score, RelationshipDirect, "direct link"))
	}
	return results
}

// assertCuratedExcerptsAreExactSpans checks that every excerpt reproduces the
// supporter's original Markdown at the line numbers it advertises.
func assertCuratedExcerptsAreExactSpans(t *testing.T, bundle ContextBundle, documents []Document) {
	t.Helper()
	manifestIndex := 0
	for _, related := range bundle.Related {
		body := relatedDocument(documents, related.Identity.Path).Body
		if len(related.Excerpts) == 0 {
			t.Fatalf("supporter %q contributed no excerpt", related.Identity.Path)
		}
		for _, excerpt := range related.Excerpts {
			if !utf8.ValidString(excerpt.Markdown) {
				t.Errorf("excerpt from %q split UTF-8", excerpt.Path)
			}
			offset := strings.Index(body, excerpt.Markdown)
			if excerpt.Markdown == "" || offset < 0 {
				t.Fatalf("excerpt %q is not exact source text from %q", excerpt.Markdown, excerpt.Path)
			}
			if want := strings.Count(body[:offset], "\n") + 1; excerpt.StartLine != want {
				t.Errorf("excerpt start line = %d, want %d", excerpt.StartLine, want)
			}
			if want := excerpt.StartLine + strings.Count(excerpt.Markdown, "\n"); excerpt.EndLine != want {
				t.Errorf("excerpt end line = %d, want %d", excerpt.EndLine, want)
			}
			if manifestIndex >= len(bundle.Manifest.Excerpts) {
				t.Fatalf("manifest is missing an entry for excerpt %d of %q", manifestIndex, excerpt.Path)
			}
			want := ContextManifestExcerpt{
				Path:      excerpt.Path,
				StartLine: excerpt.StartLine,
				EndLine:   excerpt.EndLine,
			}
			if got := bundle.Manifest.Excerpts[manifestIndex]; got != want {
				t.Errorf("manifest excerpt %d = %#v, want %#v", manifestIndex, got, want)
			}
			manifestIndex++
		}
	}
	if manifestIndex != len(bundle.Manifest.Excerpts) {
		t.Errorf("manifest has %d excerpts, want %d", len(bundle.Manifest.Excerpts), manifestIndex)
	}
}

func TestBuildCuratedContextBundleKeepsCallerOrderAndMandatoryExcerpts(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	first := curatedSupporterFixture("First")
	second := curatedSupporterFixture("Second")
	third := curatedSupporterFixture("Third")
	documents := []Document{third, anchor, first, second}
	// Ascending scores prove selection order is never re-sorted by rank.
	supporters := curatedSupporterResults(third, first, second)

	options := DefaultCuratedBundleOptions()
	options.Purpose = ContextPurpose{Name: "synthesis", Instructions: "Find tensions."}
	bundle, err := BuildCuratedContextBundle(anchor, documents, supporters, options)
	if err != nil {
		t.Fatalf("BuildCuratedContextBundle: %v", err)
	}
	if bundle.Source.Body != anchor.Body {
		t.Errorf("anchor body = %q, want verbatim %q", bundle.Source.Body, anchor.Body)
	}
	wantOrder := []string{third.Path, first.Path, second.Path}
	for index, related := range bundle.Related {
		if related.Identity.Path != wantOrder[index] {
			t.Errorf("supporter %d = %q, want %q", index, related.Identity.Path, wantOrder[index])
		}
		if related.Rank != index+1 {
			t.Errorf("supporter %d rank = %d, want %d", index, related.Rank, index+1)
		}
	}
	if got := bundle.Manifest.MaxBytes; got != DefaultBundleMaxBytes {
		t.Errorf("effective limit = %d, want the unchanged default %d", got, DefaultBundleMaxBytes)
	}
	assertCuratedExcerptsAreExactSpans(t, bundle, documents)

	markdown, err := RenderContextMarkdown(bundle)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	if bundle.Manifest.ByteCount != len(markdown) {
		t.Errorf("manifest byte count = %d, want %d", bundle.Manifest.ByteCount, len(markdown))
	}

	empty, err := BuildCuratedContextBundle(anchor, documents, nil, options)
	if err != nil {
		t.Fatalf("zero-supporter BuildCuratedContextBundle: %v", err)
	}
	if empty.Related == nil || len(empty.Related) != 0 {
		t.Errorf("zero-supporter related = %#v, want non-nil empty slice", empty.Related)
	}
	if empty.Manifest.Excerpts == nil || len(empty.Manifest.Excerpts) != 0 {
		t.Errorf("zero-supporter manifest excerpts = %#v, want non-nil empty slice", empty.Manifest.Excerpts)
	}
}

func TestBuildCuratedContextBundleRejectsInvalidSelections(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	first := curatedSupporterFixture("First")
	blank := contextDocumentFixture("Blank.md", "Blank", "")
	missing := contextDocumentFixture("Missing.md", "Missing", "Quasar evidence elsewhere.")
	documents := []Document{anchor, first, blank}
	options := DefaultCuratedBundleOptions()

	tests := []struct {
		name       string
		supporters []Result
		want       error
	}{
		{
			name:       "duplicate supporter",
			supporters: curatedSupporterResults(first, first),
			want:       ErrCuratedDuplicateSupporter,
		},
		{
			name:       "anchor as supporter",
			supporters: curatedSupporterResults(first, anchor),
			want:       ErrCuratedAnchorSupporter,
		},
		{
			name:       "supporter outside the corpus",
			supporters: curatedSupporterResults(missing),
			want:       ErrCuratedSupporterMissing,
		},
		{
			name:       "supporter with no extractable block",
			supporters: curatedSupporterResults(blank),
			want:       ErrCuratedSupporterEmpty,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildCuratedContextBundle(anchor, documents, test.supporters, options); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	overLimit := make([]Document, 0, MaxCuratedSupporters+1)
	crowded := []Document{anchor}
	for index := range MaxCuratedSupporters + 1 {
		document := curatedSupporterFixture(fmt.Sprintf("Extra%02d", index))
		overLimit = append(overLimit, document)
		crowded = append(crowded, document)
	}
	if _, err := BuildCuratedContextBundle(
		anchor,
		crowded,
		curatedSupporterResults(overLimit...),
		options,
	); !errors.Is(err, ErrCuratedSupporterLimit) {
		t.Fatalf("over-limit error = %v, want %v", err, ErrCuratedSupporterLimit)
	}
	if _, err := BuildCuratedContextBundle(
		anchor,
		crowded,
		curatedSupporterResults(overLimit[:MaxCuratedSupporters]...),
		options,
	); err != nil {
		t.Fatalf("at-limit BuildCuratedContextBundle: %v", err)
	}
}

func TestBuildCuratedContextBundleGrowsBudgetOnlyForMandatoryContent(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	documents := []Document{anchor}
	supporterDocuments := make([]Document, 0, MaxCuratedSupporters)
	for index := range MaxCuratedSupporters {
		document := curatedSupporterFixture(fmt.Sprintf("Supporter%02d", index))
		supporterDocuments = append(supporterDocuments, document)
		documents = append(documents, document)
	}
	supporters := curatedSupporterResults(supporterDocuments...)

	options := DefaultCuratedBundleOptions()
	spacious, err := BuildCuratedContextBundle(anchor, documents, supporters, options)
	if err != nil {
		t.Fatalf("64 KiB BuildCuratedContextBundle: %v", err)
	}
	if got := spacious.Manifest.MaxBytes; got != DefaultBundleMaxBytes {
		t.Errorf("64 KiB effective limit = %d, want %d", got, DefaultBundleMaxBytes)
	}
	if got := len(spacious.Related); got != MaxCuratedSupporters {
		t.Fatalf("64 KiB supporters = %d, want %d", got, MaxCuratedSupporters)
	}

	tight := DefaultCuratedBundleOptions()
	tight.MaxBytes = minContextGrowthStart
	grown, err := BuildCuratedContextBundle(anchor, documents, supporters, tight)
	if err != nil {
		t.Fatalf("grown BuildCuratedContextBundle: %v", err)
	}
	if grown.Manifest.MaxBytes <= minContextGrowthStart {
		t.Fatalf("grown limit = %d, want growth beyond %d", grown.Manifest.MaxBytes, minContextGrowthStart)
	}
	if grown.Manifest.MaxBytes > MaxCuratedBundleBytes {
		t.Fatalf("grown limit = %d, want at most %d", grown.Manifest.MaxBytes, MaxCuratedBundleBytes)
	}
	for _, related := range grown.Related {
		if len(related.Excerpts) != 1 {
			t.Errorf(
				"supporter %q has %d excerpts under a minimal budget, want exactly the mandatory one",
				related.Identity.Path,
				len(related.Excerpts),
			)
		}
	}
	assertCuratedExcerptsAreExactSpans(t, grown, documents)

	markdown, err := RenderContextMarkdown(grown)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	jsonData, err := RenderContextJSON(grown)
	if err != nil {
		t.Fatalf("RenderContextJSON: %v", err)
	}
	if len(markdown) > grown.Manifest.MaxBytes || len(jsonData) > grown.Manifest.MaxBytes {
		t.Errorf(
			"sizes Markdown=%d JSON=%d exceed the effective limit %d",
			len(markdown),
			len(jsonData),
			grown.Manifest.MaxBytes,
		)
	}
	if grown.Manifest.MaxBytes != max(len(markdown), len(jsonData)) {
		t.Errorf(
			"effective limit = %d, want the minimum requirement %d",
			grown.Manifest.MaxBytes,
			max(len(markdown), len(jsonData)),
		)
	}
}

func TestBuildCuratedContextBundleAdmitsOptionalExcerptsAtRuneBoundaries(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	optionalBlock := "quasar " + strings.Repeat("界", MaxExcerptRunes)
	supporter := contextDocumentFixture(
		"Supporter.md",
		"Supporter",
		"Quasar evidence and bridge systems appear here.\n\n"+optionalBlock,
	)
	documents := []Document{anchor, supporter}
	supporters := curatedSupporterResults(supporter)

	mandatoryOnly := DefaultCuratedBundleOptions()
	mandatoryOnly.MaxBytes = minContextGrowthStart
	minimal, err := BuildCuratedContextBundle(anchor, documents, supporters, mandatoryOnly)
	if err != nil {
		t.Fatalf("minimal BuildCuratedContextBundle: %v", err)
	}
	if got := len(minimal.Related[0].Excerpts); got != 1 {
		t.Fatalf("minimal excerpts = %d, want only the mandatory one", got)
	}
	if got := minimal.Related[0].Excerpts[0].Markdown; strings.Contains(got, "界") {
		t.Errorf("mandatory excerpt = %q, want the highest-scoring block", got)
	}

	roomy := DefaultCuratedBundleOptions()
	roomy.MaxBytes = minimal.Manifest.MaxBytes + 1200
	bundle, err := BuildCuratedContextBundle(anchor, documents, supporters, roomy)
	if err != nil {
		t.Fatalf("roomy BuildCuratedContextBundle: %v", err)
	}
	if got := bundle.Manifest.MaxBytes; got != roomy.MaxBytes {
		t.Errorf("effective limit = %d, want the unchanged start %d", got, roomy.MaxBytes)
	}
	if got := len(bundle.Related[0].Excerpts); got != 2 {
		t.Fatalf("roomy excerpts = %d, want the mandatory plus one optional", got)
	}
	optional := bundle.Related[0].Excerpts[1]
	if !optional.Truncated {
		t.Error("budgeted optional excerpt was not marked truncated")
	}
	if !utf8.ValidString(optional.Markdown) {
		t.Error("budgeted optional excerpt split UTF-8")
	}
	if !strings.HasPrefix(optionalBlock, optional.Markdown) {
		t.Errorf("budgeted optional excerpt %q is not an original prefix", optional.Markdown)
	}
	assertCuratedExcerptsAreExactSpans(t, bundle, documents)

	markdown, err := RenderContextMarkdown(bundle)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	jsonData, err := RenderContextJSON(bundle)
	if err != nil {
		t.Fatalf("RenderContextJSON: %v", err)
	}
	if len(markdown) > roomy.MaxBytes || len(jsonData) > roomy.MaxBytes {
		t.Errorf("sizes Markdown=%d JSON=%d exceed %d", len(markdown), len(jsonData), roomy.MaxBytes)
	}
}

func TestBuildCuratedContextBundleIsDeterministicAndRendererEquivalent(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	first := curatedSupporterFixture("First")
	second := curatedSupporterFixture("Second")
	supporters := curatedSupporterResults(second, first)
	options := DefaultCuratedBundleOptions()
	options.Purpose = ContextPurpose{Name: "questions", Instructions: "Identify unanswered questions."}

	forward, err := BuildCuratedContextBundle(anchor, []Document{anchor, first, second}, supporters, options)
	if err != nil {
		t.Fatalf("forward BuildCuratedContextBundle: %v", err)
	}
	reversed, err := BuildCuratedContextBundle(anchor, []Document{second, first, anchor}, supporters, options)
	if err != nil {
		t.Fatalf("reversed BuildCuratedContextBundle: %v", err)
	}
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("corpus order changed the bundle\nforward:  %#v\nreversed: %#v", forward, reversed)
	}

	markdown, err := RenderContextMarkdown(forward)
	if err != nil {
		t.Fatalf("RenderContextMarkdown: %v", err)
	}
	jsonData, err := RenderContextJSON(forward)
	if err != nil {
		t.Fatalf("RenderContextJSON: %v", err)
	}
	reversedMarkdown, err := RenderContextMarkdown(reversed)
	if err != nil {
		t.Fatalf("reversed RenderContextMarkdown: %v", err)
	}
	if !reflect.DeepEqual(markdown, reversedMarkdown) {
		t.Error("repeated Markdown rendering was not byte-identical")
	}
	for _, related := range forward.Related {
		if !strings.Contains(string(markdown), fmt.Sprintf("- Path: %q", related.Identity.Path)) {
			t.Errorf("Markdown does not name supporter %q", related.Identity.Path)
		}
		for _, excerpt := range related.Excerpts {
			if !strings.Contains(string(markdown), excerpt.Markdown) {
				t.Errorf("Markdown is missing the exact excerpt from %q", excerpt.Path)
			}
			if !strings.Contains(string(jsonData), fmt.Sprintf("%q", excerpt.Path)) {
				t.Errorf("JSON is missing supporter %q", excerpt.Path)
			}
		}
	}
	if forward.Manifest.ByteCount != len(markdown) {
		t.Errorf("manifest byte count = %d, want the Markdown size %d", forward.Manifest.ByteCount, len(markdown))
	}
}

func TestBuildCuratedContextBundleRejectsContentAboveTheHardLimit(t *testing.T) {
	t.Parallel()

	anchor := curatedAnchorFixture()
	options := DefaultCuratedBundleOptions()
	options.MaxBytes = MaxCuratedBundleBytes + 1
	if _, err := BuildCuratedContextBundle(anchor, []Document{anchor}, nil, options); err == nil ||
		!strings.Contains(err.Error(), "exceeds the 1048576 byte maximum") {
		t.Fatalf("oversized start error = %v", err)
	}

	oversized := contextDocumentFixture("Huge.md", "Huge", strings.Repeat("a", MaxCuratedBundleBytes+1))
	if _, err := BuildCuratedContextBundle(
		oversized,
		[]Document{oversized},
		nil,
		DefaultCuratedBundleOptions(),
	); !errors.Is(err, ErrSourceBodyTooLarge) {
		t.Fatalf("oversized anchor error = %v, want %v", err, ErrSourceBodyTooLarge)
	}

	framed := contextDocumentFixture("Framed.md", "Framed", strings.Repeat("a", MaxCuratedBundleBytes-10))
	if _, err := BuildCuratedContextBundle(
		framed,
		[]Document{framed},
		nil,
		DefaultCuratedBundleOptions(),
	); !errors.Is(err, ErrRequiredContextTooLarge) {
		t.Fatalf("oversized framing error = %v, want %v", err, ErrRequiredContextTooLarge)
	}

	nearLimit := contextDocumentFixture("Near.md", "Near", strings.Repeat("a", MaxCuratedBundleBytes-4096))
	supporter := curatedSupporterFixture("Supporter")
	documents := []Document{nearLimit, supporter}
	if _, err := BuildCuratedContextBundle(
		nearLimit,
		documents,
		nil,
		DefaultCuratedBundleOptions(),
	); err != nil {
		t.Fatalf("near-limit anchor without supporters: %v", err)
	}
	supporters := make([]Result, 0, MaxCuratedSupporters)
	for index := range MaxCuratedSupporters {
		document := contextDocumentFixture(
			fmt.Sprintf("Bulk%02d.md", index),
			fmt.Sprintf("Bulk%02d", index),
			"Quasar bridge evidence "+strings.Repeat("界", MaxExcerptRunes-3),
		)
		documents = append(documents, document)
		supporters = append(supporters, contextResultFixture(document, 1, RelationshipDirect, "direct link"))
	}
	if _, err := BuildCuratedContextBundle(
		nearLimit,
		documents,
		supporters,
		DefaultCuratedBundleOptions(),
	); !errors.Is(err, ErrCuratedContextTooLarge) {
		t.Fatalf("mandatory overflow error = %v, want %v", err, ErrCuratedContextTooLarge)
	}
}
