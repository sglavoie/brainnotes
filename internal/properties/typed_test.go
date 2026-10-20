package properties

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func TestNewTypedValue_ScalarsListsAndReadOnlyValues(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		want TypedValue
		ok   bool
	}{
		{
			name: "supported scalar",
			in:   Value{Kind: config.KindInt, Text: "7", Value: 7, Supported: true},
			want: TypedValue{Kind: config.KindInt, Text: "7"},
			ok:   true,
		},
		{
			name: "list of supported scalars",
			in: Value{Kind: config.KindList, Text: "a, b", Items: []Value{
				{Kind: config.KindString, Text: "a", Supported: true},
				{Kind: config.KindWikilink, Text: "B", Supported: true},
			}},
			want: TypedValue{Kind: config.KindList, Text: "a, b", Items: []TypedValue{
				{Kind: config.KindString, Text: "a"},
				{Kind: config.KindWikilink, Text: "B"},
			}},
			ok: true,
		},
		{
			name: "empty list",
			in:   Value{Kind: config.KindList},
			want: TypedValue{Kind: config.KindList, Items: []TypedValue{}},
			ok:   true,
		},
		{
			name: "unsupported scalar is read-only",
			in:   Value{Kind: KindUnsupported, Text: "null"},
		},
		{
			name: "list with an unsupported element is read-only",
			in: Value{Kind: config.KindList, Items: []Value{
				{Kind: config.KindString, Text: "a", Supported: true},
				{Kind: KindUnsupported, Text: "map"},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NewTypedValue(tc.in)
			if ok != tc.ok {
				t.Fatalf("NewTypedValue ok = %v, want %v", ok, tc.ok)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NewTypedValue = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestTypedValueDecode_EachKind(t *testing.T) {
	date := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	datetime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		in   TypedValue
		want any
	}{
		{"string", TypedValue{Kind: config.KindString, Text: " as typed "}, " as typed "},
		{"wikilink is wrapped", TypedValue{Kind: config.KindWikilink, Text: "Books/Dune"}, "[[Books/Dune]]"},
		{"date", TypedValue{Kind: config.KindDate, Text: "2026-01-02"}, date},
		{"datetime", TypedValue{Kind: config.KindDatetime, Text: "2026-01-02T03:04:05Z"}, datetime},
		{"int", TypedValue{Kind: config.KindInt, Text: "-12"}, -12},
		{"float", TypedValue{Kind: config.KindFloat, Text: "1.5"}, 1.5},
		{"bool true", TypedValue{Kind: config.KindBool, Text: "true"}, true},
		{"bool false", TypedValue{Kind: config.KindBool, Text: "false"}, false},
		{
			"list",
			TypedValue{Kind: config.KindList, Items: []TypedValue{
				{Kind: config.KindWikilink, Text: "A"},
				{Kind: config.KindInt, Text: "2"},
			}},
			[]any{"[[A]]", 2},
		},
		{"empty list", TypedValue{Kind: config.KindList}, []any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.in.Decode()
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode = %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

func TestTypedValueDecode_RefusesNonCanonicalSpellings(t *testing.T) {
	cases := []struct {
		name string
		in   TypedValue
		want string
	}{
		{"single-digit month", TypedValue{Kind: config.KindDate, Text: "2026-1-02"}, "YYYY-MM-DD"},
		{"datetime as a date", TypedValue{Kind: config.KindDate, Text: "2026-01-02T03:04:05Z"}, "YYYY-MM-DD"},
		{"date as a datetime", TypedValue{Kind: config.KindDatetime, Text: "2026-01-02"}, "RFC3339"},
		{"int with a plus sign", TypedValue{Kind: config.KindInt, Text: "+3"}, "integer"},
		{"int with a decimal", TypedValue{Kind: config.KindInt, Text: "3.0"}, "integer"},
		{"float NaN", TypedValue{Kind: config.KindFloat, Text: "NaN"}, "finite"},
		{"float infinity", TypedValue{Kind: config.KindFloat, Text: "Inf"}, "finite"},
		{"bool spelled yes", TypedValue{Kind: config.KindBool, Text: "yes"}, "boolean"},
		{"wikilink with brackets", TypedValue{Kind: config.KindWikilink, Text: "[[A]]"}, "bare wikilink"},
		{"wikilink with an alias", TypedValue{Kind: config.KindWikilink, Text: "A|alias"}, "bare wikilink"},
		{"unsupported", TypedValue{Kind: KindUnsupported, Text: "null"}, "read-only"},
		{"untyped", TypedValue{Text: "x"}, "read-only"},
		{"unknown kind", TypedValue{Kind: "colour", Text: "red"}, "unknown value kind"},
		{
			"nested list",
			TypedValue{Kind: config.KindList, Items: []TypedValue{
				{Kind: config.KindString, Text: "a"},
				{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindString, Text: "b"}}},
			}},
			"lists do not nest",
		},
		{
			"bad list item",
			TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindInt, Text: "x"}}},
			"integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Decode()
			assertInvalidRequest(t, err, tc.want)
		})
	}
}

func TestValidateTypedValue_UndefinedKeyPassesThroughDecode(t *testing.T) {
	// No definition means no kind, schema or allowed-list check: the decoded
	// value is whatever the transport value says it is.
	got, err := ValidateTypedValue("anything", TypedValue{Kind: config.KindInt, Text: "4"}, config.PropertyDefinition{Type: config.KindString, Allowed: []string{"x"}}, false)
	if err != nil {
		t.Fatalf("ValidateTypedValue: %v", err)
	}
	if got != 4 {
		t.Fatalf("ValidateTypedValue = %#v, want 4", got)
	}

	_, err = ValidateTypedValue("anything", TypedValue{Kind: config.KindInt, Text: "four"}, config.PropertyDefinition{}, false)
	assertInvalidRequest(t, err, "integer")
}

func TestValidateTypedValue_DefinedKey(t *testing.T) {
	status := config.PropertyDefinition{Type: config.KindString, Allowed: []string{"wip", "done"}}
	tags := config.PropertyDefinition{Type: config.KindList, Items: config.KindString, Allowed: []string{"a", "b"}}
	parent := config.PropertyDefinition{Type: config.KindWikilink, Allowed: []string{"Home"}}
	due := config.PropertyDefinition{Type: config.KindDate}
	count := config.PropertyDefinition{Type: config.KindInt}

	ok := []struct {
		name string
		def  config.PropertyDefinition
		in   TypedValue
		want any
	}{
		{"allowed string", status, TypedValue{Kind: config.KindString, Text: "done"}, "done"},
		{"allowed list", tags, TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindString, Text: "b"}}}, []any{"b"}},
		{"allowed wikilink", parent, TypedValue{Kind: config.KindWikilink, Text: "Home"}, "[[Home]]"},
		{"date", due, TypedValue{Kind: config.KindDate, Text: "2026-03-04"}, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)},
		{"int", count, TypedValue{Kind: config.KindInt, Text: "3"}, 3},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTypedValue("key", tc.in, tc.def, true)
			if err != nil {
				t.Fatalf("ValidateTypedValue: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ValidateTypedValue = %#v, want %#v", got, tc.want)
			}
		})
	}

	refused := []struct {
		name string
		def  config.PropertyDefinition
		in   TypedValue
		want string
	}{
		{"value outside the allowed list", status, TypedValue{Kind: config.KindString, Text: "later"}, `invalid value "later"`},
		{"list item outside the allowed list", tags, TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindString, Text: "c"}}}, `invalid value "c"`},
		{"wikilink outside the allowed list", parent, TypedValue{Kind: config.KindWikilink, Text: "Away"}, "invalid value"},
		{"scalar kind mismatch", count, TypedValue{Kind: config.KindString, Text: "3"}, "expects int, got string"},
		{"scalar for a list", tags, TypedValue{Kind: config.KindString, Text: "a"}, "expects list of string, got string"},
		{"list for a scalar", status, TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindString, Text: "wip"}}}, "expects string, got list"},
		{"list item kind mismatch", tags, TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindInt, Text: "1"}}}, "got int at index 0"},
		{"nested list", tags, TypedValue{Kind: config.KindList, Items: []TypedValue{{Kind: config.KindList}}}, "lists do not nest"},
		{"decode failure comes first", due, TypedValue{Kind: config.KindDate, Text: "soon"}, "YYYY-MM-DD"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTypedValue("key", tc.in, tc.def, true)
			assertInvalidRequest(t, err, tc.want)
			if got != nil {
				t.Fatalf("ValidateTypedValue returned %#v alongside an error", got)
			}
		})
	}
}

