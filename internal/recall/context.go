package recall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultBundleRelatedLimit is the number of ranked notes included by the
	// default context command configuration.
	DefaultBundleRelatedLimit = 5
	// DefaultBundleMaxBytes is the default cap for either serialized form of a
	// context bundle.
	DefaultBundleMaxBytes = 64 * 1024
	// MaxExcerptsPerRelatedNote bounds how many source blocks one related note
	// can contribute.
	MaxExcerptsPerRelatedNote = 2
	// MaxExcerptRunes is the per-excerpt Unicode character limit.
	MaxExcerptRunes = 800
)

var (
	// ErrSourceBodyTooLarge identifies a source note whose unmodified body is
	// larger than the complete bundle budget.
	ErrSourceBodyTooLarge = errors.New("recall: context source body exceeds byte limit")
	// ErrRequiredContextTooLarge identifies required bundle framing, source
	// identity, metadata, purpose, and body that cannot fit even with no related
	// results.
	ErrRequiredContextTooLarge = errors.New("recall: required context exceeds byte limit")
)

// ContextPurpose names the requested research task and provides the exact
// deterministic instructions carried in the bundle. Purpose templates are
// owned by the CLI; the core treats these strings as data.
type ContextPurpose struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

// ContextNoteIdentity is the portable identity of a note in a context bundle.
type ContextNoteIdentity struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// ContextMetadataValue is one configured metadata property/value pair copied
// from an extracted Document.
type ContextMetadataValue struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// ContextSource contains the complete, verbatim source note body. It is the
// only context model that has a Body field: related notes can contribute only
// ContextExcerpt values.
type ContextSource struct {
	Identity ContextNoteIdentity    `json:"identity"`
	Metadata []ContextMetadataValue `json:"metadata"`
	Body     string                 `json:"body"`
}

// ContextReason is stable ranking provenance copied from a related Result.
type ContextReason struct {
	Kind         ReasonKind `json:"kind"`
	Contribution float64    `json:"contribution"`
	Detail       string     `json:"detail"`
}

// ContextExcerpt is an exact prefix of one original Markdown source block.
// StartLine and EndLine are one-based. Truncated reports either the 800-rune
// block cap, the whole-body exclusion, or total-bundle budgeting.
type ContextExcerpt struct {
	Path             string          `json:"path"`
	Title            string          `json:"title"`
	StartLine        int             `json:"start_line"`
	EndLine          int             `json:"end_line"`
	Markdown         string          `json:"markdown"`
	Truncated        bool            `json:"truncated"`
	DistinctiveTerms []string        `json:"distinctive_terms"`
	Reasons          []ContextReason `json:"reasons"`
}

// ContextRelatedResult couples one ranked result with selected metadata and
// bounded excerpts. Ranked order is recorded explicitly and preserved.
type ContextRelatedResult struct {
	Rank         int                    `json:"rank"`
	Identity     ContextNoteIdentity    `json:"identity"`
	Score        float64                `json:"score"`
	Relationship RelationshipClass      `json:"relationship"`
	Bridge       *ContextNoteIdentity   `json:"bridge,omitempty"`
	Metadata     []ContextMetadataValue `json:"metadata"`
	Reasons      []ContextReason        `json:"reasons"`
	Excerpts     []ContextExcerpt       `json:"excerpts"`
}

// ContextManifestExcerpt names one serialized excerpt without duplicating its
// Markdown content.
type ContextManifestExcerpt struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// ContextManifest names every source/excerpt path and records the byte limit
// plus the final size of the current serialized representation. Markdown and
// JSON have the same selections but naturally have different ByteCount values.
type ContextManifest struct {
	SourcePath string                   `json:"source_path"`
	Excerpts   []ContextManifestExcerpt `json:"excerpts"`
	MaxBytes   int                      `json:"max_bytes"`
	ByteCount  int                      `json:"byte_count"`
}

// ContextBundle is a deterministic, timestamp-free research packet.
type ContextBundle struct {
	Purpose  ContextPurpose         `json:"purpose"`
	Source   ContextSource          `json:"source"`
	Related  []ContextRelatedResult `json:"related"`
	Manifest ContextManifest        `json:"manifest"`
}

