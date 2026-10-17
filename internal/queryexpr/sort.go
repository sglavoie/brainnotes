package queryexpr

import (
	"errors"
	"sort"
	"strings"
)

// The sort key vocabulary. These are surface spellings: they are what a user
// types after --sort and what a saved view stores, so they are stable names
// rather than an internal enum.
//
// SortPath is the default and the only key that needs nothing but the note
// list. The rest name state a caller holds: title and the two date keys read
// frontmatter, backlinks and degree read the link graph, pinned reads the
// pinned list, and relevance reads a recall ranker anchored at RelativeTo.
const (
	SortMatch     = "match"
	SortPath      = "path"
	SortTitle     = "title"
	SortModified  = "modified"
	SortCreated   = "created"
	SortBacklinks = "backlinks"
	SortDegree    = "degree"
	SortPinned    = "pinned"
	SortRelevance = "relevance"
)

// sortKeys is the vocabulary in canonical (sorted) order. Membership is what
// ParseSort validates; nothing here says how to order by a key, because
// queryexpr does not order by any of them beyond path.
var sortKeys = []string{
	SortBacklinks,
	SortCreated,
	SortDegree,
	SortMatch,
	SortModified,
	SortPath,
	SortPinned,
	SortRelevance,
	SortTitle,
}

// SortSpec is a declarative ordering request. It is not expression syntax: the
// grammar has no sort or limit production, so a caller fills these in from
// flags — ParseSort turns a --sort value into Key and Descending, and
// RelativeTo comes from its own flag.
//
// **queryexpr never ranks.** A SortSpec says what ordering was asked for and
// nothing about how to produce it. That is the whole point of the type: ranking
// by relevance means reading the recall sidecar, and keeping that read out of a
// package whose job is compiling text is what keeps recall's read/write
// boundary from spreading. The in-memory evaluator honours the default path
// ordering and refuses every other key rather than silently ignoring it, so a
// caller that asks for an ordering nobody implements finds out.
type SortSpec struct {
	// Key is one of the sort key constants. The empty string means SortPath.
	Key string
	// Search is literal text used by match ordering.
	Search string
	// Descending reverses the ordering.
	Descending bool
	// RelativeTo is the note the relevance ordering is anchored at. It is
	// meaningless for every other key, and ParseSort never sets it: it is a
	// separate flag because it names a note, not an ordering.
	RelativeTo string
}

// String renders the spec back as a --sort value: the key, prefixed with `-`
// when descending. RelativeTo is deliberately not part of it — it is a
// different flag, so folding it in would produce a string ParseSort could not
// read back.
func (s SortSpec) String() string {
	key := s.Key
	if key == "" {
		key = SortPath
	}
	if s.Descending {
		return "-" + key
	}
	return key
}

// SortKeys returns the sort key vocabulary in sorted order, for flag help text
// and error messages. The result is a fresh slice.
func SortKeys() []string {
	out := make([]string, len(sortKeys))
	copy(out, sortKeys)
	return out
}

// ValidSortKey reports whether key names a known ordering. The empty string is
// valid and means the default path ordering.
func ValidSortKey(key string) bool {
	if key == "" {
		return true
	}
	i := sort.SearchStrings(sortKeys, key)
	return i < len(sortKeys) && sortKeys[i] == key
}

// ParseSort parses a --sort value into a SortSpec.
//
// The accepted forms are the key alone (`modified`), the key with a direction
// sign (`-modified`, `+modified`), and the key with a direction suffix
// (`modified:desc`, `modified:asc`). Keys are case-insensitive and surrounding
// space is ignored; an empty value is the default `path` ascending.
//
// Both direction spellings exist because both are habitual — `-modified` reads
// like sort(1) and `modified:desc` reads like the query language — but giving
// the direction twice is an error rather than a precedence rule to remember,
// even when the two agree.
//
// RelativeTo is never set here. `relevance` parses like any other key, and the
// caller binding --relative-to is what decides whether an anchor is required.
func ParseSort(value string) (SortSpec, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return SortSpec{Key: SortPath}, nil
	}

	var spec SortSpec
	signed := false
	switch raw[0] {
	case '-':
		spec.Descending = true
		signed = true
		raw = raw[1:]
	case '+':
		signed = true
		raw = raw[1:]
	}

	if key, suffix, found := strings.Cut(raw, ":"); found {
		if signed {
			return SortSpec{}, errors.New("queryexpr: sort direction given twice in " + quote(strings.TrimSpace(value)))
		}
		switch strings.ToLower(strings.TrimSpace(suffix)) {
		case "desc", "descending":
			spec.Descending = true
		case "asc", "ascending":
		default:
			return SortSpec{}, errors.New("queryexpr: unknown sort direction " + quote(suffix) + "; use \"asc\" or \"desc\"")
		}
		raw = key
	}

	spec.Key = strings.ToLower(strings.TrimSpace(raw))
	if spec.Key == "" {
		return SortSpec{}, errors.New("queryexpr: sort needs a key; valid keys are " + strings.Join(sortKeys, ", "))
	}
	if !ValidSortKey(spec.Key) {
		return SortSpec{}, errors.New("queryexpr: unknown sort key " + quote(spec.Key) + "; valid keys are " + strings.Join(sortKeys, ", "))
	}
	return spec, nil
}
