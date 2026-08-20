package recall

import (
	"errors"
	"fmt"
)

const (
	// MaxCuratedSupporters bounds how many supporting notes one curated packet
	// may carry.
	MaxCuratedSupporters = 10
	// MaxCuratedBundleBytes is the hard ceiling a curated packet may grow to
	// while making room for its mandatory content.
	MaxCuratedBundleBytes = 1024 * 1024
	// curatedLimitIterations bounds the search for a byte limit that covers its
	// own decimal representation in the rendered manifest.
	curatedLimitIterations = 8
)

var (
	// ErrCuratedSupporterLimit identifies a selection larger than
	// MaxCuratedSupporters.
	ErrCuratedSupporterLimit = errors.New("recall: curated context supporter limit exceeded")
	// ErrCuratedAnchorSupporter identifies a supporter that repeats the anchor.
	ErrCuratedAnchorSupporter = errors.New("recall: curated context supporter repeats the anchor")
	// ErrCuratedDuplicateSupporter identifies a supporter selected twice.
	ErrCuratedDuplicateSupporter = errors.New("recall: curated context duplicate supporter")
	// ErrCuratedSupporterMissing identifies a supporter absent from the corpus.
	ErrCuratedSupporterMissing = errors.New("recall: curated context supporter is not in the document corpus")
	// ErrCuratedSupporterEmpty identifies a supporter with no extractable body
	// block, which cannot contribute the exact excerpt curation requires.
	ErrCuratedSupporterEmpty = errors.New("recall: curated context supporter has no extractable body block")
	// ErrCuratedContextTooLarge identifies mandatory curated content that does
	// not fit within MaxCuratedBundleBytes.
	ErrCuratedContextTooLarge = errors.New("recall: curated context exceeds the maximum byte limit")
)

// CuratedBundleOptions controls an explicit-selection packet. MaxBytes is the
// starting budget, not a cap: the builder grows it only as far as the mandatory
// anchor body and one exact excerpt per supporter require, never beyond
// MaxCuratedBundleBytes. The zero value starts at the standard 64 KiB.
type CuratedBundleOptions struct {
	MaxBytes int
	Purpose  ContextPurpose
}

// DefaultCuratedBundleOptions returns the standard 64 KiB starting budget.
func DefaultCuratedBundleOptions() CuratedBundleOptions {
	return CuratedBundleOptions{MaxBytes: DefaultBundleMaxBytes}
}

