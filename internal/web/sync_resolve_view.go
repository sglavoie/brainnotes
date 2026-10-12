package web

import (
	"sort"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
)

func syncPlanViewOf(plan *vaultimport.Plan, receipt propertyPreviewReceipt) syncPlanView {
	view := syncPlanView{
		OK:          true,
		PreviewID:   receipt.ID,
		ExpiresAt:   receipt.ExpiresAt.Format(time.RFC3339),
		PackageID:   plan.PackageID,
		OperationID: plan.OperationID,
		Paths:       make([]syncPlanPathView, 0, len(plan.Paths)),
	}
	for _, decided := range plan.Paths {
		path := syncPlanPathView{
			Path:   decided.Path,
			Kind:   string(decided.Kind),
			Choice: string(decided.Choice),
			Label:  syncChoiceLabels[decided.Choice],
			Steps:  make([]syncPlanStepView, 0, len(decided.Steps)),
		}
		for _, step := range decided.Steps {
			path.Steps = append(path.Steps, syncPlanStepView{
				Path:   step.Path,
				Action: string(step.Action),
				SHA256: step.SHA256,
			})
		}
		view.Paths = append(view.Paths, path)
	}
	return view
}

func syncResolvedViewOf(
	resolution *vaultimport.Resolution,
	plan *vaultimport.Plan,
	root string,
) syncResolvedView {
	view := syncResolvedView{
		OK:           true,
		PackageID:    resolution.PackageID,
		OperationID:  resolution.OperationID,
		JournalID:    resolution.JournalEntry,
		Paths:        resolution.Paths,
		Acknowledged: resolution.Applied != nil && resolution.Applied.Receipt != nil,
		Abandoned:    syncAbandonedViewsOf(resolution.AbandonedResolutions, root),
	}
	view.Summary = syncResolvedSummary(view, plan)
	return view
}

// syncResolvedSummary is what a finished resolution says it did, and every half of
// it is read off the resolution and the plan rather than assumed.
//
// A resolution with no journal entry is two different successes, and the plan is
// what tells them apart. A plan that revises nothing had no second commit to make —
// the ordinary outcome of taking the other replica's version, where the application
// is the whole of the decision — and claiming a recorded decision there would name
// something a person could go looking for in the history and not find. A plan that
// does revise something and still came back with no entry had every step of that
// revision already satisfied on disk, which is what a re-drive of a rolled-forward
// promise finds; saying it "needed nothing revised" there would deny the revision
// this vault is holding.
//
// An unacknowledged resolution is finished in this vault and unfinished between the
// replicas, which is the distinction the status line is also built on.
//
// Both of those sentences are claims about a plan, so neither may be made without
// one. A missing plan is not a state this surface can produce — the receipt carries
// the plan it was minted for — and the answer to it is the sentence that claims only
// what the resolution itself reports, exactly as the failure side reads an absent
// plan as the answer that does not tell somebody to stop looking.
func syncResolvedSummary(view syncResolvedView, plan *vaultimport.Plan) string {
	summary := "The change was applied and your decision was recorded."
	switch {
	case view.JournalID != "":
	case plan == nil:
		summary = "The change was applied."
	case !plan.Revises():
		summary = "The change was applied, and it needed nothing revised on top of it: what it " +
			"carries is what you chose."
	default:
		summary = "The change was applied, and the revision your decision asked for was already on " +
			"disk, so no second commit was made: this vault holds what you chose."
	}
	if !view.Acknowledged {
		summary += " The other replica has not been told yet and will keep offering it until it is."
	}
	return summary
}

// syncAbandonedViewsOf names the earlier promises a recovery pass dropped, with the
// engine's own reason for each — redacted, because these reasons are wrapped
// failures like any other and routinely carry a staged path under the vault root.
func syncAbandonedViewsOf(abandoned map[string]error, root string) []syncAbandonedView {
	if len(abandoned) == 0 {
		return nil
	}
	packages := make([]string, 0, len(abandoned))
	for packageID := range abandoned {
		packages = append(packages, packageID)
	}
	sort.Strings(packages)
	out := make([]syncAbandonedView, 0, len(packages))
	for _, packageID := range packages {
		out = append(out, syncAbandonedView{
			PackageID: packageID,
			// Through the same sentence helper as every other engine failure on this
			// surface: none of these reasons is an errors.Join today — the engine
			// carries the refusal as text rather than joining it — but syncAbandonedClause
			// puts this string inside a sentence beside the others, and a rule that holds
			// only for the ones that happen to be joins is one somebody has to re-derive.
			Reason: syncFailureSentence(abandoned[packageID], root),
		})
	}
	return out
}
