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
	if resolution != nil {
		// Something landed, whatever else failed: the package is applied and is
		// undoable. Everything this vault serves has to be re-read either way.
		s.finishPropertyWrite(true)
	}
	if err != nil {
		writeManagementError(w, syncPartialResolutionError(resolution, err, s.vault.Root))
		return
	}
	writeManagementJSON(w, http.StatusOK, syncResolvedViewOf(resolution))
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

func syncResolvedViewOf(resolution *vaultimport.Resolution) syncResolvedView {
	view := syncResolvedView{
		OK:           true,
		PackageID:    resolution.PackageID,
		OperationID:  resolution.OperationID,
		JournalID:    resolution.JournalEntry,
		Paths:        resolution.Paths,
		Acknowledged: resolution.Applied != nil && resolution.Applied.Receipt != nil,
		Abandoned:    syncAbandonedViewsOf(resolution.AbandonedResolutions),
		Summary:      "The change was applied and your decision was recorded.",
	}
	if !view.Acknowledged {
		view.Summary = "The change was applied and your decision was recorded. The other replica " +
			"has not been told yet and will keep offering it until it is."
	}
	return view
}

func syncAbandonedViewsOf(abandoned map[string]error) []syncAbandonedView {
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
		out = append(out, syncAbandonedView{PackageID: packageID, Reason: abandoned[packageID].Error()})
	}
	return out
}

// syncPartialResolutionError is the honest answer to a resolution that failed.
//
// A non-nil Resolution beside the error is the one partial outcome the engine
// produces, and it is not a generic failure: the peer's version is in this vault,
// which is the opposite of what the person asked for, and the decision is durable
// and will be finished by the next pass. Saying only "the resolution failed" would
// leave somebody looking at a note they had decided against with no idea why.
func syncPartialResolutionError(resolution *vaultimport.Resolution, err error, root string) error {
	if resolution == nil {
		return syncResolveError(err, root)
	}
	return noteops.WrapError(noteops.CodeInternal,
		"sync: the other replica's version was applied and your decision has not been recorded yet. "+
			"It is saved and will be finished automatically on the next synchronization pass",
		err)
}

// syncResolveError maps an engine failure onto the code a client acts on.
//
// The sentinels are asked first and separately from the write failures, because
// they are answers about the *decision* — it was planned against a state this
// vault has moved past, the conflict is gone, another import is running — and a
// client retries or re-reads for those. A write failure is about the vault.
func syncResolveError(err error, root string) error {
	message := "sync: " + withoutVaultRoot(err.Error(), root)
	if code, named := syncDecisionCode(err); named {
		return noteops.WrapError(code, message, err)
	}
	return noteops.WrapError(syncWriteCode(err), message, err)
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
	case errors.Is(err, vaultimport.ErrStrandedImport), errors.Is(err, vaultimport.ErrCorruptState):
		return noteops.CodeSafety, true
	}
	return "", false
}

// syncWriteCode classifies the failure of the writes a resolution makes. The
// order is the one internal/vaultimport states: a snapshot failure unwraps to a
// busy Git lock, so it has to be asked about first, and a rollback failure is
// asked before the conflict nested inside it.
func syncWriteCode(err error) noteops.ErrorCode {
	switch {
	case vaultgit.IsSnapshotFailure(err):
		return noteops.CodeSnapshotFailed
	case vaultgit.IsBlocked(err):
		return noteops.CodeSafety
	case vault.IsTxnRollbackFailure(err):
		return noteops.CodeSafety
	case vaultgit.IsBusy(err), vault.IsTxnLockBusy(err), vault.IsTxnConflict(err):
		return noteops.CodeConflict
	case vault.IsForbiddenMutation(err):
		return noteops.CodeForbidden
	}
	return noteops.CodeInternal
}
