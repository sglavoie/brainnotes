package queryexpr

import (
	"github.com/sglavoie/brainnotes/internal/config"
	"strings"
)

// View is one config.SavedView resolved against the vault: either a compiled
// plan or the error that disabled it.
//
// This type exists because internal/config cannot validate a saved query.
// config is the bottom of the layering, so importing this package to check that
// a view's expression parses would close a cycle — the same edge internal/pins
// must not close with internal/noteops. The cost is that a view whose
// expression rotted (a property it names was renamed, a field was removed)
// loads without complaint, and the fix is that resolution happens exactly here,
// once, for every consumer: `bn views`, `bn views check`, `--view`, GET
// /api/views, and the saved-views nav.
//
// The contract every consumer owes the user is that such a view is rendered as
// a *disabled* entry carrying its error. It is never dropped silently — a view
// that vanished from the list looks like a view the user never saved — and it
// is never fatal: one stale view must not fail a request or a command that
// merely lists views. ResolveViews is what makes that the default: it returns
// one View per saved view, always, in file order.
type View struct {
	// Saved is the stored view, verbatim.
	Saved config.SavedView
	// Plan is the compiled query, ordering, and cap. It is nil exactly when
	// Err is non-nil.
	Plan *Plan
	// Err is why the view is disabled: a *ParseError for a query that no
	// longer compiles (so a CLI can Render carets under the offending text), or
	// a plain error for an unusable sort spec.
	Err error
}

// Enabled reports whether the view resolved cleanly and can be run.
func (v View) Enabled() bool { return v.Err == nil }

// Name is the view's name, for callers that hold only the resolved value.
func (v View) Name() string { return v.Saved.Name }

// Detail renders why a disabled view is disabled, preferring a ParseError's
// caret block so the user sees which part of their saved query rotted. It is
// the empty string for an enabled view.
func (v View) Detail() string {
	if v.Err == nil {
		return ""
	}
	if parseErr, ok := v.Err.(*ParseError); ok {
		return parseErr.Render()
	}
	return v.Err.Error()
}

// ResolveViews resolves every saved view against env, in file order. The result
// always has one entry per input: a view that failed to compile comes back
// disabled with its error rather than being omitted.
func ResolveViews(views []config.SavedView, env Env) []View {
	if len(views) == 0 {
		return nil
	}
	out := make([]View, 0, len(views))
	for _, saved := range views {
		out = append(out, ResolveView(saved, env))
	}
	return out
}

// ResolveView compiles one saved view. Its Query becomes the plan expression,
// its Sort and RelativeTo the plan's SortSpec, and its Limit the plan's cap.
//
// RelativeTo is read from its own field rather than from the sort text because
// SortSpec.String() deliberately drops it: a relevance sort's anchor names a
// note, not an ordering, so it could not survive a round trip through the sort
// spelling.
//
// Errors are returned on the View rather than to the caller. A caller that
// wants to fail on a bad view (`--view` naming one, say) checks Err itself;
// every caller that merely lists views gets the disabled entry for free.
func ResolveView(saved config.SavedView, env Env) View {
	view := View{Saved: saved}

	spec, err := ParseSort(saved.Sort)
	if err != nil {
		view.Err = err
		return view
	}
	spec.RelativeTo = saved.RelativeTo
	if spec.Key == SortRelevance && spec.RelativeTo == "" {
		view.Err = &viewError{name: saved.Name, msg: "sort \"" + SortRelevance + "\" requires relative_to"}
		return view
	}
	if spec.RelativeTo != "" && spec.Key != SortRelevance {
		view.Err = &viewError{name: saved.Name, msg: "relative_to requires sort \"" + SortRelevance + "\""}
		return view
	}

	plan, err := Compile(saved.Query, env)
	if err != nil {
		view.Err = err
		return view
	}
	if saved.Search != "" {
		text := &Term{Value: Value{Kind: ValueAtom, Atoms: []Atom{{Text: saved.Search}}}}
		if strings.TrimSpace(saved.Query) == `""` {
			plan.Expr = text
		} else {
			plan.Expr = &And{Operands: []Expr{plan.Expr, text}}
		}
	}
	spec.Search = saved.Search
	plan.Sort = spec
	plan.Limit = saved.Limit
	view.Plan = plan
	return view
}

// viewError is a saved view that is well-formed as config but unusable as a
// query for a reason that has no source span — a sort/relative_to disagreement
// rather than a parse failure. It is deliberately not a *ParseError: there is
// no column in the saved query to point at.
type viewError struct {
	name string
	msg  string
}

func (e *viewError) Error() string { return "view \"" + e.name + "\": " + e.msg }