// ContextBundleOptions controls bounded related selection. The zero value uses
// the standard five-note and 64 KiB defaults. RelatedLimitSet distinguishes an
// explicit zero-note request from that zero-value default.
type ContextBundleOptions struct {
	RelatedLimit    int
	RelatedLimitSet bool
	MaxBytes        int
	Purpose         ContextPurpose
}

// DefaultContextBundleOptions returns the standard five-note, 64 KiB bundle
// configuration.
func DefaultContextBundleOptions() ContextBundleOptions {
	return ContextBundleOptions{
		RelatedLimit:    DefaultBundleRelatedLimit,
		RelatedLimitSet: true,
		MaxBytes:        DefaultBundleMaxBytes,
	}
}

// BuildContextBundle creates a deterministic bounded packet from an extracted
// source document, the same corpus used for ranking, and already-ranked
// results. Results are considered strictly in their supplied order.
func BuildContextBundle(
	source Document,
	documents []Document,
	results []Result,
	options ContextBundleOptions,
) (ContextBundle, error) {
	if options.RelatedLimit == 0 && !options.RelatedLimitSet {
		options.RelatedLimit = DefaultBundleRelatedLimit
	}
	if options.RelatedLimit < 0 {
		return ContextBundle{}, fmt.Errorf("recall: context related limit must be non-negative: %d", options.RelatedLimit)
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultBundleMaxBytes
	}
	if options.MaxBytes < 0 {
		return ContextBundle{}, fmt.Errorf("recall: context byte limit must be positive: %d", options.MaxBytes)
	}
	if err := validateContextString("purpose name", options.Purpose.Name); err != nil {
		return ContextBundle{}, err
	}
	if err := validateContextString("purpose instructions", options.Purpose.Instructions); err != nil {
		return ContextBundle{}, err
	}
	if err := validateContextDocument("source", source); err != nil {
		return ContextBundle{}, err
	}
	if len(source.Body) > options.MaxBytes {
		return ContextBundle{}, fmt.Errorf(
			"%w: %q is %d bytes; limit is %d",
			ErrSourceBodyTooLarge,
			source.Path,
			len(source.Body),
			options.MaxBytes,
		)
	}

	documentsByPath, lexicalDocuments, err := contextDocumentIndex(source, documents)
	if err != nil {
		return ContextBundle{}, err
	}
	lexical, err := BuildLexicalIndex(lexicalDocuments)
	if err != nil {
		return ContextBundle{}, fmt.Errorf("recall: context lexical index: %w", err)
	}

	bundle := ContextBundle{
		Purpose: options.Purpose,
		Source: ContextSource{
			Identity: ContextNoteIdentity{Path: source.Path, Title: source.Title},
			Metadata: contextMetadata(source.Metadata),
			Body:     source.Body,
		},
		Related: make([]ContextRelatedResult, 0, min(options.RelatedLimit, len(results))),
		Manifest: ContextManifest{
			SourcePath: source.Path,
			Excerpts:   make([]ContextManifestExcerpt, 0),
			MaxBytes:   options.MaxBytes,
		},
	}
	withinLimit, err := contextBundleWithinLimit(bundle, options.MaxBytes)
	if err != nil {
		return ContextBundle{}, err
	}
	if !withinLimit {
		return ContextBundle{}, fmt.Errorf(
			"%w: source %q and required framing do not fit in %d bytes",
			ErrRequiredContextTooLarge,
			source.Path,
			options.MaxBytes,
		)
	}

	seenResults := make(map[string]struct{}, min(options.RelatedLimit, len(results)))
	resultLimit := min(options.RelatedLimit, len(results))
	budgetExhausted := false
	for resultIndex := 0; resultIndex < resultLimit && !budgetExhausted; resultIndex++ {
		result := results[resultIndex]
		if result.Path == source.Path {
			return ContextBundle{}, fmt.Errorf("recall: context result %d repeats source path %q", resultIndex, result.Path)
		}
		if _, exists := seenResults[result.Path]; exists {
			return ContextBundle{}, fmt.Errorf("recall: context duplicate result path %q", result.Path)
		}
		seenResults[result.Path] = struct{}{}

		candidate, ok := documentsByPath[result.Path]
		if !ok {
			return ContextBundle{}, fmt.Errorf("recall: context result path %q is not in the document corpus", result.Path)
		}
		if err := validateContextDocument("related", candidate); err != nil {
			return ContextBundle{}, err
		}
		related, err := contextRelatedResult(resultIndex+1, result, candidate)
		if err != nil {
			return ContextBundle{}, err
		}

		candidateBundle := appendContextRelated(bundle, related)
		withinLimit, err = contextBundleWithinLimit(candidateBundle, options.MaxBytes)
		if err != nil {
			return ContextBundle{}, err
		}
		if !withinLimit {
			break
		}
		bundle = candidateBundle

		excerpts := rankedContextExcerpts(source, candidate, result, lexical)
		for _, excerpt := range excerpts {
			fitted, full, ok, fitErr := fitContextExcerpt(bundle, len(bundle.Related)-1, excerpt, options.MaxBytes)
			if fitErr != nil {
				return ContextBundle{}, fitErr
			}
			if !ok {
				budgetExhausted = true
				break
			}
			bundle = fitted
			if !full {
				budgetExhausted = true
				break
			}
		}
	}

	markdown, err := renderContextMarkdown(bundle)
	if err != nil {
		return ContextBundle{}, err
	}
	bundle.Manifest = buildContextManifest(bundle, len(markdown))
	return bundle, nil
}

