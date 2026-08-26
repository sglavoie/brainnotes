package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/pins"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// queryexpr never ranks: a SortSpec says what ordering was asked for, and the
// caller produces it. This file is that caller for `bn find`. Every key beyond
// `path` needs state the expression package deliberately does not hold —
// frontmatter dates, the link graph, the pinned list, or a recall ranker — so
// the evaluator hands back a path-ordered result and the ordering is applied
// here, once, over the notes that survived the filter.

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

// findSortInput is the state the orderings read. matched is what the filter
// selected; notes and graph describe the whole vault, because relevance ranks
// against the full corpus and the graph keys are vault-wide.
type findSortInput struct {
	vault   *vault.Vault
	matched []*vault.Note
	notes   []*vault.Note
	graph   *query.Graph
}

// sortFindResults orders the matched notes by spec and returns them.
//
// The default path ordering is what the evaluator already produced, so it is
// re-applied only to honour a descending request.
func sortFindResults(in findSortInput, spec queryexpr.SortSpec) ([]*vault.Note, error) {
	out := append([]*vault.Note(nil), in.matched...)
	key := spec.Key
	if key == "" {
		key = queryexpr.SortPath
	}

	switch key {
	case queryexpr.SortPath:
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return strings.Compare(a.Path, b.Path) },
		}, spec.Descending)
		return out, nil

	case queryexpr.SortTitle:
		titles := make(map[*vault.Note]string, len(out))
		for _, n := range out {
			titles[n] = strings.ToLower(textnorm.NFC(n.Title()))
		}
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return strings.Compare(titles[a], titles[b]) },
		}, spec.Descending)
		return out, nil

	case queryexpr.SortModified:
		return sortByDateProperty(out, "last", spec.Descending), nil

	case queryexpr.SortCreated:
		return sortByDateProperty(out, "created", spec.Descending), nil

	case queryexpr.SortBacklinks:
		g := in.linkGraph()
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return g.InDegreeForNote(a) - g.InDegreeForNote(b) },
		}, spec.Descending)
		return out, nil

	case queryexpr.SortDegree:
		g := in.linkGraph()
		metrics := query.NewGraphMetrics(g)
		degree := func(n *vault.Note) int { return metrics.Degree(g.KeyForNote(n)) }
		applyNoteOrder(out, noteOrder{
			compare: func(a, b *vault.Note) int { return degree(a) - degree(b) },
		}, spec.Descending)
		return out, nil

	case queryexpr.SortPinned:
		return sortByPinRank(in.vault.Root, out, spec.Descending), nil

	case queryexpr.SortRelevance:
		return sortByRelevance(in, spec)
	}

	// Unreachable while ParseSort guards the flag, but a hand-built SortSpec
	// must not silently fall through to an unordered result.
	return nil, fmt.Errorf("find: cannot order by sort key %q", key)
}

// linkGraph returns the caller's graph, building one only if the filter never
// needed it.
func (in findSortInput) linkGraph() *query.Graph {
	if in.graph != nil {
		return in.graph
	}
	return query.BuildGraphForRoot(in.vault.Root, in.notes)
}

// sortByDateProperty orders on a frontmatter timestamp. A note whose property
// is absent or unparseable has no value for the key and sorts last.
func sortByDateProperty(notes []*vault.Note, property string, descending bool) []*vault.Note {
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

// sortByPinRank orders by the canonical pinned order. pins.List places every
// pinned note; an unpinned note has no rank and sorts last.
func sortByPinRank(root string, notes []*vault.Note, descending bool) []*vault.Note {
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

// sortByRelevance orders the matched notes by how related they are to
// spec.RelativeTo, most relevant first.
//
// Two things here are load-bearing.
//
// It ranks with Ranker.RankSelected, never Rank. Selection is authoritative:
// the notes were chosen by the query, so mode eligibility and recall
// dismiss/snooze suppression must not remove any of them. Rank would silently
// drop query matches, which would make --sort relevance change *which* notes
// come back rather than only their order.
//
// The anchor is removed from the candidate set before ranking — RankSelected
// rejects a candidate that repeats the source — and re-inserted at position 0
// when the query matched it, so an anchor that satisfies the filter is still
// reported, and is reported first.
//
// The ordering is by rank, so ascending is most-relevant-first: position 1 is
// the closest note, exactly as in `bn related`. `--sort -relevance` reverses it.
func sortByRelevance(in findSortInput, spec queryexpr.SortSpec) ([]*vault.Note, error) {
	anchorNote, err := resolveNoteTarget(in.vault, spec.RelativeTo)
	if err != nil {
		return nil, err
	}
	anchorPath, ok := recallPath(in.vault.Root, anchorNote.Path)
	if !ok {
		return nil, fmt.Errorf("find: --relative-to note %q is outside the vault", anchorNote.Path)
	}

	setup, err := newRecallSetup(in.vault.Root, recallNow())
	if err != nil {
		return nil, err
	}
	corpus, err := buildRecallCorpus(in.vault, in.notes, in.graph, setup.rankOptions())
	if err != nil {
		return nil, err
	}

	notesByPath := make(map[string]*vault.Note, len(in.matched))
	candidates := make([]string, 0, len(in.matched))
	var anchorMatched *vault.Note
	for _, n := range in.matched {
		relPath, ok := recallPath(in.vault.Root, n.Path)
		if !ok {
			return nil, fmt.Errorf("find: note %q is outside the vault", n.Path)
		}
		if relPath == anchorPath {
			anchorMatched = n
			continue
		}
		if _, seen := notesByPath[relPath]; seen {
			continue
		}
		notesByPath[relPath] = n
		candidates = append(candidates, relPath)
	}

	results, err := corpus.ranker.RankSelected(anchorPath, candidates)
	if err != nil {
		return nil, err
	}
	// RankSelected returns results in the caller's order, which is path order.
	// Ordering by score here mirrors what Rank does, tie-break included, so the
	// two commands agree on what "more relevant" means.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})

	ordered := make([]*vault.Note, 0, len(in.matched))
	if anchorMatched != nil {
		ordered = append(ordered, anchorMatched)
	}
	for _, result := range results {
		if n, ok := notesByPath[result.Path]; ok {
			ordered = append(ordered, n)
		}
	}
	if spec.Descending {
		for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
	}
	return ordered, nil
}

// validateFindSort turns the --sort and --relative-to values into a SortSpec.
//
// --relative-to is bound here rather than by ParseSort because it names a note
// rather than an ordering. The two flags are required to agree in both
// directions: relevance without an anchor has nothing to rank against, and an
// anchor without relevance would be silently ignored.
func validateFindSort(sortText, relativeTo string) (queryexpr.SortSpec, error) {
	spec, err := queryexpr.ParseSort(sortText)
	if err != nil {
		return queryexpr.SortSpec{}, err
	}
	spec.RelativeTo = strings.TrimSpace(relativeTo)
	if spec.Key == queryexpr.SortRelevance && spec.RelativeTo == "" {
		return queryexpr.SortSpec{}, fmt.Errorf("--sort %s requires --relative-to", queryexpr.SortRelevance)
	}
	if spec.RelativeTo != "" && spec.Key != queryexpr.SortRelevance {
		return queryexpr.SortSpec{}, fmt.Errorf("--relative-to requires --sort %s", queryexpr.SortRelevance)
	}
	return spec, nil
}

// findSortHelp renders the --sort flag help from the vocabulary itself, so a
// key added to queryexpr cannot go unmentioned here.
func findSortHelp() string {
	return "order results by " + strings.Join(queryexpr.SortKeys(), ", ") +
		" (prefix with '-' for descending)"
}
