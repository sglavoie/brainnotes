// Package pins is the shared data model for user-curated pinned notes. Pin
// state lives in each note's `pinned` frontmatter property as a float rank, so
// it survives `bn rename`/`bn move` for free and stays visible and editable in
// Obsidian.
//
// The package is deliberately a leaf: it may import only the low-level vault,
// frontmatter, config, and textnorm layers. internal/query builds its --pinned
// predicate on Classify, and internal/noteops already imports internal/query,
// so importing internal/noteops here would close the cycle
// query -> pins -> noteops -> query.
package pins

import (
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// PropertyName is the frontmatter key holding a note's pin rank.
const PropertyName = "pinned"

// Kind is how a note's `pinned` value participates in the pinned list.
type Kind int

const (
	// NotPinned means the note is absent from the pinned list.
	NotPinned Kind = iota
	// Ranked means the note is pinned at an explicit float rank.
	Ranked
	// Unranked means the note is pinned but carries no usable rank. It is
	// shown as pinned and sorts after every ranked pin; it is given a real
	// rank the first time it is dragged or re-pinned.
	Unranked
)

func (k Kind) String() string {
	switch k {
	case NotPinned:
		return "not-pinned"
	case Ranked:
		return "ranked"
	case Unranked:
		return "unranked"
	}
	return "unknown"
}

// Classify interprets a raw `pinned` frontmatter value, where present reports
// whether the key existed at all. The rank is meaningful only for Ranked.
//
// A user can hand-edit `pinned` in Obsidian, so this must never panic and must
// never silently drop a note the user believes is pinned: anything pinned-ish
// that cannot yield a finite rank degrades to Unranked rather than NotPinned.
// Only an absent key and an explicit false mean "not pinned".
func Classify(v any, present bool) (float64, Kind) {
	if !present {
		return 0, NotPinned
	}
	switch value := v.(type) {
	case bool:
		if !value {
			return 0, NotPinned
		}
		return 0, Unranked
	case int:
		return float64(value), Ranked
	case int64:
		return float64(value), Ranked
	case float64:
		return finiteRank(value)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, Unranked
		}
		return finiteRank(f)
	}
	return 0, Unranked
}

func finiteRank(f float64) (float64, Kind) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, Unranked
	}
	return f, Ranked
}

// Entry is one note in the pinned list. RelPath is the vault-relative path in
// its on-disk form with forward slashes; ordering normalizes it to NFC.
type Entry struct {
	RelPath string
	Rank    float64
	Kind    Kind
}

// List returns every pinned note among notes, in display order: ranked pins
// first in ascending rank, then unranked pins, with ties broken on the
// NFC-normalized vault-relative path. Duplicate ranks are legal, so the path
// tiebreak is what makes the order total and stable.
//
// Notes that cannot be read or that fall outside root are skipped rather than
// reported: the pinned list is a view, and one unreadable note must not hide
// the rest.
func List(root string, notes []*vault.Note) []Entry {
	entries := make([]Entry, 0, len(notes))
	for _, n := range notes {
		if n == nil {
			continue
		}
		fm, err := n.Front()
		if err != nil {
			continue
		}
		raw, present := fm.Get(PropertyName)
		rank, kind := Classify(raw, present)
		if kind == NotPinned {
			continue
		}
		rel, err := relPath(root, n.Path)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{RelPath: rel, Rank: rank, Kind: kind})
	}
	sortEntries(entries)
	return entries
}

func relPath(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errOutsideRoot
	}
	return rel, nil
}

// sortEntries applies the canonical pinned order in place.
func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Kind == Ranked) != (b.Kind == Ranked) {
			return a.Kind == Ranked
		}
		if a.Kind == Ranked && a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		return textnorm.NFC(a.RelPath) < textnorm.NFC(b.RelPath)
	})
}
