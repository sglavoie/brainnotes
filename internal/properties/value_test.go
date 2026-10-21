package properties

import (
	"reflect"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

func classify(t *testing.T, source string, key string) Value {
	t.Helper()

	doc, err := frontmatter.Parse([]byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	value, ok := ClassifyProperty(&doc.Front, key)
	if !ok {
		t.Fatalf("property %q not found in %q", key, source)
	}
	return value
}

func TestClassifyProperty_Scalars(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		key       string
		wantKind  Kind
		wantText  string
		wantValue any
	}{
		{"string", "---\nk: hello\n---\n", "k", config.KindString, "hello", "hello"},
		{"wikilink", "---\nk: \"[[Target]]\"\n---\n", "k", config.KindWikilink, "Target", "Target"},
		{"aliased wikilink", "---\nk: \"[[Target|Alias]]\"\n---\n", "k", config.KindWikilink, "Target", "Target"},
		{"int", "---\nk: 42\n---\n", "k", config.KindInt, "42", 42},
		{"float", "---\nk: 1.5\n---\n", "k", config.KindFloat, "1.5", 1.5},
		{"bool", "---\nk: true\n---\n", "k", config.KindBool, "true", true},
		{"date", "---\nk: 2026-01-02\n---\n", "k", config.KindDate, "2026-01-02", "2026-01-02"},
		{"datetime", "---\nk: 2026-01-02T03:04:05Z\n---\n", "k", config.KindDatetime, "2026-01-02T03:04:05Z", "2026-01-02T03:04:05Z"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			value := classify(t, testCase.source, testCase.key)
			if value.Kind != testCase.wantKind {
				t.Fatalf("Kind = %q, want %q", value.Kind, testCase.wantKind)
			}
			if value.Text != testCase.wantText {
				t.Fatalf("Text = %q, want %q", value.Text, testCase.wantText)
			}
			if !reflect.DeepEqual(value.Value, testCase.wantValue) {
				t.Fatalf("Value = %#v, want %#v", value.Value, testCase.wantValue)
			}
			if !value.Supported {
				t.Fatalf("scalar %s must be supported", testCase.name)
			}
		})
	}
}

func TestClassifyProperty_TextOnlyLinkIsNotAWikilink(t *testing.T) {
	value := classify(t, "---\nk: \"see [[Target]] here\"\n---\n", "k")

	if value.Kind != config.KindString {
		t.Fatalf("Kind = %q, want string: a wikilink property stores exactly one link and nothing else", value.Kind)
	}
	if value.Text != "see [[Target]] here" {
		t.Fatalf("Text = %q, want the whole string preserved", value.Text)
	}
}

func TestClassifyProperty_List(t *testing.T) {
	value := classify(t, "---\nk:\n  - go\n  - \"[[Target]]\"\n---\n", "k")

	if !value.IsList() {
		t.Fatalf("Kind = %q, want list", value.Kind)
	}
	if got, want := value.Text, "go, Target"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
	logical := value.Logical()
	if len(logical) != 2 {
		t.Fatalf("Logical() = %d values, want 2", len(logical))
	}
	if logical[0].Kind != config.KindString || logical[1].Kind != config.KindWikilink {
		t.Fatalf("element kinds = %q, %q; want string, wikilink", logical[0].Kind, logical[1].Kind)
	}
	if !value.Supported {
		t.Fatalf("a list of supported elements is supported")
	}
}

// TestClassifyProperty_ListElementSpelling pins that an element written as a
// timestamp is reported as a datetime even at midnight UTC, where the value
// alone cannot tell the two kinds apart.
func TestClassifyProperty_ListElementSpelling(t *testing.T) {
	value := classify(t, "---\nk:\n  - 2026-01-05T00:00:00Z\n  - 2026-01-06\n---\n", "k")

	logical := value.Logical()
	if len(logical) != 2 {
		t.Fatalf("Logical() = %d values, want 2", len(logical))
	}
	if logical[0].Kind != config.KindDatetime || logical[0].Text != "2026-01-05T00:00:00Z" {
		t.Fatalf("element 0 = %q %q, want a datetime", logical[0].Kind, logical[0].Text)
	}
	if logical[1].Kind != config.KindDate || logical[1].Text != "2026-01-06" {
		t.Fatalf("element 1 = %q %q, want a date", logical[1].Kind, logical[1].Text)
	}
}

func TestClassifyProperty_ScalarLogicalIsItself(t *testing.T) {
	value := classify(t, "---\nk: hello\n---\n", "k")

	logical := value.Logical()
	if len(logical) != 1 || logical[0].Text != "hello" {
		t.Fatalf("Logical() = %#v, want the scalar itself", logical)
	}
}

func TestClassifyProperty_UnsupportedValues(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		wantLabel string
	}{
		{"null", "---\nk:\n---\n", "<null>"},
		{"map", "---\nk:\n  nested: 1\n---\n", "<map>"},
		{"nested list", "---\nk:\n  - - inner\n---\n", "<nested list>"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			value := classify(t, testCase.source, "k")
			logical := value.Logical()
			last := logical[len(logical)-1]
			if last.Supported {
				t.Fatalf("%s must be reported as unsupported", testCase.name)
			}
			if last.Text != testCase.wantLabel {
				t.Fatalf("Text = %q, want %q", last.Text, testCase.wantLabel)
			}
			if last.Value != nil {
				t.Fatalf("Value = %#v, want nil: an unsupported value must never be stringified into something a write could reproduce", last.Value)
			}
		})
	}
}

func TestClassifyProperty_MissingKey(t *testing.T) {
	doc, err := frontmatter.Parse([]byte("---\nk: hello\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := ClassifyProperty(&doc.Front, "absent"); ok {
		t.Fatalf("ClassifyProperty reported a value for an absent key")
	}
	if _, ok := ClassifyProperty(nil, "k"); ok {
		t.Fatalf("ClassifyProperty reported a value for nil frontmatter")
	}
}

func TestValue_KeySeparatesKinds(t *testing.T) {
	stringOne := classify(t, "---\nk: \"1\"\n---\n", "k")
	intOne := classify(t, "---\nk: 1\n---\n", "k")

	if stringOne.key() == intOne.key() {
		t.Fatalf("string \"1\" and int 1 must not share a value key")
	}
}

func TestFormatFloat_KeepsDecimalPoint(t *testing.T) {
	if got, want := formatFloat(2), "2.0"; got != want {
		t.Fatalf("formatFloat(2) = %q, want %q", got, want)
	}
	if got, want := formatFloat(1.25), "1.25"; got != want {
		t.Fatalf("formatFloat(1.25) = %q, want %q", got, want)
	}
}
