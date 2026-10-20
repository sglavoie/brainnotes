package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
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

// resultShape is the ordering, total query limit, and page position.
type resultShape struct {
	// Sort is the requested ordering. A zero Key means no ordering was
	// requested, which is *not* the same as `path`: with no sort parameter the
	// search path keeps its own best-match-first ranking, and an explicit
	// `sort=path` overrides that with plain path order.
	Sort queryexpr.SortSpec
	// Limit caps the complete result set, 0 meaning no request. Each page is
	// independently bounded by searchResultLimit; Offset skips within that set.
	Limit  int
	Offset int
	// Selected rechecks a bounded selection against the complete ordered query.
	Selected []string
}

// ordered reports whether an ordering was requested.
func (s resultShape) ordered() bool { return s.Sort.Key != "" }

// parseResultShape reads ordering, query limit, and page offset from a request.
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
	shape.Sort.Search = params.Get("q")
	shape.Sort.RelativeTo = strings.TrimSpace(params.Get("relative_to"))
	if shape.Sort.Key == queryexpr.SortRelevance && shape.Sort.RelativeTo == "" {
		return resultShape{}, fmt.Errorf("sort %q requires relative_to", queryexpr.SortRelevance)
	}
	if shape.Sort.RelativeTo != "" && shape.Sort.Key != queryexpr.SortRelevance {
		return resultShape{}, fmt.Errorf("relative_to requires sort %q", queryexpr.SortRelevance)
	}

	var err error
	if shape.Limit, err = parseSearchBound(params, "limit"); err != nil {
		return resultShape{}, err
	}
	if shape.Offset, err = parseSearchBound(params, "offset"); err != nil {
		return resultShape{}, err
	}
	shape.Selected = params["selected"]
	if len(shape.Selected) > searchResultLimit {
		return resultShape{}, fmt.Errorf("at most %d selected keys can be checked at once", searchResultLimit)
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
		in.Search = shape.Sort.Search
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
// It does not define that order: query.OrderNotesByRelevance does, and
// `bn find --sort relevance` calls the same function with the same shape of
// score map. This half only turns notes into the vault-relative paths the
// ranker scores by. Selection stays authoritative — a note the corpus does not
// index goes unscored and keeps its place at the end rather than disappearing.
func orderNotesByRelevance(notes []*vault.Note, root string, descending bool, rank searchRelevanceFunc) ([]*vault.Note, error) {
	if rank == nil {
		return nil, fmt.Errorf("sort %q: no relevance ranker for this request", queryexpr.SortRelevance)
	}

	paths := make(map[*vault.Note]string, len(notes))
	candidates := make([]string, 0, len(notes))
	seen := make(map[string]bool, len(notes))
	for _, note := range notes {
		relPath := relevancePath(root, note)
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

	return query.OrderNotesByRelevance(notes, func(note *vault.Note) string {
		return paths[note]
	}, scores, descending), nil
}

// relevancePath renders a note as the key the recall corpus indexes it under.
//
// It is noteVaultPath plus the NFC normalization recall.Document applies, which
// is the same three steps `bn find`'s recallPath takes. Skipping the
// normalization here would make a decomposed filename unscored in the browser
// and scored at the terminal — the one place the two surfaces could still
// disagree about which notes a relevance ordering ranks.
func relevancePath(root string, note *vault.Note) string {
	return textnorm.NFC(noteVaultPath(root, note))
}

// searchClientError marks a search request the client must change: an anchor
// that names no note is the request's fault, and a 400 says so. Every other
// failure on the way to a ranking (a configuration that will not reload, a
// corpus that will not build) is the server's, and is reported as one.
type searchClientError struct{ err error }

func (e *searchClientError) Error() string { return e.err.Error() }
func (e *searchClientError) Unwrap() error { return e.err }

func searchClientErrorf(format string, args ...any) error {
	return &searchClientError{err: fmt.Errorf(format, args...)}
}

// writeSearchOrderError sends a client mistake as the 400 filter error the
// query bar reads, and anything else as a 500 management error. Both pass
// through the reader redaction.
func writeSearchOrderError(w http.ResponseWriter, err error) {
	var clientErr *searchClientError
	if errors.As(err, &clientErr) {
		writeFilterError(w, err)
		return
	}
	writeManagementError(w, noteops.WrapError(noteops.CodeInternal, err.Error(), err))
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
		return nil, searchClientErrorf("relative_to: no note matches %q", anchor)
	}
	anchorPath := relevancePath(s.vault.Root, anchorNote)
	if anchorPath == "" {
		return nil, searchClientErrorf("relative_to: note %q is outside the vault", anchor)
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

func parseSearchBound(params url.Values, key string) (int, error) {
	raw := strings.TrimSpace(params.Get(key))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s %q: must be a non-negative integer", key, raw)
	}
	return n, nil
}
