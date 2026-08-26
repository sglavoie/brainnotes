package queryexpr

import (
	"strings"
	"testing"
)

// caretAt splits a fixture on "|" and returns the source plus the byte offset
// the caret marker stood at, so a table case reads as the text a user typed.
func caretAt(t *testing.T, fixture string) (string, int) {
	t.Helper()
	pos := strings.Index(fixture, "|")
	if pos < 0 {
		t.Fatalf("fixture %q has no caret marker", fixture)
	}
	return fixture[:pos] + fixture[pos+1:], pos
}

func TestCompletionAt(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		kind     CompletionKind
		field    string
		arg      string
		prefix   string
		span     Span
		quoted   bool
		wikilink bool
	}{
		{name: "empty", fixture: "|", kind: CompleteField, span: Span{0, 0}},
		{name: "partial field", fixture: "ta|", kind: CompleteField, prefix: "ta", span: Span{0, 2}},
		{name: "second term", fixture: "tag:wip fol|", kind: CompleteField, prefix: "fol", span: Span{8, 11}},
		{name: "after negation", fixture: "-fol|", kind: CompleteField, prefix: "fol", span: Span{1, 4}},
		{name: "after keyword", fixture: "tag:wip and or|", kind: CompleteField, prefix: "or", span: Span{12, 14}},
		{name: "after open paren", fixture: "(tag:a or de|", kind: CompleteField, prefix: "de", span: Span{10, 12}},
		{name: "hyphenated field", fixture: "dead-en|", kind: CompleteField, prefix: "dead-en", span: Span{0, 7}},

		{name: "empty value", fixture: "tag:|", kind: CompleteValue, field: "tag", span: Span{4, 4}},
		{name: "partial value", fixture: "tag:wi|", kind: CompleteValue, field: "tag", prefix: "wi", span: Span{4, 6}},
		{name: "equals operator", fixture: "status=wi|", kind: CompleteValue, field: "status", prefix: "wi", span: Span{7, 9}},
		{name: "not equals operator", fixture: "status!=wi|", kind: CompleteValue, field: "status", prefix: "wi", span: Span{8, 10}},
		{name: "gte operator", fixture: "backlinks>=|", kind: CompleteValue, field: "backlinks", span: Span{11, 11}},
		{name: "field case folded", fixture: "Tag:wi|", kind: CompleteValue, field: "tag", prefix: "wi", span: Span{4, 6}},

		{name: "argument form", fixture: "age:created>9|", kind: CompleteValue, field: "age", arg: "created", prefix: "9", span: Span{12, 13}},
		{name: "prop value", fixture: "prop:rating>|", kind: CompleteValue, field: "prop", arg: "rating", span: Span{12, 12}},
		{name: "prop name", fixture: "prop:rat|", kind: CompleteValue, field: "prop", prefix: "rat", span: Span{5, 8}},

		{name: "list first element", fixture: "tag:[|", kind: CompleteValue, field: "tag", span: Span{5, 5}},
		{name: "list later element", fixture: "tag:[a,b|", kind: CompleteValue, field: "tag", prefix: "b", span: Span{7, 8}},
		{name: "after closed list", fixture: "tag:[a,b] fo|", kind: CompleteField, prefix: "fo", span: Span{10, 12}},

		{name: "quoted value", fixture: "status:\"in pro|", kind: CompleteValue, field: "status", prefix: "in pro", span: Span{7, 14}, quoted: true},
		{name: "quoted phrase at start", fixture: "\"deep wo|", kind: CompleteField, prefix: "deep wo", span: Span{0, 8}, quoted: true},
		{name: "after closed quote", fixture: "status:\"in progress\" fo|", kind: CompleteField, prefix: "fo", span: Span{21, 23}},

		{name: "wikilink target", fixture: "links-to:[[Deep Wo|", kind: CompleteValue, field: "links-to", prefix: "Deep Wo", span: Span{9, 18}, wikilink: true},
		{name: "empty wikilink target", fixture: "near:[[|", kind: CompleteValue, field: "near", span: Span{5, 7}, wikilink: true},
		{name: "hop bound after wikilink", fixture: "near:[[Hub]]<=|", kind: CompleteValue, field: "near", arg: "Hub", span: Span{14, 14}},
		{name: "hop bound after quoted target", fixture: "near:\"Deep Work\"<=|", kind: CompleteValue, field: "near", arg: "Deep Work", span: Span{18, 18}},
		{name: "parenthesised argument", fixture: "age(created)>9|", kind: CompleteValue, field: "age", arg: "created", prefix: "9", span: Span{13, 14}},

		// Whitespace after the operator reads as a fresh token, not a value:
		// guessing otherwise offers tag values where a field name belongs.
		{name: "space after operator", fixture: "tag: |", kind: CompleteField, span: Span{5, 5}},

		{name: "multi byte prefix", fixture: "tag:café|", kind: CompleteValue, field: "tag", prefix: "café", span: Span{4, 9}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := caretAt(t, tc.fixture)
			got := CompletionAt(src, pos)
			if got.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.kind)
			}
			if got.Field != tc.field {
				t.Errorf("field = %q, want %q", got.Field, tc.field)
			}
			if got.Arg != tc.arg {
				t.Errorf("arg = %q, want %q", got.Arg, tc.arg)
			}
			if got.Prefix != tc.prefix {
				t.Errorf("prefix = %q, want %q", got.Prefix, tc.prefix)
			}
			if got.Span != tc.span {
				t.Errorf("span = %+v, want %+v", got.Span, tc.span)
			}
			if got.Quoted != tc.quoted {
				t.Errorf("quoted = %v, want %v", got.Quoted, tc.quoted)
			}
			if got.Wikilink != tc.wikilink {
				t.Errorf("wikilink = %v, want %v", got.Wikilink, tc.wikilink)
			}
			if got.Span.Start < 0 || got.Span.End > len(src) || got.Span.Start > got.Span.End {
				t.Errorf("span %+v is not a valid range into %q", got.Span, src)
			}
		})
	}
}

