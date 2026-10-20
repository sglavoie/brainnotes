package properties

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
)

// TypedValue is a property value in request or response transport form. Kind is
// a scalar kind or config.KindList, Text is the canonical scalar spelling, and
// Items carries the elements of a list. Null and unsupported values have no
// spelling a write could reproduce, so they cannot be expressed here and stay
// read-only.
type TypedValue struct {
	Kind  Kind         `json:"kind"`
	Text  string       `json:"text"`
	Items []TypedValue `json:"items,omitempty"`
}

// Decode converts the transport value into the Go-native frontmatter value that
// will be stored. A wikilink decodes to its wrapped `[[target]]` spelling so the
// written note matches what a wikilink-typed property already holds.
func (v TypedValue) Decode() (any, error) {
	if v.Kind != config.KindList {
		return v.decodeScalar()
	}
	items := make([]any, 0, len(v.Items))
	for i, item := range v.Items {
		if item.Kind == config.KindList {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: list item %d is itself a list; lists do not nest", i)
		}
		decoded, err := item.decodeScalar()
		if err != nil {
			return nil, err
		}
		items = append(items, decoded)
	}
	return items, nil
}

// kinds names the kind of every value v writes: one per element for a list, or
// the scalar kind otherwise.
func (v TypedValue) kinds() []Kind {
	if v.Kind != config.KindList {
		return []Kind{v.Kind}
	}
	out := make([]Kind, 0, len(v.Items))
	for _, item := range v.Items {
		out = append(out, item.Kind)
	}
	return out
}

// WriteTyped stores decoded at key and records the timestamp spelling v asks
// for. It reports false when the note already holds that value in that
// spelling, which is the caller's signal that nothing changed.
//
// A typed write must go through this rather than frontmatter.Map.Set: Set drops
// the spelling metadata, and a midnight-UTC time with no spelling serializes as
// a bare date, so a datetime would silently be written as a date.
func WriteTyped(front *frontmatter.Map, key string, v TypedValue, decoded any) bool {
	if front == nil {
		return false
	}
	before, present := front.Get(key)
	if present && reflect.DeepEqual(before, decoded) && timestampSpellingIs(front, key, v.kinds()) {
		return false
	}
	front.Set(key, decoded)
	RecordTimestampSpelling(front, key, v.kinds()...)
	return true
}

// RecordTimestampSpelling records how each time value stored at key must be
// serialized. kinds names the requested kind of the scalar at key, or of each
// element of the list stored there. Anything that is not a time is ignored.
func RecordTimestampSpelling(front *frontmatter.Map, key string, kinds ...Kind) {
	if front == nil {
		return
	}
	if list, isList := front.GetList(key); isList {
		for i := range list {
			if i >= len(kinds) {
				break
			}
			front.SetListTimeSpelling(key, i, kinds[i] == config.KindDate)
		}
		return
	}
	if len(kinds) == 0 {
		return
	}
	front.SetTimeSpelling(key, kinds[0] == config.KindDate)
}

// RecordListItemTimestampSpelling records the spelling of one list element, for
// a caller that appended an element instead of rewriting the whole list.
func RecordListItemTimestampSpelling(front *frontmatter.Map, key string, index int, kind Kind) {
	if front == nil {
		return
	}
	front.SetListTimeSpelling(key, index, kind == config.KindDate)
}

// timestampSpellingIs reports whether the time values stored at key already
// carry the spelling kinds asks for. Values that are not times never disagree.
func timestampSpellingIs(front *frontmatter.Map, key string, kinds []Kind) bool {
	if list, isList := front.GetList(key); isList {
		for i := range list {
			if i >= len(kinds) {
				break
			}
			if _, isTime := list[i].(time.Time); !isTime {
				continue
			}
			if front.ListTimeIsDateOnly(key, i) != (kinds[i] == config.KindDate) {
				return false
			}
		}
		return true
	}
	if _, isTime := front.GetTime(key); !isTime {
		return true
	}
	if len(kinds) == 0 {
		return true
	}
	return front.TimeIsDateOnly(key) == (kinds[0] == config.KindDate)
}