func TestValidateTypedListItem(t *testing.T) {
	tags := config.PropertyDefinition{Type: config.KindList, Items: config.KindString, Allowed: []string{"a", "b"}}
	links := config.PropertyDefinition{Type: config.KindList, Items: config.KindWikilink}

	t.Run("allowed item", func(t *testing.T) {
		got, err := ValidateTypedListItem("tags", TypedValue{Kind: config.KindString, Text: "a"}, tags, true)
		if err != nil || got != "a" {
			t.Fatalf("ValidateTypedListItem = %#v, %v; want \"a\", nil", got, err)
		}
	})
	t.Run("wikilink item is wrapped", func(t *testing.T) {
		got, err := ValidateTypedListItem("links", TypedValue{Kind: config.KindWikilink, Text: "Dune"}, links, true)
		if err != nil || got != "[[Dune]]" {
			t.Fatalf("ValidateTypedListItem = %#v, %v; want \"[[Dune]]\", nil", got, err)
		}
	})
	t.Run("undefined key passes through", func(t *testing.T) {
		got, err := ValidateTypedListItem("free", TypedValue{Kind: config.KindBool, Text: "true"}, config.PropertyDefinition{}, false)
		if err != nil || got != true {
			t.Fatalf("ValidateTypedListItem = %#v, %v; want true, nil", got, err)
		}
	})

	refused := []struct {
		name    string
		def     config.PropertyDefinition
		defined bool
		item    TypedValue
		want    string
	}{
		{"declared scalar", config.PropertyDefinition{Type: config.KindString}, true, TypedValue{Kind: config.KindString, Text: "a"}, "is declared string, not a list"},
		{"item outside the allowed list", tags, true, TypedValue{Kind: config.KindString, Text: "z"}, `invalid value "z"`},
		{"item kind mismatch", tags, true, TypedValue{Kind: config.KindInt, Text: "1"}, "got int at index 0"},
		{"nested list item", tags, true, TypedValue{Kind: config.KindList}, "lists do not nest"},
		{"nested list item on an undefined key", config.PropertyDefinition{}, false, TypedValue{Kind: config.KindList}, "lists do not nest"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTypedListItem("key", tc.item, tc.def, tc.defined)
			assertInvalidRequest(t, err, tc.want)
			if got != nil {
				t.Fatalf("ValidateTypedListItem returned %#v alongside an error", got)
			}
		})
	}
}

// assertInvalidRequest requires err to be a noteops invalid-request error whose
// message contains want.
func assertInvalidRequest(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	var opErr *noteops.Error
	if !errors.As(err, &opErr) || opErr.Code != noteops.CodeInvalidRequest {
		t.Fatalf("expected a %s error, got %T: %v", noteops.CodeInvalidRequest, err, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
