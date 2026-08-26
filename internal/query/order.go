package query

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/pins"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Ordering lives here rather than in internal/queryexpr because queryexpr never
// ranks: a SortSpec says what ordering was asked for and nothing about how to
// produce it. Every key below needs state that package deliberately does not
// hold — frontmatter dates, the link graph, the pinned list — all of which this
// package already owns.
//
// It lives here rather than in a command because there is more than one caller:
// `bn find --sort` and the web search path both have to order a result set, and
// a saved view stores one ordering that both must execute identically. Two
// implementations of "-modified" would be two chances to disagree about where a
// note with no date sorts.

// The order key vocabulary this package can produce. These strings are the
// surface spellings a user types after --sort and a saved view stores, so they
// are deliberately the same literals as queryexpr's Sort* constants; queryexpr
// cannot be imported here (it imports this package), so
// TestOrderKeyVocabularyMatchesQueryexpr in internal/cli pins the two together.
//
// queryexpr's `relevance` is absent on purpose: ranking by relevance reads the
// recall sidecar, and keeping that read out of this package is what stops
// recall's read/write boundary from spreading. OrderNotes reports
// ErrUnsupportedOrder for it, and each caller produces it with the ranker it
// already holds.
const (
	OrderPath      = "path"
	OrderTitle     = "title"
	OrderModified  = "modified"
	OrderCreated   = "created"
	OrderBacklinks = "backlinks"
	OrderDegree    = "degree"
	OrderPinned    = "pinned"
)

// orderKeys is the vocabulary in canonical (sorted) order.
var orderKeys = []string{
	OrderBacklinks,
	OrderCreated,
	OrderDegree,
	OrderModified,
	OrderPath,
	OrderPinned,
	OrderTitle,
}

// OrderKeys returns the keys OrderNotes can produce, sorted. The result is a
// fresh slice.
func OrderKeys() []string {
	out := make([]string, len(orderKeys))
	copy(out, orderKeys)
	return out
}

// ErrUnsupportedOrder reports a key this package does not order by. A caller
// that owns an extra ordering (relevance, say) tests for it with errors.Is
// rather than re-deriving the vocabulary.
var ErrUnsupportedOrder = errors.New("query: unsupported order key")

// OrderInput is the vault-wide state the orderings read. Notes and Graph
// describe the whole vault rather than the notes being ordered, because the
// graph keys are vault-wide.
type OrderInput struct {
	// Root is the vault root, needed by the pinned ordering.
	Root string
	// Notes is the whole vault, used only to build a Graph when one is needed
	// and none was supplied.
	Notes []*vault.Note
	// Graph is the caller's already-built link graph, or nil to build lazily.
	Graph *Graph
}

// linkGraph returns the caller's graph, building one only if the caller never
// needed it for filtering.
func (in OrderInput) linkGraph() *Graph {
	if in.Graph != nil {
		return in.Graph
	}
	return BuildGraphForRoot(in.Root, in.Notes)
}

// OrderNotes returns notes ordered by key. The input slice is never mutated:
// the result is a fresh slice, so a caller holding the unordered selection
// keeps it.
//
// An empty key means OrderPath. An unknown key — including `relevance` — is an
// error wrapping ErrUnsupportedOrder rather than a silently unordered result.
func OrderNotes(notes []*vault.Note, key string, descending bool, in OrderInput) ([]*vault.Note, error) {
	out := append([]*vault.Note(nil), notes...)
	if key == "" {
		key = OrderPath
	}

	switch key {
	case OrderPath:
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return strings.Compare(a.Path, b.Path) },
		}, descending)
		return out, nil

	case OrderTitle:
		titles := make(map[*vault.Note]string, len(out))
		for _, n := range out {
			titles[n] = strings.ToLower(textnorm.NFC(n.Title()))
		}
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return strings.Compare(titles[a], titles[b]) },
		}, descending)
		return out, nil

	case OrderModified:
		return orderByDateProperty(out, "last", descending), nil

	case OrderCreated:
		return orderByDateProperty(out, "created", descending), nil

	case OrderBacklinks:
		g := in.linkGraph()
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return g.InDegreeForNote(a) - g.InDegreeForNote(b) },
		}, descending)
		return out, nil

	case OrderDegree:
		g := in.linkGraph()
		metrics := NewGraphMetrics(g)
		degree := func(n *vault.Note) int { return metrics.Degree(g.KeyForNote(n)) }
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return degree(a) - degree(b) },
		}, descending)
		return out, nil

	case OrderPinned:
		return orderByPinRank(in.Root, out, descending), nil
	}

	return nil, fmt.Errorf("%w %q", ErrUnsupportedOrder, key)
}

