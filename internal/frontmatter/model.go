// Package frontmatter parses and rewrites YAML frontmatter at the head of
// markdown notes. It guarantees a stable, alphabetically sorted output and
// auto-wraps wikilink-typed properties so callers can mutate notes without
// worrying about layout drift.
package frontmatter

import (
	"strconv"
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
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 && t.Location() == time.UTC {
		return t.Format("2006-01-02")
	}
	return t.Format(time.RFC3339Nano)
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
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
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
	for i, key := range m.keys {
		if key == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
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
	lst, ok := v.([]any)
	return lst, ok
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

func (m *Map) setParsed(k string, v any) {
	if m.vals == nil {
		m.vals = map[string]any{}
	}
	if m.meta == nil {
		m.meta = map[string]valueMeta{}
	}
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *Map) clearMetaPrefix(k string) {
	clearMetaPrefix(m.meta, []string{metaMapPart(k)})
}

// clearListIndexMeta removes meta entries for a single list item at the given
// index under key k. Used after a value at that index is replaced so that the
// new value gets fresh formatting metadata while siblings keep theirs.
func (m *Map) clearListIndexMeta(k string, index int) {
	if len(m.meta) == 0 {
		return
	}
	clearMetaPrefix(m.meta, []string{metaMapPart(k), metaIndexPart(index)})
}

// remapListIndexMeta rewrites list-index meta entries under key k so they
// match the post-mutation indices described by keepIndexMap (old -> new).
// Meta for indices not present in keepIndexMap is dropped. This is used after
// list removals so kept items retain their timestamp-format metadata at their
// new positions.
func (m *Map) remapListIndexMeta(k string, keepIndexMap map[int]int) {
	if len(m.meta) == 0 {
		return
	}
	keyPrefix := metaPathKey([]string{metaMapPart(k)}) + "\x00"
	// Collect updates first so we don't mutate the map while iterating.
	type entry struct {
		newKey string
		val    valueMeta
	}
	var updates []entry
	for mk, mv := range m.meta {
		if !strings.HasPrefix(mk, keyPrefix) {
			continue
		}
		tail := mk[len(keyPrefix):]
		// Tail starts with an index part like "iN" optionally followed by
		// further "\x00..." segments for deeper nesting.
		idxStr, rest := splitMetaTail(tail)
		oldIdx, ok := parseIndexPart(idxStr)
		if !ok {
			// Not a list index segment — leave as-is to avoid breaking other
			// nested layouts.
			continue
		}
		// Remove the existing entry regardless; we'll re-insert if kept.
		delete(m.meta, mk)
		newIdx, keep := keepIndexMap[oldIdx]
		if !keep {
			continue
		}
		newKey := keyPrefix + metaIndexPart(newIdx)
		if rest != "" {
			newKey += "\x00" + rest
		}
		updates = append(updates, entry{newKey: newKey, val: mv})
	}
	for _, u := range updates {
		m.meta[u.newKey] = u.val
	}
}

// splitMetaTail splits a meta key tail on the first NUL byte, returning the
// first segment and the remainder (without the separator). When there is no
// separator, the whole input is returned as the head and rest is empty.
func splitMetaTail(tail string) (head, rest string) {
	if i := strings.IndexByte(tail, '\x00'); i >= 0 {
		return tail[:i], tail[i+1:]
	}
	return tail, ""
}

// parseIndexPart parses a segment of the form "iN" produced by metaIndexPart
// and returns the integer index. ok is false for any segment that doesn't
// match that shape, so non-index segments (e.g. nested map parts) are skipped.
func parseIndexPart(s string) (int, bool) {
	if len(s) < 2 || s[0] != 'i' {
		return 0, false
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (m *Map) renameMetaPrefix(oldK, newK string) {
	if len(m.meta) == 0 {
		return
	}
	oldPrefix := metaPathKey([]string{metaMapPart(oldK)})
	newPrefix := metaPathKey([]string{metaMapPart(newK)})
	for k, v := range m.meta {
		if !isMetaPrefixMatch(k, oldPrefix) {
			continue
		}
		delete(m.meta, k)
		m.meta[newPrefix+strings.TrimPrefix(k, oldPrefix)] = v
	}
}

func clearMetaPrefix(meta map[string]valueMeta, path []string) {
	if len(meta) == 0 {
		return
	}
	prefix := metaPathKey(path)
	for k := range meta {
		if isMetaPrefixMatch(k, prefix) {
			delete(meta, k)
		}
	}
}

func isMetaPrefixMatch(k, prefix string) bool {
	return k == prefix || strings.HasPrefix(k, prefix+"\x00")
}

func metaPathKey(path []string) string {
	return strings.Join(path, "\x00")
}

func metaMapPart(k string) string {
	return "m" + strconv.Itoa(len(k)) + ":" + k
}

func metaIndexPart(i int) string {
	return "i" + strconv.Itoa(i)
}

func appendMetaPath(path []string, part string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = part
	return next
}
