// Package query builds composable predicates over vault notes. Predicates are
// pure functions that decide whether a single *vault.Note satisfies a query
// term; the package supplies builders for the frontmatter dimensions the `find`
// command exposes (tags, categories, status, type, presence, folder, age, and
// orphan detection) plus an And combinator.
package query

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// Predicate evaluates a note against a query term. A nil note is rejected.
type Predicate func(*vault.Note) bool

// And returns a predicate that matches when every supplied predicate matches.
// With no arguments it matches every (non-nil) note, so it doubles as the
// identity predicate for queries that pass no flags.
func And(ps ...Predicate) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		for _, p := range ps {
			if p == nil {
				continue
			}
			if !p(n) {
				return false
			}
		}
		return true
	}
}

// Or returns a predicate that matches when any supplied predicate matches.
// With no arguments it matches nothing.
func Or(ps ...Predicate) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		for _, p := range ps {
			if p == nil {
				continue
			}
			if p(n) {
				return true
			}
		}
		return false
	}
}

// Tag matches notes whose `tags` frontmatter list contains v (case-insensitive).
// A scalar string tag is treated as a single-element list. Non-string list
// items are stringified before comparison so a tag stored as a YAML int
// (e.g. `tags: [2026]`) still matches `--tag 2026`. `bn set tags+=2026`
// auto-coerces the literal to an int and would otherwise produce a tag that
// nothing in the CLI could find again.
func Tag(v string) Predicate {
	want := comparisonKey(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if lst, ok := fm.GetList("tags"); ok {
			for _, item := range lst {
				if tagValueMatches(item, want) {
					return true
				}
			}
			return false
		}
		if v, ok := fm.Get("tags"); ok {
			return tagValueMatches(v, want)
		}
		return false
	}
}

func tagValueMatches(value any, want string) bool {
	if s, ok := value.(string); ok {
		if title, isLink := exactWikilinkTitle(s); isLink {
			return title == want
		}
	}
	return valueKey(value) == want
}

// stringifyTag renders a non-string frontmatter list element as the text a user
// would type for it. Strings pass through; ints/bools/floats render via their
// canonical form; time.Time values render as YAML date or RFC3339 (matching
// formatTimeValue in values.go). nil and unknown types render as "".
func stringifyTag(v any) string {
	switch vv := v.(type) {
	case nil:
		return ""
	case string:
		return vv
	case bool:
		if vv {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", vv)
	case int64:
		return fmt.Sprintf("%d", vv)
	case float64:
		// Preserve the decimal marker so whole-number floats remain queryable
		// in the same textual form the user types (and that frontmatter.Marshal
		// emits): `status: 3.0` round-trips through `--status 3.0`. The default
		// fmt %v rendering drops the trailing ".0" for whole numbers, breaking
		// queries against YAML float scalars.
		s := strconv.FormatFloat(vv, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case time.Time:
		return frontmatter.FormatTimestamp(vv)
	default:
		return fmt.Sprintf("%v", vv)
	}
}

// StringifyValue renders a frontmatter value as the text a user would type for
// it. It is exactly the rendering Tag and Status already compare against,
// exported so a caller comparing an *undeclared* property — one with no
// [frontmatter.properties] entry to derive a type from — reaches the same
// answer the flag surface does instead of reimplementing the conversion.
//
// nil renders as the empty string and a value with no canonical scalar spelling
// falls back to fmt's default rendering; neither is expected to match anything a
// user types.
func StringifyValue(v any) string { return stringifyTag(v) }

// comparisonKey is the form both sides of a Tag, Category, Status or Type
// comparison are reduced to: NFC-normalized, then lowercased. Normalizing only
// the query would leave a decomposed `café` written by a macOS tool unequal to
// the composed `café` a user types, and the lexer already composes the query.
func comparisonKey(s string) string { return strings.ToLower(textnorm.NFC(s)) }

// valueKey is comparisonKey for a frontmatter value, stringified first.
func valueKey(v any) string { return comparisonKey(stringifyTag(v)) }

// Category matches notes whose `categories` frontmatter contains v
// (case-insensitive) as a wikilink target title. Folder prefixes, `.md`
// suffixes, headings, and aliases are ignored, so `[[Library/Books.md#Now|To
// Read]]` matches v == "books". Plain scalar/list string values still match by
// their literal text.
func Category(v string) Predicate {
	want := comparisonKey(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, ok := fm.Get("categories")
		if !ok {
			return false
		}
		return categoryMatches(raw, want)
	}
}

func categoryMatches(v any, want string) bool {
	switch vv := v.(type) {
	case string:
		return categoryStringMatches(vv, want)
	case []any:
		for _, item := range vv {
			s, ok := item.(string)
			if ok && categoryStringMatches(s, want) {
				return true
			}
		}
	}
	return false
}

func categoryStringMatches(s, want string) bool {
	if title, ok := exactWikilinkTitle(s); ok {
		return title == want
	}
	return valueKey(s) == want
}

func exactWikilinkTitle(s string) (string, bool) {
	links := wikilink.Parse(s)
	if len(links) != 1 || links[0].Start != 0 || links[0].End != len(s) {
		return "", false
	}
	return linkTitleKey(links[0].Title()), true
}

// WikilinkTitleKey reports whether s is exactly one wikilink — no surrounding
// text — and, if so, returns its normalized title key: folder prefix, `.md`
// suffix, heading, and alias stripped, then NFC-normalized and lowercased. It is
// the same key BuildGraph uses to resolve links, so a caller comparing a
// frontmatter value against a note title should fold both through it.
//
// A non-link string, or a string containing a link plus anything else, returns
// ("", false).
func WikilinkTitleKey(s string) (string, bool) {
	return exactWikilinkTitle(s)
}

// LocalDate returns t shifted into loc. When t has no time-of-day component and
// is in UTC — the convention a YAML date scalar like `2026-05-19` parses to —
// its calendar date is preserved rather than converted, so the date does not
// slip a day in a non-UTC local zone.
//
// loc is a parameter rather than a fixed time.Local so callers stay consistent
// with Age, which compares against time.Now().Location().
func LocalDate(t time.Time, loc *time.Location) time.Time {
	return localDate(t, loc)
}

// Status matches notes whose `status` frontmatter equals v (case-insensitive).
// Non-string values (e.g. an int stored by `bn set status=5`'s auto-coercion)
// are stringified before comparison so the same value remains query-able.
func Status(v string) Predicate {
	want := comparisonKey(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, ok := fm.Get("status")
		if !ok {
			return false
		}
		return valueKey(raw) == want
	}
}

// Type matches notes whose `type` (non-project) or `kind` (project) frontmatter
// equals v (case-insensitive). Vault convention puts project notes under
// `kind` and other notes under `type`; this predicate accepts either so
// queries don't need to know the difference. Non-string values are stringified
// before comparison (see Status for the rationale).
func Type(v string) Predicate {
	want := comparisonKey(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if raw, ok := fm.Get("type"); ok && valueKey(raw) == want {
			return true
		}
		if raw, ok := fm.Get("kind"); ok && valueKey(raw) == want {
			return true
		}
		return false
	}
}

// Has matches notes whose frontmatter contains the named property.
func Has(prop string) Predicate {
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		return fm.Has(prop)
	}
}

// Missing matches notes whose frontmatter does not contain the named property.
// It is the strict complement of Has on every non-nil note: when the note
// cannot be loaded or its frontmatter cannot be parsed, the property is treated
// as absent (Missing matches, Has does not). A nil note matches neither.
func Missing(prop string) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		fm, err := n.Front()
		if err != nil {
			return true
		}
		return !fm.Has(prop)
	}
}
