package web

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// A saved view stores four things — a query, a sort, a relative_to anchor, and
// a limit — and the nav link carries all four. This file is the half of the
// contract the server owes: the ordering and the cap are executed here, so a
// view opened in the browser returns the same result set `bn find --view` does.
//
// The filters half lives in filters.go. It stays separate because these two
// parameters do not select anything: an ordering with no filter must not turn
// an empty search box into a whole-vault fetch, which is why hasNonTextFilters
// deliberately ignores them.

// resultShape is the ordering and cap one request asks for.
type resultShape struct {
	// Sort is the requested ordering. A zero Key means no ordering was
	// requested, which is *not* the same as `path`: with no sort parameter the
	// search path keeps its own best-match-first ranking, and an explicit
	// `sort=path` overrides that with plain path order.
	Sort queryexpr.SortSpec
	// Limit caps the returned results, 0 meaning no request. It can only narrow
	// the payload: searchResultLimit still applies, so a view saving limit=500
	// does not make one request serialize the whole vault.
	Limit int
}

// ordered reports whether an ordering was requested.
func (s resultShape) ordered() bool { return s.Sort.Key != "" }

// effectiveLimit folds the request's cap together with the payload cap.
func (s resultShape) effectiveLimit() int {
	if s.Limit > 0 && s.Limit < searchResultLimit {
		return s.Limit
	}
	return searchResultLimit
}

// parseResultShape reads `sort`, `relative_to`, and `limit` from a request.
//
// The sort/relative_to agreement rules are the ones queryexpr.ResolveView
// enforces on a saved view and validateFindSort enforces on `bn find`'s flags:
// relevance needs an anchor, and an anchor is meaningless without relevance.
// Enforcing them here rather than ignoring the odd combination is what keeps a
// view that `bn views` calls disabled from quietly half-running in the browser.
func parseResultShape(params url.Values) (resultShape, error) {
	var shape resultShape

	if raw := strings.TrimSpace(params.Get("sort")); raw != "" {
		spec, err := queryexpr.ParseSort(raw)
		if err != nil {
			return resultShape{}, err
		}
		shape.Sort = spec
	}
	shape.Sort.RelativeTo = strings.TrimSpace(params.Get("relative_to"))
	if shape.Sort.Key == queryexpr.SortRelevance && shape.Sort.RelativeTo == "" {
		return resultShape{}, fmt.Errorf("sort %q requires relative_to", queryexpr.SortRelevance)
	}
	if shape.Sort.RelativeTo != "" && shape.Sort.Key != queryexpr.SortRelevance {
		return resultShape{}, fmt.Errorf("relative_to requires sort %q", queryexpr.SortRelevance)
	}

	if raw := strings.TrimSpace(params.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return resultShape{}, fmt.Errorf("invalid limit %q", raw)
		}
		if limit < 0 {
			return resultShape{}, fmt.Errorf("invalid limit %d: must not be negative", limit)
		}
		shape.Limit = limit
	}

	return shape, nil
}

// searchRelevanceFunc scores an already-selected candidate set against an
// anchor the caller resolved. It is nil unless relevance was requested, which
// is what keeps the recall corpus off every ordinary search.
//
// It returns the score per vault-relative path. Only the ordering is used, but
// scoring by path rather than returning a permutation is what lets the caller
// keep its own tie-break.
type searchRelevanceFunc func(relPaths []string) (map[string]float64, error)

// orderSearchCandidates applies a requested ordering to the matched candidates.
//
// Ordering runs over the notes rather than over the result rows so that
// query.OrderNotes — the same function `bn find --sort` calls — produces it. The
// candidates are then permuted into the note order, which is what makes "the
// server applied the view's sort" a statement about one implementation rather
// than about two that happen to agree today.
func orderSearchCandidates(candidates []searchCandidate, shape resultShape, in query.OrderInput, rank searchRelevanceFunc) ([]searchCandidate, error) {
	if !shape.ordered() || len(candidates) == 0 {
		return candidates, nil
	}

	notes := make([]*vault.Note, 0, len(candidates))
	for _, candidate := range candidates {
		notes = append(notes, candidate.note)
	}

	var (
		ordered []*vault.Note
		err     error
	)
	if shape.Sort.Key == queryexpr.SortRelevance {
		ordered, err = orderNotesByRelevance(notes, in.Root, shape.Sort.Descending, rank)
	} else {
		ordered, err = query.OrderNotes(notes, shape.Sort.Key, shape.Sort.Descending, in)
	}
	if err != nil {
		return nil, err
	}

	position := make(map[*vault.Note]int, len(ordered))
	for i, note := range ordered {
		if _, seen := position[note]; !seen {
			position[note] = i
		}
	}
	out := append([]searchCandidate(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		return position[out[i].note] < position[out[j].note]
	})
	return out, nil
}

