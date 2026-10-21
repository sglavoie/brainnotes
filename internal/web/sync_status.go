package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vaultsyncd"
)

const syncStatusAPIRoutePath = "/api/sync/status"

// syncStatusView is where synchronization has got to, as a browser is told it.
//
// It is a projection of vaultsyncd.Status rather than that value re-encoded, and
// two of the differences are the reason it exists. Status.Root is this vault's
// absolute location, which no page serves and this one must not be the first to;
// and the counts are grouped under save, export, transport and peer so the three
// questions a person actually has — is my typing safe, has it left this machine,
// and what has arrived — each have one place to look.
//
// Summary is built here rather than in the browser because the mapping from ten
// states to one sentence is a statement about synchronization, not about layout,
// and a Go test can hold it to that.
type syncStatusView struct {
	OK         bool   `json:"ok"`
	Mode       string `json:"mode"`
	Enrolled   bool   `json:"enrolled"`
	ObservedAt string `json:"observed_at,omitempty"`
	// Headline is the single most urgent state, and States is every one that
	// holds. Both are the engine's own tokens, so a client styles on them without
	// parsing English.
	Headline string   `json:"headline,omitempty"`
	States   []string `json:"states"`
	// Summary is the one sentence a status line shows.
	Summary string `json:"summary"`
	// NeedsPerson is whether this vault will not make progress on its own.
	NeedsPerson bool              `json:"needs_person"`
	Save        syncSaveView      `json:"save"`
	Export      syncExportView    `json:"export"`
	Transport   syncTransportView `json:"transport"`
	Peer        syncPeerView      `json:"peer"`
	Workers     []syncWorkerView  `json:"workers,omitempty"`
	Problems    []syncProblemView `json:"problems,omitempty"`
	// Replica is the freshness of a published copy, and is set only in the mode
	// that has one. See replicaSyncStatus.
	Replica *syncReplicaView `json:"replica,omitempty"`
	// ReviewPath is where the conflicts can be decided, set only when there are
	// any. A link that is always there would offer a page with nothing on it.
	ReviewPath string `json:"review_path,omitempty"`
}

// syncSaveView is what this machine has written down and what stands in the way
// of writing more.
type syncSaveView struct {
	Managed       bool     `json:"managed"`
	SavedAt       string   `json:"saved_at,omitempty"`
	LastCommitAt  string   `json:"last_commit_at,omitempty"`
	Blocked       bool     `json:"blocked"`
	BlockReason   string   `json:"block_reason,omitempty"`
	BlockRecovery []string `json:"block_recovery,omitempty"`
}

// syncExportView is how much of this vault's history has not been packaged yet.
type syncExportView struct {
	PendingCommits  int  `json:"pending_commits"`
	BaselinePending bool `json:"baseline_pending"`
	Reserved        bool `json:"reserved"`
}

// syncTransportView is what this device published and what the peer has
// acknowledged.
//
// Acknowledged and AwaitingReceipt are carried separately and Published is not
// the sum of anything a person should read as progress: a package that has been
// copied is not a package that has been applied, and the only evidence of
// application this device can have is a receipt. The wording the browser renders
// keeps that distinction, which is what "status follows durable acknowledgments"
// means here.
type syncTransportView struct {
	Published       int                      `json:"published"`
	Acknowledged    int                      `json:"acknowledged"`
	AwaitingReceipt int                      `json:"awaiting_receipt"`
	Incomplete      int                      `json:"incomplete"`
	OldestAwaiting  string                   `json:"oldest_awaiting,omitempty"`
	Rejected        []syncReceiptProblemView `json:"rejected,omitempty"`
}

// syncPeerView is what has arrived and what this vault has done with it.
type syncPeerView struct {
	Delivered           int    `json:"delivered"`
	AwaitingApplication int    `json:"awaiting_application"`
	Incomplete          int    `json:"incomplete"`
	Applied             int    `json:"applied"`
	Pending             bool   `json:"pending"`
	LastDeliveryAt      string `json:"last_delivery_at,omitempty"`
	Conflicts           int    `json:"conflicts"`
}

type syncReceiptProblemView struct {
	PackageID string `json:"package_id"`
	Detail    string `json:"detail"`
}

