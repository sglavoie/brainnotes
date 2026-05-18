// Package query builds composable predicates over vault notes. Predicates are
// pure functions that decide whether a single *vault.Note satisfies a query
// term; the package supplies builders for the frontmatter dimensions the `find`
// command exposes (tags, categories, status, type, presence, folder, age, and
// orphan detection) plus an And combinator.
package query

import (
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
// A scalar string tag is treated as a single-element list.
func Tag(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if lst, ok := fm.GetList("tags"); ok {
			for _, item := range lst {
				if s, ok := item.(string); ok && strings.ToLower(s) == want {
					return true
				}
			}
			return false
		}
		if s, ok := fm.GetString("tags"); ok {
			return strings.ToLower(s) == want
		}
		return false
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

// Status matches notes whose `status` frontmatter string equals v
// (case-insensitive).
func Status(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		s, ok := fm.GetString("status")
		if !ok {
			return false
		}
		return strings.ToLower(s) == want
	}
}

// Type matches notes whose `type` (non-project) or `kind` (project) frontmatter
// string equals v (case-insensitive). Vault convention puts project notes
// under `kind` and other notes under `type`; this predicate accepts either so
// queries don't need to know the difference.
func Type(v string) Predicate {
	want := strings.ToLower(v)
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		if s, ok := fm.GetString("type"); ok && strings.ToLower(s) == want {
			return true
		}
		if s, ok := fm.GetString("kind"); ok && strings.ToLower(s) == want {
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
func Folder(folder, root string) Predicate {
	want := strings.TrimSuffix(folder, "/")
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

// Orphan matches notes whose title has no entry in idx (i.e. nothing in the
// vault links to them). The index is built once per find run via
// BuildBacklinkIndex.
func Orphan(idx BacklinkIndex) Predicate {
	return func(n *vault.Note) bool {
		t := strings.ToLower(n.Title())
		return len(idx[t]) == 0
	}
}

// Age matches notes whose date at prop satisfies the comparison. op is one of
// ">", "<", or "=":
//   - ">": note is older than dur (i.e. now - prop > dur).
//   - "<": note is younger than dur.
//   - "=": note's age in days equals dur in days (e.g. =0d → today).
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
		age := time.Since(t)
		switch op {
		case ">":
			return age > dur
		case "<":
			return age < dur
		case "=":
			return ageDays(age) == ageDays(dur)
		}
		return false
	}
}

func ageDays(d time.Duration) int64 {
	return int64(d / (24 * time.Hour))
}