// orderNotesByRelevance ranks notes against the anchor rank was built for, most
// related first.
//
// It mirrors `bn find --sort relevance`: the ranking is by score with a path
// tie-break, and a note the ranker returned no score for — the anchor is handled
// by the ranker itself, and a note the corpus does not index has none — keeps
// its place at the end instead of disappearing. Selection is authoritative:
// ordering must never change *which* notes come back.
func orderNotesByRelevance(notes []*vault.Note, root string, descending bool, rank searchRelevanceFunc) ([]*vault.Note, error) {
	if rank == nil {
		return nil, fmt.Errorf("sort %q: no relevance ranker for this request", queryexpr.SortRelevance)
	}

	paths := make(map[*vault.Note]string, len(notes))
	candidates := make([]string, 0, len(notes))
	seen := make(map[string]bool, len(notes))
	for _, note := range notes {
		relPath := noteVaultPath(root, note)
		if relPath == "" {
			continue
		}
		paths[note] = relPath
		if seen[relPath] {
			continue
		}
		seen[relPath] = true
		candidates = append(candidates, relPath)
	}

	scores, err := rank(candidates)
	if err != nil {
		return nil, err
	}

	ordered := append([]*vault.Note(nil), notes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, leftOK := scores[paths[ordered[i]]]
		right, rightOK := scores[paths[ordered[j]]]
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && left != right {
			if descending {
				return left < right
			}
			return left > right
		}
		return ordered[i].Path < ordered[j].Path
	})
	return ordered, nil
}

// searchRelevanceRanker resolves the anchor a relevance ordering names and
// returns a scorer bound to it.
//
// The anchor is resolved before the search runs, so naming a note that does not
// exist is a 400 on the request rather than an ordering that quietly degrades
// to path order. The corpus comes from the shared per-snapshot cache, so an
// ordered search costs no more corpus construction than the /api/related page
// on the same snapshot already did.
//
// The anchor scores +Inf rather than being ranked: RankSelected refuses a
// candidate that repeats the source, and a note that both matched the filter
// and is the thing everything else is measured against belongs first.
func (s *Server) searchRelevanceRanker(snapshot vaultSnapshot, anchor string) (searchRelevanceFunc, error) {
	anchorNote, _, ok := noteForRoute(snapshot, anchor)
	if !ok {
		return nil, fmt.Errorf("relative_to: no note matches %q", anchor)
	}
	anchorPath := noteVaultPath(s.vault.Root, anchorNote)
	if anchorPath == "" {
		return nil, fmt.Errorf("relative_to: note %q is outside the vault", anchor)
	}

	if err := s.reloadConfig(); err != nil {
		return nil, fmt.Errorf("relative_to: reload configuration: %w", err)
	}
	cfg, status := s.configState.publication()
	corpus, err := s.state.relatedCorpusFor(snapshot, cfg.Related.MetadataProperties, status.Generation)
	if err != nil {
		return nil, fmt.Errorf("relative_to: build relevance corpus: %w", err)
	}
	requestNow := s.briefNow()
	ranker := corpus.ranker.WithOptions(recall.RankOptions{
		Now: func() time.Time { return requestNow },
	})

	return func(relPaths []string) (map[string]float64, error) {
		candidates := make([]string, 0, len(relPaths))
		for _, path := range relPaths {
			if path == anchorPath {
				continue
			}
			if _, indexed := documentByPath(corpus.documents, path); !indexed {
				continue
			}
			candidates = append(candidates, path)
		}
		results, err := ranker.RankSelected(anchorPath, candidates)
		if err != nil {
			return nil, fmt.Errorf("relative_to: rank against %q: %w", anchor, err)
		}
		scores := make(map[string]float64, len(results)+1)
		scores[anchorPath] = math.Inf(1)
		for _, result := range results {
			scores[result.Path] = result.Score
		}
		return scores, nil
	}, nil
}
