package recall

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

// ContextPurpose names the requested research task and provides the exact
// deterministic instructions carried in the bundle. The bundle builders treat
// these strings as data; StandardPurpose owns the shared definitions callers
// present as the standard research purposes.
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
