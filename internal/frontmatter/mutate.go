package frontmatter

import (
	"errors"
	"reflect"
)

// ErrNotAList is returned by list-mutation methods when the target key exists
// but its value is not a YAML sequence.
var ErrNotAList = errors.New("frontmatter: value is not a list")

var ErrKeyConflict = errors.New("frontmatter: target key already exists")

// ListAdd appends values to the list stored at key k, preserving existing
// order and skipping duplicates. If k is absent, a new list is created
// containing the deduplicated input values. If k is present but its value is
// not a list, ErrNotAList is returned.
//
// Adding an already-present value is a no-op for that value. changed is true
// only if at least one value was actually appended.
//
// Values are compared as-is with reflect.DeepEqual; callers responsible for
// wikilink-typed properties must wrap values into `[[X]]` form before calling
// so that the comparison treats them as equal to existing wrapped entries.
func (m *Map) ListAdd(k string, values ...any) (bool, error) {
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
		m.keys = append(m.keys, k)
		m.vals[k] = uniq
		return len(uniq) > 0, nil
	}
	// A nil-valued key (YAML `tags: ~` / `tags:` / `tags: null`) is treated as
	// an empty list so `+=` can append to it. Without this, common notes that
	// declare an empty-but-present list property could not be mutated by
	// `bn set tags+=foo` and would fail with ErrNotAList.
	if existing == nil {
		existing = []any{}
	}
	list, ok := existing.([]any)
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
	}
	return changed, nil
}

// ListRemove removes values from the list stored at key k, preserving the
// order of remaining elements. If k is absent or a given value is not in the
// list, the call is a no-op for that value. If k is present but its value is
// not a list, ErrNotAList is returned.
//
// If removal empties the list, an empty list is left in place — the key is
// not deleted. Use Unset for key deletion.
//
// Values are compared as-is with reflect.DeepEqual; callers responsible for
// wikilink-typed properties must wrap values into `[[X]]` form before calling.
func (m *Map) ListRemove(k string, values ...any) (bool, error) {
	existing, present := m.Get(k)
	if !present {
		return false, nil
	}
	// A nil-valued key (YAML `tags: ~` / `tags:` / `tags: null`) is treated as
	// an empty list: removal is a silent no-op, mirroring the absent-key path.
	if existing == nil {
		return false, nil
	}
	list, ok := existing.([]any)
	if !ok {
		return false, ErrNotAList
	}
	toRemove := values
	out := make([]any, 0, len(list))
	changed := false
	for _, item := range list {
		if containsAny(toRemove, item) {
			changed = true
			continue
		}
		out = append(out, item)
	}
	if changed {
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
	if list, ok := existing.([]any); ok {
		changed := false
		out := make([]any, len(list))
		for i, item := range list {
			if reflect.DeepEqual(item, from) {
				out[i] = to
				changed = true
				continue
			}
			out[i] = item
		}
		if changed {
			m.vals[k] = out
		}
		return changed, nil
	}
	if reflect.DeepEqual(existing, from) {
		m.vals[k] = to
		return true, nil
	}
	return false, nil
}

func (m *Map) RenameKey(oldK, newK string) (bool, error) {
	if m.vals == nil {
		return false, nil
	}
	if _, ok := m.vals[oldK]; !ok {
		return false, nil
	}
	if _, ok := m.vals[newK]; ok {
		return false, ErrKeyConflict
	}
	m.vals[newK] = m.vals[oldK]
	delete(m.vals, oldK)
	for i, k := range m.keys {
		if k == oldK {
			m.keys[i] = newK
			break
		}
	}
	return true, nil
}

// containsAny reports whether haystack contains an element equal (per
// reflect.DeepEqual) to needle.
func containsAny(haystack []any, needle any) bool {
	for _, h := range haystack {
		if reflect.DeepEqual(h, needle) {
			return true
		}
	}
	return false
}
