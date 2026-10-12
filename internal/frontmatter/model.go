// Package frontmatter parses and rewrites YAML frontmatter at the head of
// markdown notes. It guarantees a stable, alphabetically sorted output and
// auto-wraps wikilink-typed properties so callers can mutate notes without
// worrying about layout drift.
package frontmatter

import (
	"strings"
	"time"
)

// Document is a parsed markdown note: optional frontmatter plus the body that
// follows the closing `---` line. Body is preserved verbatim (no trimming) so
// that Marshal can reproduce the original byte stream when no fields are
// mutated.
type Document struct {
	Front                 Map
	Body                  string
	hasBOM                bool
	bodyStartsWithFileBOM bool
	frontmatterLineEnding string
}

// Map is an ordered map of frontmatter keys to values. Insertion order is
// preserved during parse for introspection; Marshal emits keys in
// alphabetical order regardless of insertion order.
//
// Values use Go-native types after parse: string, int, float64, bool,
// time.Time, []any (lists), or map[string]any (nested mappings). Missing
// scalars are represented as nil.
type Map struct {
	keys []string
	vals map[string]any
	meta map[string]valueMeta
}

type valueMeta struct {
	timestampDateOnly bool
}

// NewMap returns an empty Map ready for use.
func NewMap() Map {
	return Map{vals: map[string]any{}, meta: map[string]valueMeta{}}
}

// FormatTimestamp renders t as a date string ("2006-01-02") when the value was
// parsed from a bare YAML date scalar (midnight UTC, no time-of-day component),
// and as RFC3339Nano otherwise. This heuristic is the only way to distinguish a
// YAML date from a full timestamp because yaml.v3 surfaces both as time.Time.
func FormatTimestamp(t time.Time) string {
	if isMidnightUTC(t) {
		return t.Format("2006-01-02")
	}
	return t.Format(time.RFC3339Nano)
}

// isMidnightUTC reports whether t is indistinguishable from a bare YAML date.
// It is the fallback used wherever no recorded spelling says which of the two a
// time value was written as.
func isMidnightUTC(t time.Time) bool {
	return t.Location() == time.UTC &&
		t.Hour() == 0 &&
		t.Minute() == 0 &&
		t.Second() == 0 &&
		t.Nanosecond() == 0
}

// Len reports the number of entries.
func (m *Map) Len() int { return len(m.keys) }

// IsEmpty reports whether the map has no entries.
func (m *Map) IsEmpty() bool { return len(m.keys) == 0 }

// Keys returns the keys in insertion order. The returned slice is a copy.
func (m *Map) Keys() []string {
	out := make([]string, len(m.keys))
	copy(out, m.keys)
	return out
}

// Has reports whether the given key is present.
func (m *Map) Has(k string) bool {
	if m.vals == nil {
		return false
	}
	_, ok := m.vals[k]
	return ok
}

// Get returns the raw value for k and whether it was present.
func (m *Map) Get(k string) (any, bool) {
	if m.vals == nil {
		return nil, false
	}
	v, ok := m.vals[k]
	return v, ok
}

// Set assigns v to key k, appending to the key order if k is new.
func (m *Map) Set(k string, v any) {
	if m.vals == nil {
		m.vals = map[string]any{}
	}
	m.ensureKeyOrder(k)
	m.clearMetaPrefix(k)
	m.vals[k] = v
}

// Unset removes key k if present. No-op if k is unknown.
func (m *Map) Unset(k string) {
	if m.vals == nil {
		return
	}
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	m.clearMetaPrefix(k)
	m.removeKeyOrder(k)
}

