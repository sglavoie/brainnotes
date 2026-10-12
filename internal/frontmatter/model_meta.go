package frontmatter

import (
	"strconv"
	"strings"
)

func (m *Map) setParsed(k string, v any) {
	if m.vals == nil {
		m.vals = map[string]any{}
	}
	if m.meta == nil {
		m.meta = map[string]valueMeta{}
	}
	m.ensureKeyOrder(k)
	m.vals[k] = v
}

func (m *Map) ensureKeyOrder(k string) {
	out := m.keys[:0]
	seen := false
	for _, existing := range m.keys {
		if existing != k {
			out = append(out, existing)
			continue
		}
		if seen {
			continue
		}
		out = append(out, existing)
		seen = true
	}
	m.keys = out
	if !seen {
		m.keys = append(m.keys, k)
	}
}

func (m *Map) removeKeyOrder(k string) {
	out := m.keys[:0]
	for _, existing := range m.keys {
		if existing == k {
			continue
		}
		out = append(out, existing)
	}
	m.keys = out
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
