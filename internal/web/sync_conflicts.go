package web

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
)

const syncConflictsAPIRoutePath = "/api/sync/conflicts"

// syncVersionTextLimit is how much of one version this route will carry.
//
// It is a bound on the response rather than a judgement about the note: a review
// page shows three versions of every contested path at once, so an unbounded read
// would let one large note decide the size of the whole payload. A version over
// the bound is reported as present and unshowable, which is also what makes the
// merge editor refuse it — see syncMergeable.
const syncVersionTextLimit = 512 << 10

// syncConflictsView is every package this vault is holding for a decision.
//
// It carries the versions themselves and not only their digests, because the one
// thing a person resolving a conflict has to do is read both versions, and a page
// that had to fetch each one separately would be showing a list of hashes while
// the fetches landed.
type syncConflictsView struct {
	OK bool `json:"ok"`
	// Operations are the held packages, in the order the channel is drained.
	Operations []syncConflictOperationView `json:"operations"`
}

// syncConflictOperationView is one held package: what it disagrees about, and
// what else in the channel is waiting behind it.
type syncConflictOperationView struct {
	PackageID    string                  `json:"package_id"`
	OperationID  string                  `json:"operation_id"`
	OriginDevice string                  `json:"origin_device"`
	Sequence     uint64                  `json:"sequence"`
	DetectedAt   string                  `json:"detected_at,omitempty"`
	Paths        []syncContestedPathView `json:"paths"`
	// Blocked are the packages this one is holding up, directly or through
	// another. They are not a detail: a person deciding whether to deal with this
	// now is deciding about those changes too, and nothing else on the page would
	// tell them so.
	Blocked []syncBlockedView `json:"blocked,omitempty"`
	// Problem is why this hold could not be described, empty for the ordinary
	// case. An operation carrying one has no paths and cannot be resolved from
	// here; see syncOperationView.
	Problem string `json:"problem,omitempty"`
}

// syncBlockedView is one package waiting behind a conflict.
type syncBlockedView struct {
	PackageID    string `json:"package_id"`
	OriginDevice string `json:"origin_device"`
	Sequence     uint64 `json:"sequence"`
	// Reason and Detail are the hold as the engine states it. WaitingOn is the
	// package it *directly* waits on, which is the conflict itself for a direct
	// dependent and an intermediate package for the rest — so a chain reads as a
	// chain rather than as five things blocked by one.
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
	WaitingOn string `json:"waiting_on,omitempty"`
}

// syncContestedPathView is one contested path with both versions and the
// decisions it can take.
type syncContestedPathView struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Summary is what the disagreement is, in one sentence.
	Summary string `json:"summary"`
	// RenamedTo and Collides are the second path the kind involves, empty for the
	// kinds that involve one.
	RenamedTo string          `json:"renamed_to,omitempty"`
	Collides  string          `json:"collides,omitempty"`
	Local     syncVersionView `json:"local"`
	Remote    syncVersionView `json:"remote"`
	Baseline  syncVersionView `json:"baseline"`
	// LocalChanged reports that this vault's version is not the one the hold
	// recorded, which means somebody edited the path after the conflict was
	// detected. The decision is made against the version shown here — Preview pins
	// this reading — but a person who wrote that edit should be told it is in play.
	LocalChanged bool `json:"local_changed"`
	// Available and Refused are the four choices, sorted by whether this conflict
	// can take them. A refused one is carried with its reason rather than dropped,
	// so the page can show why "keep both" is not on offer here.
	Available []syncChoiceView `json:"available"`
	Refused   []syncChoiceView `json:"refused,omitempty"`
	// Mergeable reports whether a manual merge can actually be composed here:
	// the choice is available and both versions are text this response carried.
	Mergeable bool `json:"mergeable"`
	// Remedy is what to do when no choice is on offer, empty when one is.
	Remedy string `json:"remedy,omitempty"`
}

// syncChoiceView is one of the four decisions, as a person reads it.
type syncChoiceView struct {
	Choice string `json:"choice"`
	Label  string `json:"label"`
	// Effect is what the choice does, set on an available choice.
	Effect string `json:"effect,omitempty"`
	// Discards names the version that stops being in this vault — "local" or
	// "remote" — and is empty for a choice that keeps both.
	//
	// It is a fact rather than a warning level, and the wording of the warning is
	// left to the page. What it deliberately does not say is that the discarded
	// version stays recoverable: the resolution's first commit puts the peer's
	// version in the content history and the hold's retained copy is discarded
	// when the hold is released, so which of the two survives where depends on
	// whether this vault's own edit had been committed yet. Promising recovery
	// here would be a promise this layer cannot keep.
	Discards string `json:"discards,omitempty"`
	// Reason is why the choice is not on offer, set on a refused choice.
	Reason string `json:"reason,omitempty"`
}