// RenderContextMarkdown renders the canonical agent-facing Markdown packet.
// The returned byte slice always ends in a newline and its manifest byte count
// includes that newline.
func RenderContextMarkdown(bundle ContextBundle) ([]byte, error) {
	data, err := renderContextMarkdown(bundle)
	if err != nil {
		return nil, err
	}
	if bundle.Manifest.MaxBytes > 0 && len(data) > bundle.Manifest.MaxBytes {
		return nil, fmt.Errorf(
			"recall: rendered Markdown is %d bytes; limit is %d",
			len(data),
			bundle.Manifest.MaxBytes,
		)
	}
	return data, nil
}

// RenderContextJSON renders indented deterministic JSON with HTML escaping
// disabled so source and excerpt Markdown remain directly inspectable.
func RenderContextJSON(bundle ContextBundle) ([]byte, error) {
	data, err := renderContextJSON(bundle)
	if err != nil {
		return nil, err
	}
	if bundle.Manifest.MaxBytes > 0 && len(data) > bundle.Manifest.MaxBytes {
		return nil, fmt.Errorf(
			"recall: rendered JSON is %d bytes; limit is %d",
			len(data),
			bundle.Manifest.MaxBytes,
		)
	}
	return data, nil
}

func validateContextDocument(role string, document Document) error {
	if document.Path == "" {
		return fmt.Errorf("recall: context %s document has empty path", role)
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "path", value: document.Path},
		{label: "title", value: document.Title},
		{label: "body", value: document.Body},
	} {
		if err := validateContextString(role+" "+field.label, field.value); err != nil {
			return err
		}
	}
	for index, metadata := range document.Metadata {
		if err := validateContextString(fmt.Sprintf("%s metadata %d property", role, index), metadata.Property); err != nil {
			return err
		}
		if err := validateContextString(fmt.Sprintf("%s metadata %d value", role, index), metadata.Value); err != nil {
			return err
		}
	}
	return nil
}

func validateContextString(label, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("recall: context %s is not valid UTF-8", label)
	}
	return nil
}

func contextDocumentIndex(source Document, documents []Document) (map[string]Document, []Document, error) {
	documents = append([]Document(nil), documents...)
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})

	byPath := make(map[string]Document, len(documents)+1)
	for index, document := range documents {
		if document.Path == "" {
			return nil, nil, fmt.Errorf("recall: context document %d has empty path", index)
		}
		if _, exists := byPath[document.Path]; exists {
			return nil, nil, fmt.Errorf("recall: context duplicate document path %q", document.Path)
		}
		byPath[document.Path] = document
	}
	if existing, ok := byPath[source.Path]; ok {
		if existing.Title != source.Title || existing.Body != source.Body {
			return nil, nil, fmt.Errorf("recall: context source %q does not match corpus document", source.Path)
		}
	} else {
		byPath[source.Path] = source
		documents = append(documents, source)
		sort.SliceStable(documents, func(i, j int) bool {
			return documents[i].Path < documents[j].Path
		})
	}
	return byPath, documents, nil
}