// OrderNotesByRelevance orders notes by a relevance score the caller computed,
// most related first. The input slice is never mutated.
//
// Scoring stays out of this package — it reads the recall sidecar, which this
// package deliberately does not — but *ordering* a set of scores does not, and
// having it here is what stops `bn find --sort relevance` and `/api/search`
// from owning two definitions of the same order. Both hand this function a
// score per vault-relative path and the function that renders a note as that
// path; everything below is then the vocabulary's ordinary ordering rules:
//
//   - Higher score sorts first, because relevance ascending means most related
//     first, exactly as `bn related` prints it. `-relevance` flips that
//     comparison and nothing else.
//   - A note with no score sorts last in *both* directions, like an undated
//     note under `--sort modified`. The anchor is not one of these: a caller
//     scores it +Inf, so it leads under `relevance` and trails under
//     `-relevance`. A note the recall corpus never indexed is one, and it keeps
//     its place at the end rather than disappearing — ordering never changes
//     which notes come back.
//   - Notes that tie — equal scores, or two unscored notes — are ordered by
//     path ascending in both directions, because the tie-break is a stability
//     device rather than part of the requested ordering.
//
// pathOf may return "" for a note it cannot place; that note is unscored.
func OrderNotesByRelevance(notes []*vault.Note, pathOf func(*vault.Note) string, scores map[string]float64, descending bool) []*vault.Note {
	out := append([]*vault.Note(nil), notes...)
	scoreOf := func(n *vault.Note) (float64, bool) {
		if pathOf == nil {
			return 0, false
		}
		path := pathOf(n)
		if path == "" {
			return 0, false
		}
		score, ok := scores[path]
		return score, ok
	}
	values := make(map[*vault.Note]float64, len(out))
	scored := make(map[*vault.Note]bool, len(out))
	for _, n := range out {
		if score, ok := scoreOf(n); ok {
			values[n] = score
			scored[n] = true
		}
	}
	applyNoteOrder(out, noteOrder{
		missing: func(n *vault.Note) bool { return !scored[n] },
		compare: func(a, b *vault.Note) int {
			switch {
			// Reversed against the other keys on purpose: ascending
			// relevance is descending score.
			case values[a] > values[b]:
				return -1
			case values[a] < values[b]:
				return 1
			}
			return 0
		},
	}, descending)
	return out
}

// noteOrder is one ordering rule: which notes have no value for the key, and
// how two notes that do have one compare ascending.
type noteOrder struct {
	// missing reports that the note carries no value for this key. A note with
	// no value sorts after every note that has one in *both* directions, so
	// `--sort -modified` means "newest first" rather than "undated first".
	missing func(*vault.Note) bool
	// compare orders two present values ascending. It is never called for a
	// note that missing reports true for.
	compare func(a, b *vault.Note) int
}

// applyNoteOrder sorts notes in place.
//
// The path tie-break stays ascending in both directions on purpose: it is a
// stability device, not part of the requested ordering, so reversing it would
// make `--sort x` and `--sort -x` disagree about notes that are equal under x.
func applyNoteOrder(notes []*vault.Note, order noteOrder, descending bool) {
	missing := func(n *vault.Note) bool { return order.missing != nil && order.missing(n) }
	sort.SliceStable(notes, func(i, j int) bool {
		a, b := notes[i], notes[j]
		missingA, missingB := missing(a), missing(b)
		switch {
		case missingA != missingB:
			return missingB
		case !missingA:
			if c := order.compare(a, b); c != 0 {
				if descending {
					return c > 0
				}
				return c < 0
			}
		}
		return a.Path < b.Path
	})
}

// orderByDateProperty orders on a frontmatter timestamp. A note whose property
// is absent or unparseable has no value for the key and sorts last.
func orderByDateProperty(notes []*vault.Note, property string, descending bool) []*vault.Note {
	type stamp struct {
		value int64
		ok    bool
	}
	stamps := make(map[*vault.Note]stamp, len(notes))
	for _, n := range notes {
		front, err := n.Front()
		if err != nil {
			stamps[n] = stamp{}
			continue
		}
		t, ok := front.GetTime(property)
		if !ok {
			stamps[n] = stamp{}
			continue
		}
		stamps[n] = stamp{value: t.UnixNano(), ok: true}
	}
	applyNoteOrder(notes, noteOrder{
		missing: func(n *vault.Note) bool { return !stamps[n].ok },
		compare: func(a, b *vault.Note) int {
			switch {
			case stamps[a].value < stamps[b].value:
				return -1
			case stamps[a].value > stamps[b].value:
				return 1
			}
			return 0
		},
	}, descending)
	return notes
}

// orderByPinRank orders by the canonical pinned order. pins.List places every
// pinned note; an unpinned note has no rank and sorts last.
func orderByPinRank(root string, notes []*vault.Note, descending bool) []*vault.Note {
	byRel := make(map[string]*vault.Note, len(notes))
	for _, n := range notes {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			continue
		}
		byRel[filepath.ToSlash(rel)] = n
	}
	ranks := make(map[*vault.Note]int, len(notes))
	for _, entry := range pins.List(root, notes) {
		n, ok := byRel[entry.RelPath]
		if !ok {
			continue
		}
		if _, placed := ranks[n]; placed {
			continue
		}
		ranks[n] = len(ranks)
	}
	applyNoteOrder(notes, noteOrder{
		missing: func(n *vault.Note) bool { _, ok := ranks[n]; return !ok },
		compare: func(a, b *vault.Note) int { return ranks[a] - ranks[b] },
	}, descending)
	return notes
}