// syncVersionView is one version of a contested path.
//
// Exists and Shown are separate because three situations reach this page and only
// one of them is "here it is": a branch that is a deletion has no content by
// definition, a baseline older than the content history was never kept, and a
// version over syncVersionTextLimit or holding bytes that are not text cannot be
// put in a JSON string. Collapsing them would make a deletion look like a read
// failure.
type syncVersionView struct {
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256,omitempty"`
	// Shown is whether Text carries this version.
	Shown bool `json:"shown"`
	// Text is the version, present only when Shown.
	Text string `json:"text,omitempty"`
	// Size is the version's length in bytes, whether or not it is shown.
	Size int `json:"size"`
	// Binary and TooLarge are why it is not shown, when it exists and is not.
	Binary   bool `json:"binary,omitempty"`
	TooLarge bool `json:"too_large,omitempty"`
}

// handleSyncConflicts reports every conflict this vault is holding. It reads and
// writes nothing: vaultimport.Inspect and vaultimport.Offers both take no lock,
// which is what lets this answer while the companion is working in the vault.
//
// The cost of that is stated where it belongs — in the engine — and it is the
// reason this is not the last word on anything: the answer is a snapshot, and a
// decision made from it is checked again by Preview and once more by Resolve.
func (s *Server) handleSyncConflicts(w http.ResponseWriter, r *http.Request) {
	if !requireSyncGet(w, r, syncConflictsAPIRoutePath) {
		return
	}
	pending, err := vaultimport.Inspect(s.vault.Root)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal,
			"sync: the incoming channel could not be read", err))
		return
	}
	view := syncConflictsView{OK: true, Operations: []syncConflictOperationView{}}
	blocked := syncBlockedIndex(pending)
	for _, held := range pending {
		if held.Reason != vaultimport.HoldConflict || held.Conflict == nil {
			continue
		}
		operation, holding := s.syncOperationView(held, blocked)
		if !holding {
			continue
		}
		view.Operations = append(view.Operations, operation)
	}
	writeManagementJSON(w, http.StatusOK, view)
}

// syncOperationView describes one held package, and reports whether it is still
// held at all.
//
// A failure here is carried on the operation rather than failing the response, so
// one damaged hold does not take the resolvable conflicts down with it: the page's
// whole purpose is to let somebody act, and a vault with a corrupt record is
// precisely the vault where that matters most.
func (s *Server) syncOperationView(
	held vaultimport.Pending,
	blocked map[string][]syncBlockedView,
) (syncConflictOperationView, bool) {
	record := held.Conflict
	operation := syncConflictOperationView{
		PackageID:    record.PackageID,
		OperationID:  record.OperationID,
		OriginDevice: record.OriginDevice,
		Sequence:     record.Sequence,
		DetectedAt:   syncTimestamp(record.DetectedAt),
		Paths:        []syncContestedPathView{},
		Blocked:      blocked[record.PackageID],
	}
	offers, err := vaultimport.Offers(s.vault.Root, held.PackageID)
	switch {
	case errors.Is(err, vaultimport.ErrNoConflict):
		// A drain applied it, or somebody resolved it from the CLI, between the scan
		// and this read. It is not a failure and there is nothing left to show.
		return syncConflictOperationView{}, false
	case err != nil:
		operation.Problem = sanitizedSyncProblem(err, s.vault.Root)
		return operation, true
	}
	for _, offer := range offers {
		operation.Paths = append(operation.Paths, s.syncPathView(record.PackageID, offer))
	}
	return operation, true
}

// sanitizedSyncProblem is an engine failure as this route reports it: the message,
// with this vault's absolute location taken out of it for the reason
// syncWorkerViewsOf gives.
func sanitizedSyncProblem(err error, root string) string {
	return withoutVaultRoot(err.Error(), root)
}

