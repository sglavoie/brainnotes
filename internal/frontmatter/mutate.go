package frontmatter

import (
	"errors"
	"reflect"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// ErrNotAList is returned by list-mutation methods when the target key exists
// but its value is not a YAML sequence.
var ErrNotAList = errors.New("frontmatter: value is not a list")

var ErrKeyConflict = errors.New("frontmatter: target key already exists")

// ListAdd appends values to the list stored at key k, preserving existing
// order and skipping duplicates. If k is absent and values is non-empty, a new
// list is created containing the deduplicated input values. Empty values are a
// no-op. If k is present but its value is not a list, ErrNotAList is returned.
//
// Adding an already-present value is a no-op for that value. changed is true
// only if at least one value was actually appended.
//
// Strings are compared under NFC and other values with reflect.DeepEqual
// (see listItemEqual); callers responsible for wikilink-typed properties must
// wrap values into `[[X]]` form before calling so that the comparison treats them as equal to existing wrapped entries.
func (m *Map) ListAdd(k string, values ...any) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	if m.vals == nil {
		m.vals = map[string]any{}
	}
	existing, present := m.vals[k]
	if !present {
		uniq := make([]any, 0, len(values))
		for _, v := range values {
			if containsAny(uniq, v) {
				continue
			}
			uniq = append(uniq, v)
		}
		m.ensureKeyOrder(k)
		m.vals[k] = uniq
		return len(uniq) > 0, nil
	}
	// A nil-valued key (YAML `tags: ~` / `tags:` / `tags: null`) is treated as
	// an empty list so `+=` can append to it. Without this, common notes that
	// declare an empty-but-present list property could not be mutated by
	// `bn set tags+=foo` and would fail with ErrNotAList.
	wasNil := existing == nil
	if wasNil {
		existing = []any{}
	}
	list, ok := asAnyList(existing)
	if !ok {
		return false, ErrNotAList
	}
	changed := false
	for _, v := range values {
		if containsAny(list, v) {
			continue
		}
		list = append(list, v)
		changed = true
	}
	if changed {
		m.vals[k] = list
		if wasNil {
			m.clearMetaPrefix(k)
		}
	}
	return changed, nil
}

// ListRemove removes values from the list stored at key k, preserving the
// order of remaining elements. If k is absent, values is empty, or a given
// value is not in the list, the call is a no-op for that value. If k is
// present but its value is not a list, ErrNotAList is returned.
//
// If removal empties the list, an empty list is left in place — the key is
// not deleted. Use Unset for key deletion.
//
// Strings are compared under NFC and other values with reflect.DeepEqual
// (see listItemEqual); callers responsible for wikilink-typed properties must
// wrap values into `[[X]]` form before calling.
func (m *Map) ListRemove(k string, values ...any) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	existing, present := m.Get(k)
	if !present {
		return false, nil
	}
	// A nil-valued key (YAML `tags: ~` / `tags:` / `tags: null`) is treated as
	// an empty list: removal is a silent no-op, mirroring the absent-key path.
	if existing == nil {
		return false, nil
	}
	list, ok := asAnyList(existing)
	if !ok {
		return false, ErrNotAList
	}
	toRemove := values
	out := make([]any, 0, len(list))
	// keepIndexMap maps an old index in the source list to its new index in the
	// surviving list. Indices that were removed are not present in the map.
	keepIndexMap := make(map[int]int, len(list))
	changed := false
	for i, item := range list {
		if containsAny(toRemove, item) {
			changed = true
			continue
		}
		keepIndexMap[i] = len(out)
		out = append(out, item)
	}
	if changed {
		// Rewrite the list's index meta so surviving items keep their
		// timestamp-format metadata at the new (shifted) indices. Clearing the
		// whole prefix here would cause kept midnight-UTC datetimes to silently
		// downgrade to date-only on the next Marshal.
		m.remapListIndexMeta(k, keepIndexMap)
		m.vals[k] = out
	}
	return changed, nil
}

// ReplaceValue replaces occurrences of from with to at key k. For scalar
// values the replacement happens when the stored value equals from. For list
// values each matching element is replaced in place, preserving order. If k
// is absent, the call is a no-op. If from == to the call is a no-op.
//
// Values are compared with reflect.DeepEqual on Go-native types. The CLI
// layer is responsible for running user input through the type-coercion
// helper before invoking this method, and for wikilink-wrapping where the
// property's config demands it.
func (m *Map) ReplaceValue(k string, from, to any) (bool, error) {
	if reflect.DeepEqual(from, to) {
		return false, nil
	}
	existing, present := m.Get(k)
	if !present {
		return false, nil
	}
	if list, ok := asAnyList(existing); ok {
		changed := false
		out := make([]any, len(list))
		replacedIdx := make([]int, 0, len(list))
		for i, item := range list {
			if reflect.DeepEqual(item, from) {
				out[i] = to
				replacedIdx = append(replacedIdx, i)
				changed = true
				continue
			}
			out[i] = item
		}
		if changed {
			// Clear meta only for the replaced indices so that surviving list
			// items keep their timestamp-format metadata. Clearing the whole
			// prefix here would cause kept midnight-UTC datetimes elsewhere in
			// the list to silently downgrade to date-only on Marshal.
			for _, i := range replacedIdx {
				m.clearListIndexMeta(k, i)
			}
			m.vals[k] = out
		}
		return changed, nil
	}
	if reflect.DeepEqual(existing, from) {
		m.clearMetaPrefix(k)
		m.vals[k] = to
		return true, nil
	}
	return false, nil
}

func (m *Map) RenameKey(oldK, newK string) (bool, error) {
	if m.vals == nil {
		return false, nil
	}
	if oldK == newK {
		return false, nil
	}
	// An empty newK would silently insert a `""` key, which YAML re-parses as
	// an unnamed property and is not addressable by callers. Reject it so the
	// rename operation can't corrupt the frontmatter.
	if newK == "" {
		return false, errors.New("frontmatter: empty target key")
	}
	if _, ok := m.vals[oldK]; !ok {
		return false, nil
	}
	if _, ok := m.vals[newK]; ok {
		return false, ErrKeyConflict
	}
	m.vals[newK] = m.vals[oldK]
	delete(m.vals, oldK)
	m.renameMetaPrefix(oldK, newK)
	for i, k := range m.keys {
		if k == oldK {
			m.keys[i] = newK
			break
		}
	}
	m.removeKeyOrder(oldK)
	m.ensureKeyOrder(newK)
	return true, nil
}

// containsAny reports whether haystack contains an element equal to needle
// per listItemEqual.
func containsAny(haystack []any, needle any) bool {
	for _, h := range haystack {
		if listItemEqual(h, needle) {
			return true
		}
	}
	return false
}

// listItemEqual is the equality ListAdd and ListRemove use. Two strings are
// equal when their NFC forms are, so a decomposed `café` written by a macOS
// tool and the composed `café` a user types name one list item: without this,
// `tags-=café` would leave the stored item in place and `tags+=café` would
// append a visually identical duplicate. Only the comparison is normalized; a
// retained item keeps its stored bytes. Every other value compares with
// reflect.DeepEqual.
func listItemEqual(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs || textnorm.NFC(as) == textnorm.NFC(bs)
	}
	return reflect.DeepEqual(a, b)
}