func contextMetadata(metadata []MetadataValue) []ContextMetadataValue {
	values := make([]ContextMetadataValue, 0, len(metadata))
	seen := make(map[ContextMetadataValue]struct{}, len(metadata))
	for _, value := range metadata {
		converted := ContextMetadataValue{Property: value.Property, Value: value.Value}
		if _, exists := seen[converted]; exists {
			continue
		}
		seen[converted] = struct{}{}
		values = append(values, converted)
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Property != values[j].Property {
			return values[i].Property < values[j].Property
		}
		return values[i].Value < values[j].Value
	})
	return values
}

func contextRelatedResult(rank int, result Result, document Document) (ContextRelatedResult, error) {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "result path", value: result.Path},
		{label: "result title", value: result.Title},
		{label: "relationship", value: string(result.Relationship)},
		{label: "bridge path", value: result.BridgePath},
		{label: "bridge title", value: result.BridgeTitle},
	} {
		if err := validateContextString(field.label, field.value); err != nil {
			return ContextRelatedResult{}, err
		}
	}
	if math.IsNaN(result.Score) || math.IsInf(result.Score, 0) {
		return ContextRelatedResult{}, fmt.Errorf("recall: context result %q has invalid score %v", result.Path, result.Score)
	}
	reasons := make([]ContextReason, len(result.Reasons))
	for index, reason := range result.Reasons {
		if math.IsNaN(reason.Contribution) || math.IsInf(reason.Contribution, 0) {
			return ContextRelatedResult{}, fmt.Errorf(
				"recall: context result %q reason %d has invalid contribution %v",
				result.Path,
				index,
				reason.Contribution,
			)
		}
		if err := validateContextString("reason detail", reason.Detail); err != nil {
			return ContextRelatedResult{}, err
		}
		if err := validateContextString("reason kind", string(reason.Kind)); err != nil {
			return ContextRelatedResult{}, err
		}
		reasons[index] = ContextReason{
			Kind:         reason.Kind,
			Contribution: reason.Contribution,
			Detail:       reason.Detail,
		}
	}

	identity := ContextNoteIdentity{Path: result.Path, Title: result.Title}
	if identity.Title == "" {
		identity.Title = document.Title
	}
	related := ContextRelatedResult{
		Rank:         rank,
		Identity:     identity,
		Score:        result.Score,
		Relationship: result.Relationship,
		Metadata:     contextMetadata(document.Metadata),
		Reasons:      reasons,
		Excerpts:     make([]ContextExcerpt, 0, MaxExcerptsPerRelatedNote),
	}
	if result.BridgePath != "" || result.BridgeTitle != "" {
		related.Bridge = &ContextNoteIdentity{Path: result.BridgePath, Title: result.BridgeTitle}
	}
	return related, nil
}

type scoredContextBlock struct {
	index            int
	score            float64
	distinctiveTerms []string
}