type syncProblemView struct {
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// syncWorkerView is one scheduled unit's record, which is how a failing vault
// says so rather than looking idle.
type syncWorkerView struct {
	Name             string `json:"name"`
	Failures         int    `json:"failures,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	LastRunAt        string `json:"last_run_at,omitempty"`
	LastSuccessAt    string `json:"last_success_at,omitempty"`
	NextAttemptAfter string `json:"next_attempt_after,omitempty"`
	Ready            bool   `json:"ready"`
}

// syncReplicaView is the freshness of a read-only replica, which is the only
// thing a published copy honestly knows about synchronization.
//
// Recorded and Stale are separate fields on purpose. A replica whose status file
// has never recorded a successful copy and one whose last copy is an hour old are
// different situations with different remedies, and collapsing them into "stale"
// would report a replica that has never worked as one that briefly fell behind.
type syncReplicaView struct {
	Recorded           bool   `json:"recorded"`
	Stale              bool   `json:"stale"`
	LastSuccessfulSync string `json:"last_successful_sync,omitempty"`
}

// syncStateSummaries is the sentence each state says when it is the headline and
// has no count to carry. The four that do have one are in syncSummary.
var syncStateSummaries = map[vaultsyncd.State]string{
	vaultsyncd.StateBlocked:      "The content history is blocked, so nothing is being recorded.",
	vaultsyncd.StateFailing:      "Synchronization is failing and is waiting to try again.",
	vaultsyncd.StateDisconnected: "Nothing has been carried between the replicas for a while.",
	vaultsyncd.StateLocalSaved:   "Edits are saved here and are not in the history yet.",
	vaultsyncd.StateUnenrolled:   "This vault does not synchronize with another replica.",
	vaultsyncd.StateIdle:         "Everything here is saved, exported and acknowledged.",
}

// handleSyncStatus reports where synchronization has got to. It is read-only and
// takes no CSRF token, per the read-only route class: exact path, GET only, no
// lock, and no walk of the vault.
//
// A mode that cannot write documents answers with its replica's freshness and
// never reads the vault's synchronization state. That is not caution about locks
// — vaultsyncd.Read takes none — but about what the answer would mean: a replica
// is a copy of somebody else's vault, so its .bnotes/sync records describe the
// machine that made the copy rather than anything a reader of this page can act
// on, and a read there could create state directories in a tree this process does
// not own.
func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if !requireSyncGet(w, r, syncStatusAPIRoutePath) {
		return
	}
	if !s.caps.documentWrites {
		writeManagementJSON(w, http.StatusOK, s.replicaSyncStatus())
		return
	}
	status, err := vaultsyncd.Read(r.Context(), s.vault.Root, vaultsyncd.ReadOptions{})
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal,
			"sync: synchronization status is unavailable", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, s.syncStatusViewOf(status))
}

// requireSyncGet is the preamble the two read-only sync routes share: the exact
// path and GET or HEAD, answered in the management envelope.
func requireSyncGet(w http.ResponseWriter, r *http.Request, route string) bool {
	if r.URL.Path != route {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Both of them, because both are accepted. An Allow header that named fewer
		// methods than the route takes is the one part of a 405 a client is entitled
		// to act on.
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed,
			noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return false
	}
	return true
}

func (s *Server) syncStatusViewOf(status *vaultsyncd.Status) syncStatusView {
	view := syncStatusView{
		OK:          true,
		Mode:        string(s.caps.mode),
		Enrolled:    status.Enrolled,
		ObservedAt:  syncTimestamp(status.ObservedAt),
		Headline:    string(status.Headline()),
		States:      syncStateTokens(status.States),
		Summary:     syncSummary(status),
		NeedsPerson: status.NeedsPerson(),
		Save:        syncSaveViewOf(status.Local),
		Export:      syncExportViewOf(status.Export),
		Transport:   syncTransportViewOf(status.Transport),
		Peer:        syncPeerViewOf(status.Peer),
		Workers:     syncWorkerViewsOf(status.Workers, s.vault.Root),
		Problems:    syncProblemViewsOf(status.Problems),
	}
	if len(status.Peer.Conflicts) > 0 {
		view.ReviewPath = syncRoutePath
	}
	return view
}

// replicaSyncStatus is the whole answer in a mode that publishes a copy.
//
// It replaces the freshness sentence the navigation used to render from a
// template function, and keeps both of that sentence's cases: a replica with no
// recorded copy says so, and a stale one says that separately. Stale is reported
// as needing a person because the remedy is on the machine doing the copying,
// which nobody looking at this page will check unless it is said.
func (s *Server) replicaSyncStatus() syncStatusView {
	replica := readReplicaStatus(s.replicaStatusFile)
	recorded := !replica.LastSuccessfulSync.IsZero()
	return syncStatusView{
		OK:          true,
		Mode:        string(s.caps.mode),
		States:      []string{},
		Summary:     replicaSummary(replica, recorded),
		NeedsPerson: replica.Stale,
		Replica: &syncReplicaView{
			Recorded:           recorded,
			Stale:              replica.Stale,
			LastSuccessfulSync: syncTimestamp(replica.LastSuccessfulSync),
		},
	}
}

func replicaSummary(replica replicaStatus, recorded bool) string {
	if !recorded {
		return "Read-only replica · no verified copy has been recorded."
	}
	when := replica.LastSuccessfulSync.Format("2006-01-02 15:04:05 MST")
	if replica.Stale {
		return "Read-only replica · last verified copy " + when + ", over 15 minutes ago."
	}
	return "Read-only replica · last verified copy " + when + "."
}

// syncSummary is the one sentence the status line shows.
//
// The four states with a count answer with it, because "packages are waiting" and
// "nine packages are waiting" are different facts to a person deciding whether to
// go and look at the other machine. Everything else is a fixed sentence.
func syncSummary(status *vaultsyncd.Status) string {
	switch state := status.Headline(); state {
	case vaultsyncd.StateConflict:
		return syncCount(len(status.Peer.Conflicts),
			"change is waiting for a decision.", "changes are waiting for a decision.")
	case vaultsyncd.StatePeerApplication:
		return syncCount(status.Peer.AwaitingApplication,
			"change has arrived and is not applied yet.", "changes have arrived and are not applied yet.")
	case vaultsyncd.StateExportPending:
		return syncCount(status.Export.PendingCommits,
			"commit here has not been packaged yet.", "commits here have not been packaged yet.")
	case vaultsyncd.StateTransportPending:
		return syncCount(status.Transport.AwaitingReceipt,
			"change is published and not acknowledged yet.", "changes are published and not acknowledged yet.")
	default:
		if sentence, named := syncStateSummaries[state]; named {
			return sentence
		}
		return "Synchronization is " + string(state) + "."
	}
}

func syncCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func syncStateTokens(states []vaultsyncd.State) []string {
	tokens := make([]string, 0, len(states))
	for _, state := range states {
		tokens = append(tokens, string(state))
	}
	return tokens
}

func syncSaveViewOf(local vaultsyncd.LocalStatus) syncSaveView {
	return syncSaveView{
		Managed:       local.Managed,
		SavedAt:       syncTimestamp(local.SavedAt),
		LastCommitAt:  syncTimestamp(local.LastCommitAt),
		Blocked:       local.Blocked,
		BlockReason:   local.BlockReason,
		BlockRecovery: local.BlockRecovery,
	}
}

func syncExportViewOf(export vaultsyncd.ExportStatus) syncExportView {
	return syncExportView{
		PendingCommits:  export.PendingCommits,
		BaselinePending: export.BaselinePending,
		Reserved:        export.Reserved,
	}
}

func syncTransportViewOf(transport vaultsyncd.TransportStatus) syncTransportView {
	view := syncTransportView{
		Published:       transport.Published,
		Acknowledged:    transport.Acknowledged,
		AwaitingReceipt: transport.AwaitingReceipt,
		Incomplete:      transport.Incomplete,
		OldestAwaiting:  syncTimestamp(transport.OldestAwaiting),
	}
	for _, rejected := range transport.Rejected {
		view.Rejected = append(view.Rejected, syncReceiptProblemView{
			PackageID: rejected.PackageID,
			Detail:    rejected.Detail,
		})
	}
	return view
}

func syncPeerViewOf(peer vaultsyncd.PeerStatus) syncPeerView {
	return syncPeerView{
		Delivered:           peer.Delivered,
		AwaitingApplication: peer.AwaitingApplication,
		Incomplete:          peer.Incomplete,
		Applied:             peer.Applied,
		Pending:             peer.Pending,
		LastDeliveryAt:      syncTimestamp(peer.LastDeliveryAt),
		Conflicts:           len(peer.Conflicts),
	}
}

func syncProblemViewsOf(problems []vaultsyncd.Problem) []syncProblemView {
	views := make([]syncProblemView, 0, len(problems))
	for _, problem := range problems {
		views = append(views, syncProblemView{Subject: problem.Subject, Detail: problem.Detail})
	}
	if len(views) == 0 {
		return nil
	}
	return views
}

// syncWorkerViewsOf carries the companion's own failure messages, with this
// vault's absolute location taken out of them.
//
// The message is the one `bn sync status` prints, and it is the only place a
// person can read why an export has been failing for an hour, so dropping it
// would make a failing vault unexplainable from the browser. What it can carry
// that nothing else on this server does is the vault's absolute path, since the
// wrapped errors underneath are filesystem failures: relative note paths are
// served by every page here, the root is served by none.
func syncWorkerViewsOf(workers []vaultsyncd.WorkerStatus, root string) []syncWorkerView {
	views := make([]syncWorkerView, 0, len(workers))
	for _, worker := range workers {
		views = append(views, syncWorkerView{
			Name:             string(worker.Name),
			Failures:         worker.Failures,
			LastError:        withoutVaultRoot(worker.LastError, root),
			LastRunAt:        syncTimestamp(worker.LastRunAt),
			LastSuccessAt:    syncTimestamp(worker.LastSuccessAt),
			NextAttemptAfter: syncTimestamp(worker.NextAttemptAfter),
			Ready:            worker.Ready,
		})
	}
	if len(views) == 0 {
		return nil
	}
	return views
}

// withoutVaultRoot replaces the vault's absolute location wherever it appears in
// a message, leaving the vault-relative remainder a person can act on.
func withoutVaultRoot(message, root string) string {
	if message == "" || root == "" {
		return message
	}
	return strings.ReplaceAll(message, root, "<vault>")
}

// syncTimestamp is a time as a client reads it, and the empty string for a zero
// time: absent and "the epoch" are not the same claim, and RFC 3339 cannot say
// the first one.
func syncTimestamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