// GetString returns the value at k as a string when its underlying type is
// string. The second return value reports presence-and-typed-match.
func (m *Map) GetString(k string) (string, bool) {
	v, ok := m.Get(k)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// GetList returns the value at k as a list. Scalar values are not promoted;
// callers that want either-or semantics should fall back to GetString.
func (m *Map) GetList(k string) ([]any, bool) {
	v, ok := m.Get(k)
	if !ok {
		return nil, false
	}
	return asAnyList(v)
}

// GetTime returns the value at k as a time.Time when it parsed as a YAML
// timestamp.
func (m *Map) GetTime(k string) (time.Time, bool) {
	v, ok := m.Get(k)
	if !ok {
		return time.Time{}, false
	}
	t, ok := v.(time.Time)
	return t, ok
}

// TimeIsDateOnly reports whether the time-valued property at k should be treated
// as a YAML date scalar instead of a full timestamp. Parsed values consult the
// preserved timestamp metadata; values set programmatically fall back to the
// same midnight-UTC heuristic used during marshal.
func (m *Map) TimeIsDateOnly(k string) bool {
	t, ok := m.GetTime(k)
	if !ok {
		return false
	}
	if m != nil && m.meta != nil {
		if meta, ok := m.meta[metaPathKey([]string{metaMapPart(k)})]; ok {
			return meta.timestampDateOnly
		}
	}
	return isMidnightUTC(t)
}

// ListTimeIsDateOnly reports whether the time-valued element at index of the
// list stored at k should be treated as a YAML date scalar. It is
// TimeIsDateOnly for one list element.
func (m *Map) ListTimeIsDateOnly(k string, index int) bool {
	t, ok := m.listTime(k, index)
	if !ok {
		return false
	}
	if m != nil && m.meta != nil {
		if meta, ok := m.meta[metaPathKey([]string{metaMapPart(k), metaIndexPart(index)})]; ok {
			return meta.timestampDateOnly
		}
	}
	return isMidnightUTC(t)
}

// SetTimeSpelling records how the time value stored at k must be serialized: a
// bare YYYY-MM-DD date when dateOnly is true, an RFC3339 timestamp otherwise.
// Set cannot infer this, because yaml.v3 surfaces a date and a midnight-UTC
// timestamp as the same time.Time, so a caller writing a typed datetime must
// declare it or the value falls back to the date spelling. Calling this for a
// key that does not hold a time is a no-op.
func (m *Map) SetTimeSpelling(k string, dateOnly bool) {
	if _, ok := m.GetTime(k); !ok {
		return
	}
	m.recordTimeSpelling([]string{metaMapPart(k)}, dateOnly)
}

// SetListTimeSpelling records the serialized spelling of the time value at
// index of the list stored at k. It is SetTimeSpelling for one list element.
func (m *Map) SetListTimeSpelling(k string, index int, dateOnly bool) {
	if _, ok := m.listTime(k, index); !ok {
		return
	}
	m.recordTimeSpelling([]string{metaMapPart(k), metaIndexPart(index)}, dateOnly)
}

func (m *Map) listTime(k string, index int) (time.Time, bool) {
	list, ok := m.GetList(k)
	if !ok || index < 0 || index >= len(list) {
		return time.Time{}, false
	}
	t, ok := list[index].(time.Time)
	return t, ok
}

func (m *Map) recordTimeSpelling(path []string, dateOnly bool) {
	if m.meta == nil {
		m.meta = map[string]valueMeta{}
	}
	m.meta[metaPathKey(path)] = valueMeta{timestampDateOnly: dateOnly}
}

// GetWikilinkList returns wikilink-typed values at k with `[[...]]` stripped.
// A scalar string is treated as a single-element list. Non-string items are
// skipped. A missing key returns (nil, false); an empty/null value returns
// (empty-slice, true).
func (m *Map) GetWikilinkList(k string) ([]string, bool) {
	v, ok := m.Get(k)
	if !ok {
		return nil, false
	}
	switch vv := v.(type) {
	case nil:
		return []string{}, true
	case string:
		return []string{unwrapWikilink(vv)}, true
	case []string:
		out := make([]string, len(vv))
		for i, item := range vv {
			out[i] = unwrapWikilink(item)
		}
		return out, true
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, unwrapWikilink(s))
			}
		}
		return out, true
	}
	return nil, false
}

func asAnyList(v any) ([]any, bool) {
	switch vv := v.(type) {
	case []any:
		return vv, true
	case []string:
		out := make([]any, len(vv))
		for i, item := range vv {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

// unwrapWikilink returns the inner text of a `[[X]]` wikilink, or s unchanged
// if it is not a wikilink.
func unwrapWikilink(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") && len(s) >= 4 {
		return s[2 : len(s)-2]
	}
	return s
}

// wrapWikilink returns `[[s]]` unless s is already wrapped, partially wrapped,
// empty, or otherwise contains `[[` / `]]` brackets anywhere. The defensive
// check guards against producing visibly malformed values like `[[[[X]]` for a
// stored value of `[[X` (prefix only) or `[[X]] B` (wrapped + trailing text):
// in either case the safer behaviour is to leave the value untouched rather
// than emit triple/quadruple-bracket strings that no consumer can parse.
func wrapWikilink(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, "[[") || strings.Contains(s, "]]") {
		return s
	}
	return "[[" + s + "]]"
}