func TestCompletionAtClampsAndAlignsOffsets(t *testing.T) {
	const src = "tag:café"

	if got := CompletionAt(src, -5); got.Span != (Span{0, 0}) || got.Kind != CompleteField {
		t.Errorf("negative pos = %+v, want an empty field completion at 0", got)
	}
	if got := CompletionAt(src, len(src)+9); got.Prefix != "café" {
		t.Errorf("past-end pos prefix = %q, want %q", got.Prefix, "café")
	}
	// len(src)-1 splits the two bytes of é; the caret must fall back to the
	// rune boundary rather than slicing the source mid-character.
	if got := CompletionAt(src, len(src)-1); got.Prefix != "caf" {
		t.Errorf("mid-rune pos prefix = %q, want %q", got.Prefix, "caf")
	}
}

func TestFieldsCoverTheReservedVocabulary(t *testing.T) {
	fields := Fields()
	if len(fields) != len(reservedFields) {
		t.Fatalf("Fields() returned %d entries, want %d", len(fields), len(reservedFields))
	}

	seen := make(map[string]struct{}, len(fields))
	for i, f := range fields {
		if i > 0 && fields[i-1].Name >= f.Name {
			t.Fatalf("Fields() is not sorted: %q then %q", fields[i-1].Name, f.Name)
		}
		if strings.TrimSpace(f.Hint) == "" {
			t.Errorf("field %q has no hint; add one to fieldHints", f.Name)
		}
		reserved, ok := reservedFields[f.Name]
		if !ok {
			t.Errorf("field %q is not a reserved field", f.Name)
			continue
		}
		if f.NeedsGraph != reserved.needsGraph {
			t.Errorf("field %q NeedsGraph = %v, want %v", f.Name, f.NeedsGraph, reserved.needsGraph)
		}
		seen[f.Name] = struct{}{}
	}
	for name := range fieldHints {
		if _, ok := seen[name]; !ok {
			t.Errorf("fieldHints documents %q, which is not a reserved field", name)
		}
	}
}

func TestIsReservedField(t *testing.T) {
	for _, name := range []string{"tag", "TAG", "dead-end", " near "} {
		if !IsReservedField(name) {
			t.Errorf("IsReservedField(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "rating", "sort", "tga"} {
		if IsReservedField(name) {
			t.Errorf("IsReservedField(%q) = true, want false", name)
		}
	}
}

func TestFieldsUsed(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{src: "", want: nil},
		{src: "deep work", want: nil},
		{src: "tag:alpha", want: []string{"tag"}},
		{src: "Tag:alpha or TAG:beta", want: []string{"tag"}},
		{src: "tag:alpha and not folder:Archive", want: []string{"folder", "tag"}},
		{src: "prop:rating>3", want: []string{"prop"}},
		{src: "age:created>90d and (orphan:true or backlinks>2) deep", want: []string{"age", "backlinks", "orphan"}},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			expr, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.src, err)
			}
			got := FieldsUsed(expr)
			if len(got) != len(tc.want) {
				t.Fatalf("FieldsUsed(%q) = %#v, want %#v", tc.src, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("FieldsUsed(%q) = %#v, want %#v", tc.src, got, tc.want)
				}
			}
		})
	}

	if got := FieldsUsed(nil); got != nil && len(got) != 0 {
		t.Errorf("FieldsUsed(nil) = %#v, want empty", got)
	}
}
