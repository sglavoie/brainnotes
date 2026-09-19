package web

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
)

const (
	syncPreviewAPIRoutePath = "/api/sync/conflicts/preview"
	syncResolveAPIRoutePath = "/api/sync/conflicts/resolve"
)

// syncResolveCommand is what the undo journal records for a resolution decided in
// a browser. It names the person's action rather than the endpoint, the
// convention every management route here follows.
const syncResolveCommand = "bn serve sync resolve"

// syncMergeLimit bounds one merged version. It is the same bound the review route
// shows a version under, deliberately: a merge is composed from what that route
// carried, so a merge larger than that bound is content this surface never showed.
const syncMergeLimit = syncVersionTextLimit

// A resolution is a **preview-gated write whose precondition is the plan itself**,
// which is a third write class and is described in
// docs/internals/web-write-classes.md.
//
// The two classes already here both re-derive their work at apply time: a property
// or settings receipt carries the *request*, which is replanned and compared
// against pinned file digests, so an apply can never write what the preview did
// not show. This one carries the `*vaultimport.Plan` instead, and the reason is
// that the engine's own staleness check is stronger than anything this layer could
// reconstruct. `Resolve` re-asks every fact the plan was built on — the hold, each
// kind, both branches, and this vault's own current version of every path — and
// refuses as `ErrStalePreview` with nothing written; each step is then pinned again
// as a transaction precondition under the path's own lock. Replanning here would
// answer a weaker question twice and would have to reproduce the merge the person
// composed, which only the plan holds.
//
// So the receipt is one-use, kind-separated and TTL-bounded exactly as the others
// are — that is what stops a replay and a cross-endpoint redemption — and
// staleness is the engine's answer rather than this layer's.

// syncResolutionRecord is the plan a receipt was issued for.
//
// The plan is held as the value Preview returned rather than re-encoded, because
// the content each step installs is staged beside it and is unexported: a plan
// that had been through a serialization would have to re-read every version from
// the vault, and the one version it could never re-read is a merge only its author
// has.
type syncResolutionRecord struct {
	// PackageDir is where the held package sits in the incoming channel. Resolve
	// checks it against the plan, so a receipt cannot be redeemed against a
	// different package even if the store were confused about which it held.
	PackageDir string
	Plan       *vaultimport.Plan
}

// syncResolveRequest is a decision about every contested path of one package.
type syncResolveRequest struct {
	Token     string                  `json:"token"`
	PackageID string                  `json:"package_id"`
	Paths     []syncPathChoiceRequest `json:"paths"`
}

// syncPathChoiceRequest is one path's decision.
//
// There is deliberately no request-wide default. Choices has one, and it is the
// right shape for a command line where somebody types "accept everything
// incoming" as a sentence; here every contested path is on the screen with both
// versions beside it, so a path the request does not name is a path nobody looked
// at, and inheriting a destructive decision for it is exactly the mistake this
// surface exists to prevent.
type syncPathChoiceRequest struct {
	Path   string `json:"path"`
	Choice string `json:"choice"`
	// Merged is the assembled version for a manual merge. It is a pointer because
	// an empty merge is a legitimate decision — emptying a note — and is not the
	// same as not having supplied one.
	Merged *string `json:"merged"`
}

// syncPlanView is what a resolution will do, as a person reads it before
// authorizing it.
type syncPlanView struct {
	OK          bool               `json:"ok"`
	PreviewID   string             `json:"preview_id"`
	ExpiresAt   string             `json:"expires_at"`
	PackageID   string             `json:"package_id"`
	OperationID string             `json:"operation_id"`
	Paths       []syncPlanPathView `json:"paths"`
}

type syncPlanPathView struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Choice string `json:"choice"`
	// Label is the choice as a control says it, carried so the plan a person reads
	// before applying it is in the same words as the radio they chose. The slug is
	// the engine's name for the decision and reads as jargon in the one place
	// somebody is deciding whether to go ahead, and a second wording table in the
	// page would be a second thing to keep in step with sync_choice_wording.go.
	Label string `json:"label"`
	// Steps are the writes and removals the decision makes *after* the peer's
	// change is applied, which is where a derived path — the conflicted copy a
	// keep-both writes beside the note — becomes visible. A choice the applied
	// change already satisfies has none, and that is worth showing rather than
	// hiding: "take the other replica's version" revises nothing.
	Steps []syncPlanStepView `json:"steps"`
}

