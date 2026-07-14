// Package recall builds deterministic, read-only representations of vault
// notes for contextual retrieval.
package recall

import "time"

// TextKind identifies the role and weight of a searchable text field.
type TextKind string

const (
	TextTitle   TextKind = "title"
	TextHeading TextKind = "heading"
	TextProse   TextKind = "prose"
)

const (
	TitleWeight   = 3.0
	HeadingWeight = 2.0
	ProseWeight   = 1.0
)

// BlockKind identifies the source Markdown construct represented by a block.
// Lists and block quotes produce prose blocks for each contained paragraph;
// tables remain whole so their row and column context is preserved.
type BlockKind string

const (
	BlockHeading BlockKind = "heading"
	BlockProse   BlockKind = "prose"
	BlockTable   BlockKind = "table"
)

// SourceLocation is a half-open byte range in Document.Body. Line numbers are
// one-based and inclusive. Offsets are bytes, not runes, so slicing the source
// reproduces the original Markdown exactly.
type SourceLocation struct {
	Start     int
	End       int
	StartLine int
	EndLine   int
}

// Slice returns the source covered by the location. The boolean is false when
// the location is invalid for source.
func (l SourceLocation) Slice(source string) (string, bool) {
	if l.Start < 0 || l.End < l.Start || l.End > len(source) {
		return "", false
	}
	return source[l.Start:l.End], true
}

// TextField is one weighted input to lexical indexing. BlockIndex is -1 for
// the filename-derived title and otherwise indexes Document.Blocks.
type TextField struct {
	Kind       TextKind
	Text       string
	Weight     float64
	BlockIndex int
}

// MarkdownBlock couples normalized searchable text with its exact source
// span. HeadingLevel is set only for heading blocks.
type MarkdownBlock struct {
	Kind         BlockKind
	Text         string
	Location     SourceLocation
	HeadingLevel int
}

// MetadataValue is a normalized property/value pair. Both fields are
// NFC-normalized, trimmed, and lowercased.
type MetadataValue struct {
	Property string
	Value    string
}

// Document is the read-only corpus representation of one vault note.
// Path is vault-relative and slash-separated. Body is preserved verbatim.
// CreatedAt is zero when the note has no usable created property.
type Document struct {
	Path      string
	Title     string
	Body      string
	Fields    []TextField
	Blocks    []MarkdownBlock
	Metadata  []MetadataValue
	CreatedAt time.Time
}

// BlockSource returns the original Markdown for a block.
func (d Document) BlockSource(index int) (string, bool) {
	if index < 0 || index >= len(d.Blocks) {
		return "", false
	}
	return d.Blocks[index].Location.Slice(d.Body)
}

// ExtractOptions controls note extraction. An empty MetadataProperties slice
// intentionally disables metadata extraction.
type ExtractOptions struct {
	MetadataProperties []string
}