// BuildCuratedContextBundle creates a deterministic packet from an explicitly
// chosen anchor and ordered supporter set. Unlike BuildContextBundle, which
// admits ranked results only while they fit, every supplied supporter is
// mandatory and contributes at least one exact excerpt; the byte budget grows
// to fit them rather than dropping them. Supporters are used strictly in the
// supplied order and are never reordered by score.
func BuildCuratedContextBundle(
	anchor Document,
	documents []Document,
	supporters []Result,
	options CuratedBundleOptions,
) (ContextBundle, error) {
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultBundleMaxBytes
	}
	if options.MaxBytes < 0 {
		return ContextBundle{}, fmt.Errorf("recall: curated context byte limit must be positive: %d", options.MaxBytes)
	}
	if options.MaxBytes > MaxCuratedBundleBytes {
		return ContextBundle{}, fmt.Errorf(
			"recall: curated context byte limit %d exceeds the %d byte maximum",
			options.MaxBytes,
			MaxCuratedBundleBytes,
		)
	}
	if len(supporters) > MaxCuratedSupporters {
		return ContextBundle{}, fmt.Errorf(
			"%w: %d selected; limit is %d",
			ErrCuratedSupporterLimit,
			len(supporters),
			MaxCuratedSupporters,
		)
	}
	if err := validateContextString("purpose name", options.Purpose.Name); err != nil {
		return ContextBundle{}, err
	}
	if err := validateContextString("purpose instructions", options.Purpose.Instructions); err != nil {
		return ContextBundle{}, err
	}
	if err := validateContextDocument("anchor", anchor); err != nil {
		return ContextBundle{}, err
	}
	if len(anchor.Body) > MaxCuratedBundleBytes {
		return ContextBundle{}, fmt.Errorf(
			"%w: %q is %d bytes; maximum is %d",
			ErrSourceBodyTooLarge,
			anchor.Path,
			len(anchor.Body),
			MaxCuratedBundleBytes,
		)
	}

	documentsByPath, lexicalDocuments, err := contextDocumentIndex(anchor, documents)
	if err != nil {
		return ContextBundle{}, err
	}
	lexical, err := BuildLexicalIndex(lexicalDocuments)
	if err != nil {
		return ContextBundle{}, fmt.Errorf("recall: curated context lexical index: %w", err)
	}

	base := ContextBundle{
		Purpose: options.Purpose,
		Source: ContextSource{
			Identity: ContextNoteIdentity{Path: anchor.Path, Title: anchor.Title},
			Metadata: contextMetadata(anchor.Metadata),
			Body:     anchor.Body,
		},
		Related: make([]ContextRelatedResult, 0),
		Manifest: ContextManifest{
			SourcePath: anchor.Path,
			Excerpts:   make([]ContextManifestExcerpt, 0),
			MaxBytes:   options.MaxBytes,
		},
	}

	mandatory := base
	mandatory.Related = make([]ContextRelatedResult, 0, len(supporters))
	optional := make([][]ContextExcerpt, 0, len(supporters))
	selected := make(map[string]struct{}, len(supporters))
	for index, supporter := range supporters {
		if supporter.Path == anchor.Path {
			return ContextBundle{}, fmt.Errorf("%w: supporter %d is %q", ErrCuratedAnchorSupporter, index, supporter.Path)
		}
		if _, exists := selected[supporter.Path]; exists {
			return ContextBundle{}, fmt.Errorf("%w: %q", ErrCuratedDuplicateSupporter, supporter.Path)
		}
		selected[supporter.Path] = struct{}{}

		candidate, ok := documentsByPath[supporter.Path]
		if !ok {
			return ContextBundle{}, fmt.Errorf("%w: %q", ErrCuratedSupporterMissing, supporter.Path)
		}
		if err := validateContextDocument("supporter", candidate); err != nil {
			return ContextBundle{}, err
		}
		related, err := contextRelatedResult(index+1, supporter, candidate)
		if err != nil {
			return ContextBundle{}, err
		}
		excerpts := rankedContextExcerpts(anchor, candidate, supporter, lexical)
		if len(excerpts) == 0 {
			return ContextBundle{}, fmt.Errorf("%w: %q", ErrCuratedSupporterEmpty, supporter.Path)
		}
		related.Excerpts = append(related.Excerpts, excerpts[0])
		mandatory.Related = append(mandatory.Related, related)
		optional = append(optional, excerpts[1:])
	}

	baseLimit, fits, err := curatedByteLimit(base, options.MaxBytes)
	if err != nil {
		return ContextBundle{}, err
	}
	if !fits {
		return ContextBundle{}, fmt.Errorf(
			"%w: anchor %q and required framing do not fit in %d bytes",
			ErrRequiredContextTooLarge,
			anchor.Path,
			MaxCuratedBundleBytes,
		)
	}
	effective, fits, err := curatedByteLimit(mandatory, baseLimit)
	if err != nil {
		return ContextBundle{}, err
	}
	if !fits {
		return ContextBundle{}, fmt.Errorf(
			"%w: anchor %q and %d mandatory supporter excerpts do not fit in %d bytes",
			ErrCuratedContextTooLarge,
			anchor.Path,
			len(supporters),
			MaxCuratedBundleBytes,
		)
	}

	bundle := mandatory
	bundle.Manifest.MaxBytes = effective
	budgetExhausted := false
	for index := range bundle.Related {
		if budgetExhausted {
			break
		}
		for _, excerpt := range optional[index] {
			fitted, full, ok, fitErr := fitContextExcerpt(bundle, index, excerpt, effective)
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

// curatedByteLimit returns the smallest limit at or above start that both
// serialized forms of bundle fit into. Raising the limit widens its own decimal
// rendering in the manifest, so the search iterates to a fixed point. The
// boolean is false when the bundle cannot fit within MaxCuratedBundleBytes.
func curatedByteLimit(bundle ContextBundle, start int) (int, bool, error) {
	limit := start
	for range curatedLimitIterations {
		if limit > MaxCuratedBundleBytes {
			return 0, false, nil
		}
		trial := bundle
		trial.Manifest.MaxBytes = limit
		size, err := contextBundleSize(trial)
		if err != nil {
			return 0, false, err
		}
		if size <= limit {
			return limit, true, nil
		}
		limit = size
	}
	return 0, false, errors.New("recall: curated context byte limit did not stabilize")
}

func contextBundleSize(bundle ContextBundle) (int, error) {
	markdown, err := renderContextMarkdown(bundle)
	if err != nil {
		return 0, err
	}
	jsonData, err := renderContextJSON(bundle)
	if err != nil {
		return 0, err
	}
	return max(len(markdown), len(jsonData)), nil
}
