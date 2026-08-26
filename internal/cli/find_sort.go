package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// queryexpr never ranks: a SortSpec says what ordering was asked for, and the
// caller produces it. This file is that caller for `bn find`.
//
// Every key except `relevance` is produced by query.OrderNotes, which the web
// search path calls too, so a saved view's stored ordering means the same thing
// at the terminal and in the browser. `relevance` stays here because it reads
// the recall sidecar, which internal/query deliberately does not.

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
func sortFindResults(in findSortInput, spec queryexpr.SortSpec) ([]*vault.Note, error) {
	if spec.Key == queryexpr.SortRelevance {
		return sortByRelevance(in, spec)
	}
	ordered, err := query.OrderNotes(in.matched, spec.Key, spec.Descending, query.OrderInput{
		Root:  in.vault.Root,
		Notes: in.notes,
		Graph: in.graph,
	})
	if err != nil {
		// Unreachable while ParseSort guards the flag, but a hand-built SortSpec
		// must not silently fall through to an unordered result.
		return nil, fmt.Errorf("find: %w", err)
	}
	return ordered, nil
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
