// Package query builds composable predicates over vault notes. Predicates are
// pure functions that decide whether a single *vault.Note satisfies a query
// term; the package supplies builders for the frontmatter dimensions the `find`
// command exposes (tags, categories, status, type, presence, folder, age, and
// orphan detection) plus an And combinator.
package query

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if lst, ok := fm.GetList("tags"); ok {
			for _, item := range lst {
				if strings.ToLower(stringifyTag(item)) == want {
					return true
				}
			}
			return false
		}
		if v, ok := fm.Get("tags"); ok {
			if s, isStr := v.(string); isStr {
				return strings.ToLower(s) == want
			}
			return strings.ToLower(stringifyTag(v)) == want
		}
		return false
	}
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
		return fmt.Sprintf("%v", vv)
	case time.Time:
		if vv.Hour() == 0 && vv.Minute() == 0 && vv.Second() == 0 && vv.Nanosecond() == 0 && vv.Location() == time.UTC {
			return vv.Format("2006-01-02")
		}
		return vv.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", vv)
	}
}

// Category matches notes whose `categories` frontmatter contains v
// (case-insensitive) as a wikilink target. A frontmatter value of
// `[[Books]]` matches v == "books".
func Category(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		cats, ok := fm.GetWikilinkList("categories")
		if !ok {
			return false
		}
		for _, c := range cats {
			if strings.ToLower(c) == want {
				return true
			}
		}
		return false
	}
}

// Status matches notes whose `status` frontmatter equals v (case-insensitive).
// Non-string values (e.g. an int stored by `bn set status=5`'s auto-coercion)
// are stringified before comparison so the same value remains query-able.
func Status(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, ok := fm.Get("status")
		if !ok {
			return false
		}
		return strings.ToLower(stringifyTag(raw)) == want
	}
}

// Type matches notes whose `type` (non-project) or `kind` (project) frontmatter
// equals v (case-insensitive). Vault convention puts project notes under
// `kind` and other notes under `type`; this predicate accepts either so
// queries don't need to know the difference. Non-string values are stringified
// before comparison (see Status for the rationale).
func Type(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if raw, ok := fm.Get("type"); ok && strings.ToLower(stringifyTag(raw)) == want {
			return true
		}
		if raw, ok := fm.Get("kind"); ok && strings.ToLower(stringifyTag(raw)) == want {
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
func Missing(prop string) Predicate {
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		return !fm.Has(prop)
	}
}

// Folder matches notes that live in folder or one of its descendants. folder is
// vault-relative with forward slashes; an empty folder matches every note.
// root is the absolute vault root (used to compute each note's folder).
//
// Leading/trailing slashes, backslashes, and `./` segments in the user-supplied
// folder are normalized: `--folder /References`, `--folder References/`,
// `--folder ./References`, and `--folder References\Books` all behave like
// `References` (or `References/Books`).
func Folder(folder, root string) Predicate {
	folder = strings.TrimSpace(folder)
	want := strings.Trim(strings.ReplaceAll(folder, "\\", "/"), "/")
	want = path.Clean(want)
	if want == "." {
		want = ""
	}
	return func(n *vault.Note) bool {
		if want == "" {
			return true
		}
		nf, err := n.Folder(root)
		if err != nil {
			return false
		}
		return nf == want || strings.HasPrefix(nf, want+"/")
	}
}

// Orphan matches notes that no *other* note in the vault links to. The index
// is built once per find run via BuildBacklinkIndex.
//
// Self-references are excluded so a note that only links to itself still
// counts as an orphan — this matches `bn backlinks`, which also excludes the
// target's own path from its referrers list.
func Orphan(idx BacklinkIndex) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		t := strings.ToLower(n.Title())
		for _, p := range idx[t] {
			if p != n.Path {
				return false
			}
		}
		return true
	}
}

// Age matches notes whose date at prop satisfies the comparison. op is one of
// ">", "<", or "=":
//   - ">": note is older than dur (i.e. now - prop > dur).
//   - "<": note is younger than dur.
//   - "=": note's age in days equals dur in days (e.g. =0d → today).
//
// For "=" comparisons, the comparison is performed on calendar dates in the
// local time zone. This matters because YAML date scalars like `2026-05-19`
// parse as midnight UTC, so a naive `time.Since` measurement can land on the
// wrong side of a day boundary when the local zone differs from UTC. The
// frontmatter date is interpreted as a local-time date when its time-of-day
// component is zero and the zone is UTC (the YAML-date convention).
//
// Notes without a parseable timestamp at prop never match.
func Age(op string, dur time.Duration, prop string) Predicate {
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		t, ok := fm.GetTime(prop)
		if !ok {
			return false
		}
		switch op {
		case ">":
			return time.Since(t) > dur
		case "<":
			return time.Since(t) < dur
		case "=":
			return equalDayAge(t, dur)
		}
		return false
	}
}

// equalDayAge reports whether t is exactly dur days old, with day boundaries
// evaluated in the local time zone. A YAML date scalar (midnight UTC, zero
// time-of-day) is reinterpreted as the same calendar date in local time so
// that `=0d` reliably means "today" regardless of the user's timezone.
func equalDayAge(t time.Time, dur time.Duration) bool {
	now := time.Now()
	noteDate := localDate(t, now.Location())
	wantDate := now.AddDate(0, 0, -int(dur/(24*time.Hour))).In(now.Location())
	wantY, wantM, wantD := wantDate.Date()
	gotY, gotM, gotD := noteDate.Date()
	return gotY == wantY && gotM == wantM && gotD == wantD
}

// localDate returns t shifted into loc. When t has no time-of-day component
// and is in UTC — the YAML-date convention — its calendar date is preserved
// rather than converted (so a `2026-05-19` value remains 2026-05-19 in any
// local zone).
func localDate(t time.Time, loc *time.Location) time.Time {
	if t.Location() == time.UTC && t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	return t.In(loc)
}
