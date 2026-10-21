package queryexpr

import (
	"context"
	"errors"
	"sort"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// Evaluator runs a compiled plan against a corpus of notes. It is an interface
// so the in-memory scan stays swappable for a cache-backed one later without
// every caller changing: one evaluator, one set of results, so parity between
// execution paths is structural rather than something tests must defend.
type Evaluator interface {
	Evaluate(ctx context.Context, plan *Plan) ([]*vault.Note, error)
}

// memoryEvaluator scans an already-loaded []*vault.Note. At vault scale the
// scan is microseconds — the cost of a query is loading and parsing the notes,
// which the caller has already paid by the time it gets here.
type memoryEvaluator struct {
	root  string
	notes []*vault.Note
	env   Env
}

// NewMemoryEvaluator returns an Evaluator over notes already in memory.
//
// The graph is built lazily: a plan whose NeedsGraph reports false never pays
// for query.BuildGraphForRoot, and a plan that needs one gets it built once and
// reused for every Evaluate call on this evaluator. env.Graph, when supplied,
// is used as-is.
func NewMemoryEvaluator(root string, notes []*vault.Note, env Env) Evaluator {
	if env.Root == "" {
		env.Root = root
	}
	return &memoryEvaluator{root: root, notes: notes, env: env}
}

// Evaluate returns every note the plan matches, ordered by the plan's SortSpec
// and truncated to its Limit. The result is a fresh slice; the notes in it are
// the evaluator's own, not copies.
func (e *memoryEvaluator) Evaluate(ctx context.Context, plan *Plan) ([]*vault.Note, error) {
	if plan == nil {
		return nil, errors.New("queryexpr: evaluate needs a compiled plan")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if plan.NeedsGraph() && e.env.Graph == nil {
		e.env.Graph = query.BuildGraphForRoot(e.root, e.notes)
	}
	pred, err := plan.Predicate(e.env)
	if err != nil {
		return nil, err
	}

	out := make([]*vault.Note, 0, len(e.notes))
	for i, n := range e.notes {
		// Cancellation is checked in blocks: a predicate can read a note body
		// off disk, so a long scan is interruptible, but the check itself must
		// not dominate a scan of cheap frontmatter predicates.
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if pred(n) {
			out = append(out, n)
		}
	}
	if err := sortNotes(out, plan.Sort); err != nil {
		return nil, err
	}
	if plan.Limit > 0 && len(out) > plan.Limit {
		out = out[:plan.Limit]
	}
	return out, nil
}

// sortNotes applies a SortSpec in place. Only the default path ordering is
// understood here; any other key is an error rather than a silent no-op, so a
// caller asking for an ordering this evaluator cannot honour finds out.
//
// The two failures are distinguished on purpose. An unknown key is a bad
// SortSpec — ParseSort would have rejected it — while a *valid* key this
// evaluator cannot honour is the queryexpr-never-ranks boundary showing
// through: backlinks, degree, pinned, and relevance all need state that reaches
// the caller and not this package, so the caller orders the result itself.
func sortNotes(notes []*vault.Note, spec SortSpec) error {
	switch spec.Key {
	case "", SortPath:
	default:
		if !ValidSortKey(spec.Key) {
			return errors.New("queryexpr: unknown sort key " + quote(spec.Key))
		}
		return errors.New("queryexpr: sort key " + quote(spec.Key) + " is not ordered here; the caller must apply it")
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if spec.Descending {
			return notes[i].Path > notes[j].Path
		}
		return notes[i].Path < notes[j].Path
	})
	return nil
}
