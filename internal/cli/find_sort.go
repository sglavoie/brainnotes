package cli

import (
	"fmt"
	"math"
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
		Root:   in.vault.Root,
		Search: spec.Search,
		Notes:  in.notes,
		Graph:  in.graph,
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
// Three things here are load-bearing.
//
// It ranks with Ranker.RankSelected, never Rank. Selection is authoritative:
// the notes were chosen by the query, so mode eligibility and recall
// dismiss/snooze suppression must not remove any of them. Rank would silently
// drop query matches, which would make --sort relevance change *which* notes
// come back rather than only their order.
//
// The anchor is removed from the candidate set before ranking — RankSelected
// rejects a candidate that repeats the source — and scored +Inf instead, so an
// anchor the query matched is still reported, and leads the `relevance`
// ordering it is the measure of.
//
// The ordering itself is query.OrderNotesByRelevance, which the web search path
// calls with the same shape of score map. That is the only definition of what
// `relevance` and `-relevance` mean, tie-break and unscored notes included, so
// a saved view carrying a direction and a limit cannot select a different top N
// here than it does in the browser.
func sortByRelevance(in findSortInput, spec queryexpr.SortSpec) ([]*vault.Note, error) {
	anchorPath, err := relevanceAnchorPath(in.vault, spec.RelativeTo)
	if err != nil {
		return nil, err
	}
	corpus, err := relevanceCorpus(in)
	if err != nil {
		return nil, err
	}
	pathByNote, candidates, err := relevanceCandidates(in, corpus, anchorPath)
	if err != nil {
		return nil, err
	}

	results, err := corpus.ranker.RankSelected(anchorPath, candidates)
	if err != nil {
		return nil, err
	}
	scores := make(map[string]float64, len(results)+1)
	scores[anchorPath] = math.Inf(1)
	for _, result := range results {
		scores[result.Path] = result.Score
	}

	return query.OrderNotesByRelevance(in.matched, func(n *vault.Note) string {
		return pathByNote[n]
	}, scores, spec.Descending), nil
}

// relevanceAnchorPath resolves --relative-to to the vault-relative path the
// ranker keys on.
func relevanceAnchorPath(v *vault.Vault, relativeTo string) (string, error) {
	anchorNote, err := resolveNoteTarget(v, relativeTo)
	if err != nil {
		return "", err
	}
	anchorPath, ok := recallPath(v.Root, anchorNote.Path)
	if !ok {
		return "", fmt.Errorf("find: --relative-to note %q is outside the vault", anchorNote.Path)
	}
	return anchorPath, nil
}

// relevanceCorpus builds the corpus the ordering ranks against, over the whole
// vault rather than the matched subset, and reusing the filter's graph when it
// already built one.
func relevanceCorpus(in findSortInput) (*recallCorpus, error) {
	setup, err := newRecallSetup(in.vault.Root, recallNow())
	if err != nil {
		return nil, err
	}
	return buildRecallCorpus(in.vault, in.notes, in.graph, setup.rankOptions())
}

// relevanceCandidates returns the vault-relative path of every matched note and
// the deduplicated subset of them to rank. The anchor is ranked against rather
// than ranked, and a note the corpus never indexed cannot be ranked at all — both
// stay out of the candidate list but keep their path, so they are still reported
// as unscored rather than dropped.
func relevanceCandidates(in findSortInput, corpus *recallCorpus, anchorPath string) (map[*vault.Note]string, []string, error) {
	indexed := make(map[string]bool, len(corpus.documents))
	for _, document := range corpus.documents {
		indexed[document.Path] = true
	}

	pathByNote := make(map[*vault.Note]string, len(in.matched))
	candidates := make([]string, 0, len(in.matched))
	seen := make(map[string]bool, len(in.matched))
	for _, n := range in.matched {
		relPath, ok := recallPath(in.vault.Root, n.Path)
		if !ok {
			return nil, nil, fmt.Errorf("find: note %q is outside the vault", n.Path)
		}
		pathByNote[n] = relPath
		if relPath == anchorPath || seen[relPath] || !indexed[relPath] {
			continue
		}
		seen[relPath] = true
		candidates = append(candidates, relPath)
	}
	return pathByNote, candidates, nil
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
