package recall

import (
	"errors"
	"fmt"
	"math"
	"sort"
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
		converted := ContextMetadataValue(value)
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
		reasons[index] = ContextReason(reason)
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