func (s *Server) syncPathView(packageID string, offer vaultimport.Offer) syncContestedPathView {
	contested := offer.Contested
	view := syncContestedPathView{
		Path:         contested.Path,
		Kind:         string(contested.Kind),
		Summary:      syncConflictSummaries[contested.Kind],
		RenamedTo:    contested.RenamedTo,
		Collides:     contested.Collides,
		Local:        s.syncVersionView(packageID, contested, offer.Local),
		Remote:       s.syncVersionView(packageID, contested, contested.Remote),
		Baseline:     s.syncVersionView(packageID, contested, contested.Baseline),
		LocalChanged: offer.Local.SHA256 != contested.Local.SHA256,
		Available:    make([]syncChoiceView, 0, len(offer.Available)),
	}
	for _, choice := range offer.Available {
		view.Available = append(view.Available, syncAvailableChoice(contested.Kind, choice))
	}
	for _, choice := range syncChoiceLabelOrder {
		if reason, refused := offer.Refused[choice]; refused {
			view.Refused = append(view.Refused, syncChoiceView{
				Choice: string(choice), Label: syncChoiceLabels[choice], Reason: reason,
			})
		}
	}
	view.Mergeable = syncMergeable(offer, view)
	if len(view.Available) == 0 {
		view.Remedy = syncConflictRemedies[contested.Kind]
	}
	return view
}

// syncVersionView reads one version, or says why it is not being shown.
//
// Every branch goes through vaultimport.Version, which decides between the disk
// and the hold's retained copy by digest, so a version this vault has since
// edited still reads as the version the conflict is about.
func (s *Server) syncVersionView(
	packageID string,
	contested vaultimport.PathConflict,
	branch vaultimport.Branch,
) syncVersionView {
	view := syncVersionView{Exists: branch.Exists, SHA256: branch.SHA256}
	if !branch.Exists {
		return view
	}
	content := vaultimport.Version(s.vault.Root, packageID, contested, branch)
	if content == nil {
		return view
	}
	view.Size = len(content)
	switch {
	case len(content) > syncVersionTextLimit:
		view.TooLarge = true
	case !utf8.Valid(content):
		view.Binary = true
	default:
		view.Shown, view.Text = true, string(content)
	}
	return view
}

// syncMergeable reports whether a merge can be composed from what was sent.
//
// A merge editor needs both versions as text, so a manual merge the engine would
// accept is still not offerable when one side is an attachment or is over the
// response bound. The engine's answer is necessary and this is the rest of it;
// a page that offered the editor anyway would ask somebody to retype a PNG.
func syncMergeable(offer vaultimport.Offer, view syncContestedPathView) bool {
	if !syncOffers(offer, vaultimport.ManualMerge) {
		return false
	}
	return view.Local.showable() && view.Remote.showable()
}

func syncOffers(offer vaultimport.Offer, choice vaultimport.Choice) bool {
	for _, available := range offer.Available {
		if available == choice {
			return true
		}
	}
	return false
}

// showable is a version that either has content this response carried or has no
// content to carry. A deletion is mergeable — the merge is what replaces it.
func (v syncVersionView) showable() bool {
	return !v.Exists || v.Shown
}

// syncBlockedIndex maps each package to everything waiting behind it.
//
// Pending.Blocker names one package, so the dependents of a conflict are the
// transitive closure of the inverse relation. It is walked with a visited set
// because the engine tolerates a cycle — it stalls rather than spinning — and a
// status surface that inherited the same tolerance by looping forever would not
// be tolerating anything.
func syncBlockedIndex(pending []vaultimport.Pending) map[string][]syncBlockedView {
	dependents := map[string][]vaultimport.Pending{}
	for _, entry := range pending {
		if entry.Blocker == "" || entry.PackageID == "" {
			continue
		}
		dependents[entry.Blocker] = append(dependents[entry.Blocker], entry)
	}
	index := map[string][]syncBlockedView{}
	for _, entry := range pending {
		if entry.Reason != vaultimport.HoldConflict || entry.PackageID == "" {
			continue
		}
		index[entry.PackageID] = syncBlockedChain(dependents, entry.PackageID)
	}
	return index
}

func syncBlockedChain(dependents map[string][]vaultimport.Pending, packageID string) []syncBlockedView {
	var out []syncBlockedView
	seen := map[string]bool{packageID: true}
	queue := []string{packageID}
	for len(queue) > 0 {
		blocker := queue[0]
		queue = queue[1:]
		for _, waiting := range dependents[blocker] {
			if seen[waiting.PackageID] {
				continue
			}
			seen[waiting.PackageID] = true
			out = append(out, syncBlockedView{
				PackageID:    waiting.PackageID,
				OriginDevice: waiting.OriginDevice,
				Sequence:     waiting.Sequence,
				Reason:       string(waiting.Reason),
				Detail:       waiting.Detail,
				WaitingOn:    blocker,
			})
			queue = append(queue, waiting.PackageID)
		}
	}
	return out
}