type syncPlanStepView struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	SHA256 string `json:"sha256,omitempty"`
}

// syncResolvedView is what a resolution left behind.
type syncResolvedView struct {
	OK          bool     `json:"ok"`
	PackageID   string   `json:"package_id"`
	OperationID string   `json:"operation_id"`
	JournalID   string   `json:"journal_id,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	// Acknowledged reports whether the peer has been told the change was applied.
	// A resolution that succeeded without it is complete in this vault and the
	// peer will keep offering the package until an acknowledgment reaches it.
	Acknowledged bool `json:"acknowledged"`
	// Abandoned names earlier decisions this call's recovery pass dropped, with
	// the reason for each. They are reported because once the pass has run the
	// vault no longer remembers them, and the person reading this is usually the
	// one whose earlier attempt was dropped.
	Abandoned []syncAbandonedView `json:"abandoned,omitempty"`
	Summary   string              `json:"summary"`
}

type syncAbandonedView struct {
	PackageID string `json:"package_id"`
	Reason    string `json:"reason"`
}

// handleSyncPreview plans a resolution and issues the one-use receipt the resolve
// endpoint redeems. It writes nothing to the vault.
func (s *Server) handleSyncPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, syncPreviewAPIRoutePath) {
		return
	}
	req, err := s.parseSyncResolveRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	held, err := s.heldPackage(req.PackageID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	choices, err := req.choices(held.Conflict)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	plan, err := vaultimport.Preview(s.vault.Root, req.PackageID, choices)
	if err != nil {
		writeManagementError(w, syncResolveError(err, s.vault.Root))
		return
	}
	receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
		Kind:       propertyPreviewResolution,
		Resolution: syncResolutionRecord{PackageDir: held.Dir, Plan: plan},
	})
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeManagementJSON(w, http.StatusOK, syncPlanViewOf(plan, receipt))
}

// handleSyncResolve redeems a resolution receipt: it applies the held package and
// records the decision as a revision of it.
//
// The partial outcome is reported rather than smoothed over. A resolution can
// apply the peer's change and fail to record the decision, and that is the one
// state where the vault holds a version somebody decided against — so it is a
// distinct failure that says the change landed and the decision will be finished
// automatically, never a generic write error and never a success.
func (s *Server) handleSyncResolve(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, syncResolveAPIRoutePath) {
		return
	}
	req, err := s.parsePropertyApplyRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewResolution)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	resolution, err := vaultimport.Resolve(
		s.vault.Root, record.Resolution.PackageDir, record.Resolution.Plan,
		vaultimport.Options{Command: syncResolveCommand})
	// Unconditionally, because a nil resolution does not mean nothing landed: an
	// application that failed after committing has its own in-process recovery, which
	// can finish the whole resolution on the way out and leaves the error with no
	// Resolution to carry. The cost of asking when nothing changed is one reload; the
	// cost of not asking when something did is a page serving the version somebody
	// decided against.
	s.finishPropertyWrite(true)
	if err != nil {
		writeManagementError(w, syncPartialResolutionError(
			resolution, record.Resolution.Plan, err, s.vault.Root))
		return
	}
	writeManagementJSON(w, http.StatusOK,
		syncResolvedViewOf(resolution, record.Resolution.Plan, s.vault.Root))
}

// heldPackage finds one package this vault is holding for a decision.
//
// It is read through Inspect rather than from the state file directly, because
// the resolution needs the package's place in the incoming channel and that is a
// fact about the channel. A package that is in the state file and not in the
// channel is not resolvable — there is nothing left to apply — and says so here
// rather than failing inside Resolve.
func (s *Server) heldPackage(packageID string) (vaultimport.Pending, error) {
	pending, err := vaultimport.Inspect(s.vault.Root)
	if err != nil {
		return vaultimport.Pending{}, noteops.WrapError(noteops.CodeInternal,
			"sync: the incoming channel could not be read", err)
	}
	for _, entry := range pending {
		if entry.PackageID != packageID {
			continue
		}
		if entry.Reason != vaultimport.HoldConflict || entry.Conflict == nil {
			return vaultimport.Pending{}, noteops.Errorf(noteops.CodeConflict,
				"sync: %s is not waiting for a decision", packageID)
		}
		return entry, nil
	}
	return vaultimport.Pending{}, noteops.Errorf(noteops.CodeNotFound,
		"sync: %s is not in this vault's incoming channel", packageID)
}

func (s *Server) parseSyncResolveRequest(r *http.Request) (syncResolveRequest, error) {
	var req syncResolveRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return syncResolveRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return syncResolveRequest{}, err
	}
	req.Token = ""
	req.PackageID = strings.TrimSpace(req.PackageID)
	if req.PackageID == "" {
		return syncResolveRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "sync: package_id is required")
	}
	if len(req.Paths) == 0 {
		return syncResolveRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "sync: no decision was made")
	}
	return req, nil
}

// choices turns the request into the engine's decision, and refuses one that does
// not decide every contested path.
//
// The completeness check is here and not in the engine because it is a fact about
// this surface: Choices.Default exists precisely so a caller can decide the rest
// in one word, and a browser showing every path individually has no business using
// it. Refusing early also means the message names the path nobody decided.
func (req syncResolveRequest) choices(record *vaultimport.ConflictRecord) (vaultimport.Choices, error) {
	choices := vaultimport.Choices{
		Paths:  make(map[string]vaultimport.Choice, len(req.Paths)),
		Merged: map[string][]byte{},
	}
	for _, decided := range req.Paths {
		choice, err := decided.choice()
		if err != nil {
			return vaultimport.Choices{}, err
		}
		if _, twice := choices.Paths[decided.Path]; twice {
			return vaultimport.Choices{}, noteops.Errorf(noteops.CodeInvalidRequest,
				"sync: %s was decided twice", decided.Path)
		}
		choices.Paths[decided.Path] = choice
		if decided.Merged != nil {
			choices.Merged[decided.Path] = []byte(*decided.Merged)
		}
	}
	for _, contested := range record.Paths {
		if _, decided := choices.Paths[contested.Path]; !decided {
			return vaultimport.Choices{}, noteops.Errorf(noteops.CodeInvalidRequest,
				"sync: %s is contested and was not decided", contested.Path)
		}
	}
	return choices, nil
}

// choice validates one decision. The choice has to be one of the four the engine
// names, because an unrecognized one would otherwise reach Preview as a path it
// refuses for an unhelpful reason.
func (decided syncPathChoiceRequest) choice() (vaultimport.Choice, error) {
	if strings.TrimSpace(decided.Path) == "" {
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "sync: a decision named no path")
	}
	choice := vaultimport.Choice(strings.TrimSpace(decided.Choice))
	if !syncKnownChoice(choice) {
		return "", noteops.Errorf(noteops.CodeInvalidRequest,
			"sync: %q is not a resolution", decided.Choice)
	}
	if err := decided.validMerge(choice); err != nil {
		return "", err
	}
	return choice, nil
}

// validMerge holds a merge to what this surface can have shown. The bytes are
// required to be text because they arrive as a JSON string and the editor that
// composed them is a text editor; the bound is the one the review route reads a
// version under.
func (decided syncPathChoiceRequest) validMerge(choice vaultimport.Choice) error {
	if choice != vaultimport.ManualMerge {
		return nil
	}
	if decided.Merged == nil {
		return noteops.Errorf(noteops.CodeInvalidRequest,
			"sync: merging %s requires the merged text", decided.Path)
	}
	if len(*decided.Merged) > syncMergeLimit {
		return noteops.Errorf(noteops.CodeInvalidRequest,
			"sync: the merge of %s is larger than this page can carry", decided.Path)
	}
	if !utf8.ValidString(*decided.Merged) {
		return noteops.Errorf(noteops.CodeInvalidRequest,
			"sync: the merge of %s is not valid text", decided.Path)
	}
	return nil
}

func syncKnownChoice(choice vaultimport.Choice) bool {
	for _, known := range syncChoiceLabelOrder {
		if known == choice {
			return true
		}
	}
	return false
}

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

// syncPartialResolutionError is the honest answer to a resolution that failed.
//
// A non-nil Resolution beside the error says something landed, and there are two
// such states rather than one — Resolve returns the resolution it built for both
// the application's own tail failure and the revision's — so the answer reads
// Resolution.JournalEntry to tell them apart. A revision that never committed is
// the state where this vault holds a version somebody decided against, and it says
// so. A revision that did commit is a vault that holds exactly what was asked for,
// and telling that person "the resolution failed" would send them looking for
// damage there is none of; what failed is afterwards, and the usual afterwards is
// the acknowledgment the peer is owed.
//
// The engine's own sentence is kept in either case, because what failed is not
// derivable from either state, and the code is the same syncResolutionCode the
// ordinary refusal uses: a sentinel means the same thing to a client whether or not
// a resolution came back beside it, and a refused decision in particular is a
// conflict either way.
func syncPartialResolutionError(
	resolution *vaultimport.Resolution,
	plan *vaultimport.Plan,
	err error,
	root string,
) error {
	if resolution == nil {
		return syncResolveError(err, root)
	}
	message := "sync: " + syncPartialOutcome(resolution, plan, err) + " What failed: " +
		syncFailureSentence(err, root) +
		syncAbandonedClause(resolution.AbandonedResolutions, root)
	return noteops.WrapError(syncPartialResolutionCode(err), message, err)
}

// syncPartialResolutionCode is syncResolutionCode with the one answer a partial
// outcome cannot give.
//
// CodeSafety is documented as the engine refusing the operation *before it changed
// anything*, and a resolution that came back at all applied the other replica's
// version — so on this path that code is false whatever raised it. It is reachable:
// the revision's own noteops.BeginOperation preflight runs after the application
// committed, and a vault another process latched in between refuses it there as a
// blocked safety failure rather than as a snapshot one.
//
// Everything else stays shared, which is the point of syncResolutionCode: this
// narrows one code whose meaning is a claim about the vault rather than about the
// decision, and CodeInternal is what the other half-applied failure on this surface
// answers for the same reason — the code says how much a client may assume, and what
// gets a person to the repair is syncPartialOutcome's sentence.
func syncPartialResolutionCode(err error) noteops.ErrorCode {
	if code := syncResolutionCode(err); code != noteops.CodeSafety {
		return code
	}
	return noteops.CodeInternal
}

// syncPartialOutcome is what is durable, for a resolution that came back beside an
// error. It is a sentence about this vault and never about what to do, which is
// syncPartialResolutionError's business.
//
// An empty JournalEntry is five states rather than one — six answers, because one of
// the five is answered two ways — and only the plan and the failure together say
// which. A plan that revises nothing has no second commit to
// make — accepting the other replica's version is carried out by applying it — so
// there is nothing outstanding and no promise anybody is keeping. A plan that does
// revise something and got no entry has a durable promise the next pass finishes,
// unless the revision failed on one of the three sentinels that abandon it, in
// which case no pass will ever make that commit and this vault is left holding the
// version somebody decided against. Telling those apart is the difference between a
// person waiting for a commit that is coming, waiting for one that is not, and
// waiting for one that was never owed. The promise that survives is the state with
// two answers, and what splits it is the failure rather than the plan: a vault that
// latched a block refuses the very write the next pass would make, so the promise is
// outstanding there and coming to nothing until somebody clears it. That is a second
// sentence for one state, not a sixth state — the vault is holding exactly what it
// holds for the promise that is coming.
//
// Two failures are neither, and both are asked first because they are answers about
// the vault that no journal entry is ever written for. A snapshot failure means the
// decision's bytes and its undo manifest are both durable (noteops.Finalize writes
// the manifest before it snapshots) and the content history is not — and whether it
// latched the block is the error's own answer rather than a property of the type:
// noteops fills Blocked in from vaultgit.Blocks, which is false for a snapshot a
// lock somebody else was holding merely refused.
// The two need opposite sentences. A latched block refuses the next write, so the
// promise the state file still holds cannot be kept and "it will be finished
// automatically" is exactly wrong; an unlatched one leaves a finalized entry the
// next pass re-snapshots — revision.commit's own first case — so saying this vault
// refuses further changes would send somebody to clear a block that is not there.
// The unlatched sentence still owes the stall, because an unlatched kind is not
// necessarily a transient one: a busy lock clears itself, a repository mid-rebase
// and a git nobody can run do not, and finishResolutions runs ahead of every
// import, drain and resolution, so until that snapshot succeeds this vault applies
// nothing at all.
// A stranded import means a half-made revision could not be taken back out, and that
// is the one refusal that stops every later import until a person looks.
func syncPartialOutcome(
	resolution *vaultimport.Resolution,
	plan *vaultimport.Plan,
	err error,
) string {
	var snapshot *noteops.SnapshotError
	if errors.As(err, &snapshot) {
		if snapshot.Blocked {
			return "your decision is on disk and is undoable, and the content history was not " +
				"updated: this vault refuses further changes until that is cleared, so nothing is " +
				"finished automatically while it stands." + syncPeerClause(resolution)
		}
		return "your decision is on disk and is undoable, and the content history was not updated: " +
			"nothing is blocked, so the next synchronization pass commits it — and until it does, " +
			"this vault applies nothing else." + syncPeerClause(resolution)
	}
	if errors.Is(err, vaultimport.ErrStrandedImport) {
		return "the other replica's version was applied, your decision was left half recorded, and " +
			"this vault could not put that half back: synchronization is stopped until somebody " +
			"resolves it, and no pass will finish it in the meantime."
	}
	// A record with no plan is not a state this surface can produce — a receipt
	// carries the plan it was minted for — and an absent one is read as a promise
	// still outstanding, which is the answer that sends somebody to look rather
	// than the answer that tells them to stop looking.
	if plan != nil && !plan.Revises() {
		return "the change was applied and there was nothing to record on top of it, because what " +
			"it carries is what you chose. It is undoable." + syncPartialTail(resolution)
	}
	if resolution.JournalEntry == "" {
		if vaultimport.IsUnrecordableDecision(err) {
			return "the other replica's version was applied and your decision could not be recorded " +
				"on top of it, and it will not be tried again: this vault is holding the version you " +
				"decided against, and the change is undoable."
		}
		// A latched block is the other way the promise cannot be kept, and it is the
		// snapshot answer above one step earlier: the revision's own preflight is
		// refused, so there is no entry and no snapshot failure to read it off — the
		// application landed and the second commit never started. The promise does
		// survive, which is why this is not an abandonment, and saying a later pass
		// finishes it would still be wrong: the pass is refused the same write, and
		// finishResolutions running ahead of every import means nothing else is
		// applied either while it stands.
		if vaultgit.IsBlocked(err) {
			return "the other replica's version was applied and your decision has not been recorded " +
				"yet: this vault refuses further changes until that is cleared, so no pass records it " +
				"while it stands, and nothing else is applied either." + syncPeerClause(resolution)
		}
		return "the other replica's version was applied and your decision has not been recorded yet. " +
			"It is saved and will be finished automatically on the next synchronization pass."
	}
	return "the change was applied and your decision was recorded, and both are undoable." +
		syncPartialTail(resolution)
}

// syncPartialTail is what is still owed after the commits that did land: the
// acknowledgment the peer has not been given, or nothing this sentence can name.
func syncPartialTail(resolution *vaultimport.Resolution) string {
	if clause := syncPeerClause(resolution); clause != "" {
		return clause
	}
	return " What failed came afterwards."
}

// syncPeerClause is the acknowledgment half of that tail on its own, for the
// answers that already name what failed and must not say it came afterwards.
//
// It is reachable there: resolveLocked attempts the revision even when the
// application's own tail failed, so a receipt that could not be published and a
// snapshot that then failed arrive together as one joined error, and the redelivery
// is the part of it nobody would otherwise be told about.
func syncPeerClause(resolution *vaultimport.Resolution) string {
	if resolution.Applied != nil && resolution.Applied.Receipt == nil {
		return " The other replica has not been told, so it will keep offering the change until it is."
	}
	return ""
}

// syncAbandonedClause carries earlier decisions this call's recovery pass dropped
// into a failure message.
//
// A successful resolution reports them on its view. A failed one has nowhere else
// to put them: the pass has already run, so the state file no longer holds the
// promise it dropped, and the person reading this failure is usually the one whose
// earlier attempt was the promise.
func syncAbandonedClause(abandoned map[string]error, root string) string {
	views := syncAbandonedViewsOf(abandoned, root)
	if len(views) == 0 {
		return ""
	}
	dropped := make([]string, 0, len(views))
	for _, view := range views {
		dropped = append(dropped, view.PackageID+" ("+view.Reason+")")
	}
	return ". Dropped on the way in, from an earlier attempt: " + strings.Join(dropped, "; ")
}

// syncResolveError maps an engine failure onto the code a client acts on.
//
// The sentinels are asked first and separately from the write failures, because
// they are answers about the *decision* — it was planned against a state this
// vault has moved past, the conflict is gone, another import is running — and a
// client retries or re-reads for those. A write failure is about the vault.
func syncResolveError(err error, root string) error {
	message := "sync: " + syncFailureSentence(err, root)
	return noteops.WrapError(syncResolutionCode(err), message, err)
}

// syncFailureSentence is the engine's own words, with this vault's root taken out
// and a joined failure put on one line.
//
// Both of the engine's answers here can be an errors.Join — an application tail
// failure beside the revision's, and a pass that dropped an earlier promise beside
// whatever it failed on — and Join separates with a newline. These messages are
// read out of a JSON field and put in a sentence on a page, where a raw newline is
// one statement that looks like two, with the second one missing its subject.
func syncFailureSentence(err error, root string) string {
	return strings.ReplaceAll(withoutVaultRoot(err.Error(), root), "\n", "; ")
}

// syncResolutionCode is the one classification both resolution failures use, so a
// sentinel cannot mean one thing when the call came back empty-handed and another
// when a resolution came back beside it. ErrConflict is the reason it has to be one
// function: it is what a client retries or re-reads for, and syncWriteCode alone
// would call a decision the vault refused an internal error.
func syncResolutionCode(err error) noteops.ErrorCode {
	if code, named := syncDecisionCode(err); named {
		return code
	}
	return syncWriteCode(err)
}

func syncDecisionCode(err error) (noteops.ErrorCode, bool) {
	switch {
	case errors.Is(err, vaultimport.ErrStalePreview):
		return noteops.CodePreviewStale, true
	case errors.Is(err, vaultimport.ErrNoConflict), errors.Is(err, vaultimport.ErrImportBusy),
		errors.Is(err, vaultimport.ErrConflict):
		return noteops.CodeConflict, true
	case errors.Is(err, vaultimport.ErrResolutionUnavailable):
		return noteops.CodeInvalidRequest, true
	// Named here rather than left to syncWriteCode so the answer cannot drift onto
	// something nested inside them, and CodeInternal rather than CodeSafety for the
	// reason syncWriteCode gives: a stranded import is a *half-applied* change —
	// the package is on disk, the peer has been told to drop its copy, and the
	// revision is half recorded — which is the negation of what CodeSafety is
	// documented to mean. What gets a person to the repair is the sentence
	// syncPartialOutcome writes, not the code.
	case errors.Is(err, vaultimport.ErrStrandedImport), errors.Is(err, vaultimport.ErrCorruptState):
		return noteops.CodeInternal, true
	}
	return "", false
}

// syncWriteCode classifies the failure of the writes a resolution makes. The
// order is the one internal/vaultimport states: a snapshot failure unwraps to a
// busy Git lock, so it has to be asked about first, and a rollback failure is
// asked before the conflict nested inside it.
//
// A transaction that could not put content back is in that same group and for the
// same reason: it unwraps to the mutation that failed — often a conflict — and
// answering "retry" would bury the one sentence saying which file somebody's
// content is sitting in. internal/noteops and the property class both ask it
// before the conflict too, and both answer it with CodeInternal rather than
// CodeSafety — which this follows, because CodeSafety is documented as the engine
// refusing *before* anything changed, and a rollback that failed is the one state
// where content is provably somewhere nobody asked for it. The distinction that
// matters to a client here is carried by the message, which names the recovery
// file, and by CodeSnapshotFailed for the other post-write failure.
func syncWriteCode(err error) noteops.ErrorCode {
	switch {
	case vaultgit.IsSnapshotFailure(err):
		return noteops.CodeSnapshotFailed
	case vaultgit.IsBlocked(err):
		return noteops.CodeSafety
	case vault.IsTxnRollbackFailure(err), vault.IsTxnRecoveryNeeded(err):
		return noteops.CodeInternal
	case vaultgit.IsBusy(err), vault.IsTxnLockBusy(err), vault.IsTxnConflict(err):
		return noteops.CodeConflict
	case vault.IsForbiddenMutation(err):
		return noteops.CodeForbidden
	}
	return noteops.CodeInternal
}