func (v TypedValue) decodeScalar() (any, error) {
	switch v.Kind {
	case config.KindString:
		return v.Text, nil
	case config.KindWikilink:
		return decodeWikilinkText(v.Text)
	case config.KindDate:
		// Parsing alone is too lenient: Go accepts a single-digit month or day
		// for this layout, so the canonical spelling is re-checked.
		t, err := time.Parse(dateLayout, v.Text)
		if err != nil || t.Format(dateLayout) != v.Text {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not a YYYY-MM-DD date", v.Text)
		}
		return t, nil
	case config.KindDatetime:
		t, err := time.Parse(datetimeLayout, v.Text)
		if err != nil {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not an RFC3339 datetime", v.Text)
		}
		return t, nil
	case config.KindInt:
		n, err := strconv.Atoi(v.Text)
		if err != nil || strconv.Itoa(n) != v.Text {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not an integer", v.Text)
		}
		return n, nil
	case config.KindFloat:
		f, err := strconv.ParseFloat(v.Text, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not a finite number", v.Text)
		}
		return f, nil
	case config.KindBool:
		switch v.Text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not a boolean (want true or false)", v.Text)
	case KindUnsupported, "":
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: null and unsupported values are read-only")
	}
	return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unknown value kind %q", string(v.Kind))
}

// decodeWikilinkText accepts a bare link target and returns its wrapped form.
// The wrapped spelling is round-tripped through the wikilink parser so a target
// carrying its own brackets, a heading, or an alias is refused instead of being
// written as a malformed link.
func decodeWikilinkText(text string) (string, error) {
	target := strings.TrimSpace(text)
	wrapped := "[[" + target + "]]"
	if parsed, ok := bareWikilinkTarget(wrapped); !ok || parsed != target {
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "properties: %q is not a bare wikilink target", text)
	}
	return wrapped, nil
}

// ValidateTypedValue decodes v and revalidates it against the declared
// definition. Revalidation is unconditional: a browser-side check is a
// convenience and never the authority for what reaches a note.
func ValidateTypedValue(key string, v TypedValue, def config.PropertyDefinition, defined bool) (any, error) {
	decoded, err := v.Decode()
	if err != nil {
		return nil, err
	}
	if !defined {
		return decoded, nil
	}
	if err := checkTypedKind(key, v, def); err != nil {
		return nil, err
	}

	probe := frontmatter.NewMap()
	probe.Set(key, decoded)
	if ok, detail := schema.CheckDefinition(&probe, key, decoded, def); !ok {
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %s: %s", key, detail)
	}
	if len(def.Allowed) > 0 {
		if ok, bad := schema.CheckAllowed(decoded, def.Allowed, def.IsWikilink()); !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %s: invalid value %q (allowed: %s)", key, bad, strings.Join(def.Allowed, ", "))
		}
	}
	return decoded, nil
}

// ValidateTypedListItem decodes and revalidates one element destined for a
// list-typed property, so a list addition is held to the same rules as a full
// list replacement.
func ValidateTypedListItem(key string, item TypedValue, def config.PropertyDefinition, defined bool) (any, error) {
	if defined && !def.IsList() {
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "properties: %s is declared %s, not a list", key, schema.KindLabel(def))
	}
	decoded, err := ValidateTypedValue(key, TypedValue{Kind: config.KindList, Items: []TypedValue{item}}, def, defined)
	if err != nil {
		return nil, err
	}
	items, ok := decoded.([]any)
	if !ok || len(items) != 1 {
		return nil, noteops.Errorf(noteops.CodeInternal, "properties: %s: list item decode produced no value", key)
	}
	return items[0], nil
}

func checkTypedKind(key string, v TypedValue, def config.PropertyDefinition) error {
	if !def.IsList() {
		if v.Kind != def.Type {
			return typedKindError(key, def, v.Kind, -1)
		}
		return nil
	}
	if v.Kind != config.KindList {
		return typedKindError(key, def, v.Kind, -1)
	}
	for i, item := range v.Items {
		if item.Kind != def.Items {
			return typedKindError(key, def, item.Kind, i)
		}
	}
	return nil
}

func typedKindError(key string, def config.PropertyDefinition, got Kind, index int) error {
	label := string(got)
	if label == "" {
		label = "an untyped value"
	}
	if index >= 0 {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: %s expects %s, got %s at index %d", key, schema.KindLabel(def), label, index)
	}
	return noteops.Errorf(noteops.CodeInvalidRequest, "properties: %s expects %s, got %s", key, schema.KindLabel(def), label)
}