func rankedContextExcerpts(
	source Document,
	candidate Document,
	result Result,
	lexical *LexicalIndex,
) []ContextExcerpt {
	match, _ := lexical.Compare(source.Path, candidate.Path)
	distinctiveWeights := make(map[string]float64, len(match.Terms))
	for _, term := range match.Terms {
		distinctiveWeights[term.Term] = 1 + term.Contribution
	}

	reasonWeights := make(map[string]float64)
	for _, reason := range result.Reasons {
		weight := 0.25 + max(reason.Contribution, 0)
		for _, term := range Tokenize(reason.Detail) {
			if weight > reasonWeights[term] {
				reasonWeights[term] = weight
			}
		}
	}

	scored := make([]scoredContextBlock, 0, len(candidate.Blocks))
	for index, block := range candidate.Blocks {
		sourceBlock, ok := candidate.BlockSource(index)
		if !ok || sourceBlock == "" || !utf8.ValidString(sourceBlock) {
			continue
		}
		blockTerms := make(map[string]struct{})
		for _, term := range Tokenize(block.Text) {
			blockTerms[term] = struct{}{}
		}
		blockScore := 0.0
		matchedDistinctive := make([]string, 0)
		for _, term := range match.Terms {
			if _, ok := blockTerms[term.Term]; !ok {
				continue
			}
			blockScore += distinctiveWeights[term.Term]
			matchedDistinctive = append(matchedDistinctive, term.Term)
		}
		for _, term := range sortedFloatKeys(reasonWeights) {
			if _, ok := blockTerms[term]; ok {
				blockScore += reasonWeights[term]
			}
		}
		scored = append(scored, scoredContextBlock{
			index:            index,
			score:            blockScore,
			distinctiveTerms: matchedDistinctive,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		left := candidate.Blocks[scored[i].index].Location
		right := candidate.Blocks[scored[j].index].Location
		if left.Start != right.Start {
			return left.Start < right.Start
		}
		return scored[i].index < scored[j].index
	})

	reasons := make([]ContextReason, len(result.Reasons))
	for index, reason := range result.Reasons {
		reasons[index] = ContextReason{
			Kind:         reason.Kind,
			Contribution: reason.Contribution,
			Detail:       reason.Detail,
		}
	}
	excerpts := make([]ContextExcerpt, 0, min(MaxExcerptsPerRelatedNote, len(scored)))
	for _, selected := range scored[:min(MaxExcerptsPerRelatedNote, len(scored))] {
		block := candidate.Blocks[selected.index]
		sourceBlock, _ := candidate.BlockSource(selected.index)
		markdown, truncated := truncateContextRunes(sourceBlock, MaxExcerptRunes)
		if markdown == candidate.Body {
			markdown, truncated = excludeWholeRelatedBody(markdown)
		}
		if markdown == "" {
			continue
		}
		excerpt := ContextExcerpt{
			Path:             candidate.Path,
			Title:            candidate.Title,
			StartLine:        block.Location.StartLine,
			EndLine:          block.Location.StartLine + strings.Count(markdown, "\n"),
			Markdown:         markdown,
			Truncated:        truncated,
			DistinctiveTerms: append([]string(nil), selected.distinctiveTerms...),
			Reasons:          append([]ContextReason(nil), reasons...),
		}
		excerpt.DistinctiveTerms = presentContextTerms(excerpt.Markdown, excerpt.DistinctiveTerms)
		excerpts = append(excerpts, excerpt)
	}
	return excerpts
}

func truncateContextRunes(value string, maximum int) (string, bool) {
	if maximum < 0 {
		maximum = 0
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value, false
	}
	if maximum == 0 {
		return "", true
	}
	count := 0
	for index := range value {
		if count == maximum {
			return value[:index], true
		}
		count++
	}
	return value, false
}

func excludeWholeRelatedBody(value string) (string, bool) {
	runes := utf8.RuneCountInString(value)
	if runes <= 1 {
		return "", true
	}
	truncated, _ := truncateContextRunes(value, runes-1)
	return truncated, true
}

func presentContextTerms(markdown string, terms []string) []string {
	present := make(map[string]struct{})
	for _, term := range Tokenize(markdown) {
		present[term] = struct{}{}
	}
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := present[term]; ok {
			filtered = append(filtered, term)
		}
	}
	return filtered
}

func appendContextRelated(bundle ContextBundle, related ContextRelatedResult) ContextBundle {
	bundle.Related = append(append([]ContextRelatedResult(nil), bundle.Related...), related)
	return bundle
}

func appendContextExcerpt(bundle ContextBundle, relatedIndex int, excerpt ContextExcerpt) ContextBundle {
	bundle.Related = append([]ContextRelatedResult(nil), bundle.Related...)
	related := bundle.Related[relatedIndex]
	related.Excerpts = append(append([]ContextExcerpt(nil), related.Excerpts...), excerpt)
	bundle.Related[relatedIndex] = related
	return bundle
}

func fitContextExcerpt(
	bundle ContextBundle,
	relatedIndex int,
	excerpt ContextExcerpt,
	maxBytes int,
) (ContextBundle, bool, bool, error) {
	fullBundle := appendContextExcerpt(bundle, relatedIndex, excerpt)
	withinLimit, err := contextBundleWithinLimit(fullBundle, maxBytes)
	if err != nil {
		return ContextBundle{}, false, false, err
	}
	if withinLimit {
		return fullBundle, true, true, nil
	}

	runeCount := utf8.RuneCountInString(excerpt.Markdown)
	low, high := 1, runeCount-1
	best := 0
	for low <= high {
		middle := low + (high-low)/2
		partial := contextExcerptPrefix(excerpt, middle)
		partialBundle := appendContextExcerpt(bundle, relatedIndex, partial)
		fits, fitErr := contextBundleWithinLimit(partialBundle, maxBytes)
		if fitErr != nil {
			return ContextBundle{}, false, false, fitErr
		}
		if fits {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best == 0 {
		return bundle, false, false, nil
	}
	partial := contextExcerptPrefix(excerpt, best)
	partialBundle := appendContextExcerpt(bundle, relatedIndex, partial)
	return partialBundle, false, true, nil
}

func contextExcerptPrefix(excerpt ContextExcerpt, runes int) ContextExcerpt {
	prefix, truncated := truncateContextRunes(excerpt.Markdown, runes)
	excerpt.Markdown = prefix
	excerpt.Truncated = excerpt.Truncated || truncated
	excerpt.EndLine = excerpt.StartLine + strings.Count(prefix, "\n")
	excerpt.DistinctiveTerms = presentContextTerms(prefix, excerpt.DistinctiveTerms)
	return excerpt
}

func contextBundleWithinLimit(bundle ContextBundle, maxBytes int) (bool, error) {
	markdown, err := renderContextMarkdown(bundle)
	if err != nil {
		return false, err
	}
	if len(markdown) > maxBytes {
		return false, nil
	}
	jsonData, err := renderContextJSON(bundle)
	if err != nil {
		return false, err
	}
	return len(jsonData) <= maxBytes, nil
}

func buildContextManifest(bundle ContextBundle, byteCount int) ContextManifest {
	excerpts := make([]ContextManifestExcerpt, 0)
	for _, related := range bundle.Related {
		for _, excerpt := range related.Excerpts {
			excerpts = append(excerpts, ContextManifestExcerpt{
				Path:      excerpt.Path,
				StartLine: excerpt.StartLine,
				EndLine:   excerpt.EndLine,
			})
		}
	}
	return ContextManifest{
		SourcePath: bundle.Source.Identity.Path,
		Excerpts:   excerpts,
		MaxBytes:   bundle.Manifest.MaxBytes,
		ByteCount:  byteCount,
	}
}

func renderContextMarkdown(bundle ContextBundle) ([]byte, error) {
	return renderContextFixedPoint(bundle, func(current ContextBundle) ([]byte, error) {
		var output strings.Builder
		output.WriteString("# Research Context\n\n")
		output.WriteString("## Purpose\n\n")
		output.WriteString("- Name: ")
		output.WriteString(strconv.Quote(current.Purpose.Name))
		output.WriteString("\n\n")
		if current.Purpose.Instructions == "" {
			output.WriteString("(none)\n\n")
		} else {
			output.WriteString(current.Purpose.Instructions)
			if !strings.HasSuffix(current.Purpose.Instructions, "\n") {
				output.WriteByte('\n')
			}
			output.WriteByte('\n')
		}

		output.WriteString("## Manifest\n\n")
		output.WriteString("- Source: ")
		output.WriteString(strconv.Quote(current.Manifest.SourcePath))
		output.WriteByte('\n')
		output.WriteString("- Excerpts:\n")
		if len(current.Manifest.Excerpts) == 0 {
			output.WriteString("  - (none)\n")
		} else {
			for _, excerpt := range current.Manifest.Excerpts {
				fmt.Fprintf(
					&output,
					"  - %s (lines %d-%d)\n",
					strconv.Quote(excerpt.Path),
					excerpt.StartLine,
					excerpt.EndLine,
				)
			}
		}
		fmt.Fprintf(&output, "- Maximum bytes: %d\n", current.Manifest.MaxBytes)
		fmt.Fprintf(&output, "- Byte count: %d\n\n", current.Manifest.ByteCount)

		output.WriteString("## Source\n\n")
		writeContextIdentity(&output, current.Source.Identity)
		writeContextMetadata(&output, current.Source.Metadata)
		output.WriteString("### Complete body\n\n")
		output.WriteString("<!-- BEGIN SOURCE BODY -->\n")
		output.WriteString(current.Source.Body)
		if !strings.HasSuffix(current.Source.Body, "\n") {
			output.WriteByte('\n')
		}
		output.WriteString("<!-- END SOURCE BODY -->\n\n")

		output.WriteString("## Related Notes\n\n")
		if len(current.Related) == 0 {
			output.WriteString("(none)\n")
		}
		for _, related := range current.Related {
			fmt.Fprintf(&output, "### %d. %s\n\n", related.Rank, strconv.Quote(related.Identity.Title))
			writeContextIdentity(&output, related.Identity)
			fmt.Fprintf(&output, "- Score: %s\n", strconv.FormatFloat(related.Score, 'g', -1, 64))
			relationship := string(related.Relationship)
			if relationship == "" {
				relationship = "none"
			}
			output.WriteString("- Relationship: ")
			output.WriteString(strconv.Quote(relationship))
			output.WriteByte('\n')
			if related.Bridge != nil {
				output.WriteString("- Bridge path: ")
				output.WriteString(strconv.Quote(related.Bridge.Path))
				output.WriteByte('\n')
				output.WriteString("- Bridge title: ")
				output.WriteString(strconv.Quote(related.Bridge.Title))
				output.WriteByte('\n')
			}
			writeContextMetadata(&output, related.Metadata)
			writeContextReasons(&output, related.Reasons)
			if len(related.Excerpts) == 0 {
				output.WriteString("#### Excerpts\n\n(none)\n\n")
				continue
			}
			for excerptIndex, excerpt := range related.Excerpts {
				fmt.Fprintf(&output, "#### Excerpt %d\n\n", excerptIndex+1)
				output.WriteString("- Path: ")
				output.WriteString(strconv.Quote(excerpt.Path))
				output.WriteByte('\n')
				fmt.Fprintf(&output, "- Lines: %d-%d\n", excerpt.StartLine, excerpt.EndLine)
				fmt.Fprintf(&output, "- Truncated: %t\n", excerpt.Truncated)
				output.WriteString("- Distinctive terms: ")
				if len(excerpt.DistinctiveTerms) == 0 {
					output.WriteString("(none)\n")
				} else {
					quoted := make([]string, len(excerpt.DistinctiveTerms))
					for index, term := range excerpt.DistinctiveTerms {
						quoted[index] = strconv.Quote(term)
					}
					output.WriteString(strings.Join(quoted, ", "))
					output.WriteByte('\n')
				}
				writeContextReasons(&output, excerpt.Reasons)
				output.WriteString("\n<!-- BEGIN RELATED EXCERPT -->\n")
				output.WriteString(excerpt.Markdown)
				if !strings.HasSuffix(excerpt.Markdown, "\n") {
					output.WriteByte('\n')
				}
				output.WriteString("<!-- END RELATED EXCERPT -->\n\n")
			}
		}
		return []byte(output.String()), nil
	})
}

func writeContextIdentity(output *strings.Builder, identity ContextNoteIdentity) {
	output.WriteString("- Path: ")
	output.WriteString(strconv.Quote(identity.Path))
	output.WriteByte('\n')
	output.WriteString("- Title: ")
	output.WriteString(strconv.Quote(identity.Title))
	output.WriteByte('\n')
}

func writeContextMetadata(output *strings.Builder, metadata []ContextMetadataValue) {
	output.WriteString("- Metadata:")
	if len(metadata) == 0 {
		output.WriteString(" (none)\n")
		return
	}
	output.WriteByte('\n')
	for _, value := range metadata {
		output.WriteString("  - ")
		output.WriteString(strconv.Quote(value.Property))
		output.WriteString(" = ")
		output.WriteString(strconv.Quote(value.Value))
		output.WriteByte('\n')
	}
}

func writeContextReasons(output *strings.Builder, reasons []ContextReason) {
	output.WriteString("- Reasons:")
	if len(reasons) == 0 {
		output.WriteString(" (none)\n")
		return
	}
	output.WriteByte('\n')
	for _, reason := range reasons {
		fmt.Fprintf(
			output,
			"  - %s (%s): %s\n",
			reason.Kind,
			strconv.FormatFloat(reason.Contribution, 'g', -1, 64),
			strconv.Quote(reason.Detail),
		)
	}
}

func renderContextJSON(bundle ContextBundle) ([]byte, error) {
	return renderContextFixedPoint(bundle, func(current ContextBundle) ([]byte, error) {
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(current); err != nil {
			return nil, fmt.Errorf("recall: render context JSON: %w", err)
		}
		return output.Bytes(), nil
	})
}

func renderContextFixedPoint(
	bundle ContextBundle,
	render func(ContextBundle) ([]byte, error),
) ([]byte, error) {
	byteCount := 0
	for range 16 {
		current := bundle
		current.Manifest = buildContextManifest(current, byteCount)
		data, err := render(current)
		if err != nil {
			return nil, err
		}
		if len(data) == byteCount {
			return data, nil
		}
		byteCount = len(data)
	}
	return nil, errors.New("recall: context byte count did not stabilize")
}
