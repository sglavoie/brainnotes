package vaultsyncd

import (
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// State names one thing that is true of a vault's synchronization right now.
//
// They are deliberately not a progression. Several hold at once in the ordinary
// case — a vault with an open edit burst, a package in flight, and a conflict
// waiting for a person is in three of them — so [Status.States] is a set and
// [Status.Headline] is the one a single line should report.
type State string

const (
	// StateUnenrolled is a vault that synchronizes with nothing. It is not a
	// fault and not a degraded mode: it is what almost every vault is.
	StateUnenrolled State = "unenrolled"
	// StateIdle is an enrolled vault with nothing outstanding in either
	// direction.
	StateIdle State = "idle"
	// StateLocalSaved is edits written to disk that the content history does not
	// carry yet — the autosave burst internal/vaultgit is debouncing. Nothing can
	// be exported until they are committed, so a vault that stays here while its
	// owner keeps typing is working correctly, and one that stays here with a
	// blocked history is not.
	StateLocalSaved State = "local-saved"
	// StateExportPending is commits in the content history that this device has
	// not turned into packages yet.
	StateExportPending State = "export-pending"
	// StateTransportPending is packages published into this device's outgoing
	// channel with no verified receipt from the peer.
	//
	// It covers everything from "the transport has not copied it yet" through
	// "the peer is applying it right now", and that breadth is the point: from
	// here those are the same fact, because the only evidence this device can
	// have of application is a receipt. Calling a copied file "delivered and
	// therefore done" is the mistake this state exists to make impossible.
	StateTransportPending State = "transport-pending"
	// StatePeerApplication is packages from the peer sitting in this vault's
	// incoming channel that this device has not applied. They are on this disk
	// and they are not in this vault.
	StatePeerApplication State = "peer-application"
	// StateConflict is at least one package held because this vault disagrees
	// with what it expected to find. It needs a person.
	StateConflict State = "conflict"
	// StateBlocked is the content history refusing mutations: internal/vaultgit
	// saw something it would not classify and wrote a block marker. It needs a
	// person, and it is the failure most likely to look like idleness — a blocked
	// vault takes no snapshots, so it produces nothing to export and no error.
	StateBlocked State = "blocked"
	// StateDisconnected is the inference that nothing is carrying files between
	// the replicas: packages have been waiting for an acknowledgment longer than
	// the threshold and nothing has arrived from the peer in that time.
	//
	// It is an inference and never an observation. This companion cannot see the
	// transport — by design, since the transport is a separate program that must
	// never touch a live vault — so what it reports is staleness. A peer that is
	// merely switched off looks exactly like a transport that has stopped, and
	// both are the same thing to act on: go and look at the other machine.
	StateDisconnected State = "disconnected"
	// StateFailing is a worker whose last attempt failed and which is waiting out
	// a backoff. The evidence is in [WorkerStatus.LastError], and it is a state
	// rather than a detail because an export that has been failing for an hour
	// must never read as an idle vault.
	//
	// The **first** failure raises it, not the third. A threshold would mean a
	// window in which the vault is failing and says it is fine, and the cost of
	// erring loud is bounded in the other direction: the streak is cleared the
	// moment the worker succeeds, and also the moment it is due and finds nothing
	// to do — so a transient failure shows for one backoff and no longer.
	StateFailing State = "failing"
	// StateRestoreReview is a restored causal state that has not yet been
	// compared with its peer and explicitly accepted into a new enrollment.
	// No worker may run while it holds.
	StateRestoreReview State = "restore-review"
)

// DefaultDisconnectedAfter is how long an unacknowledged package and a silent
// incoming channel must coexist before this reports [StateDisconnected].
//
// It is generous on purpose. A replica that syncs over the open internet can be
// legitimately quiet for a long time — a laptop that is shut, a Pi on a
// rebooting router — and an alarm that fires every lunch break is one nobody
// reads. What it must catch is the transport that stopped days ago and nobody
// noticed.
const DefaultDisconnectedAfter = 30 * time.Minute

// Problem is something the status read could not determine, named by what it was
// trying to read rather than by a stack of wrapped errors.
//
// It is a field rather than an error return because a status that cannot be
// printed is worse than a partial one: the operator asking is usually asking
// *because* something is wrong, and "the export record is unreadable" is the
// most useful sentence the command could say.
type Problem struct {
	// Subject is what could not be read, in the words an operator would use.
	Subject string `json:"subject"`
	// Detail is the sanitized failure. It carries no note content, because a
	// status line reaches logs and terminals a vault's contents should not.
	Detail string `json:"detail"`
}

// LocalStatus is what this device has written down and what stands in the way of
// writing more.
type LocalStatus struct {
	// Managed reports whether the vault has a content history at all. A vault
	// without one cannot export, which is a configuration fact rather than a
	// failure.
	Managed bool `json:"managed"`
	// SavedAt is when the open edit burst started, zero when none is open. A
	// non-zero value means content exists on this disk that the history does not
	// carry.
	SavedAt time.Time `json:"saved_at"`
	// LastCommitAt is the last external autosave the history recorded.
	LastCommitAt time.Time `json:"last_commit_at"`
	// Blocked, BlockReason and BlockRecovery are the content history's refusal.
	// The recovery steps are carried because they are what a person does next.
	Blocked       bool     `json:"blocked"`
	BlockReason   string   `json:"block_reason,omitempty"`
	BlockRecovery []string `json:"block_recovery,omitempty"`
}

// ExportStatus is this device's producer position.
type ExportStatus struct {
	// Branch and Cursor are the line of history being followed and the last
	// commit fully accounted for.
	Branch string `json:"branch,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	// Tip is the branch's current commit.
	Tip string `json:"tip,omitempty"`
	// PendingCommits is how many commits lie between the cursor and the tip. It
	// is the honest upper bound rather than a count of packages: some of those
	// commits will be skipped as imported or private.
	PendingCommits int `json:"pending_commits"`
	// BaselinePending reports a first export that has not finished describing the
	// vault.
	BaselinePending bool `json:"baseline_pending"`
	// Reserved reports a sequence number claimed for a publication whose outcome
	// is not yet known — an interrupted pass, which the next export resolves.
	Reserved bool `json:"reserved"`
}

// ReceiptProblem is a receipt that arrived for one of this device's packages and
// did not verify against it.
//
// It is reported rather than ignored because it is unfalsifiable evidence of
// something wrong between the replicas — a stale epoch, a device that is not
// enrolled, an acknowledgment covering only part of a package — and because the
// safe response makes it invisible otherwise: the package is simply never
// retired, and a channel that silently stops draining looks like a slow one.
type ReceiptProblem struct {
	PackageID string `json:"package_id"`
	Detail    string `json:"detail"`
}

// TransportStatus is what this device has published and what the peer has
// acknowledged. Every field here is about *this device's* stream.
type TransportStatus struct {
	// Published is how many package directories are in the outgoing channel.
	Published int `json:"published"`
	// Acknowledged is how many of them carry a verified receipt. That is the
	// evidence retirement waits for, not the decision: the package an interrupted
	// publication reserved is kept whatever its receipt says, for the reason
	// reservedPackage gives.
	Acknowledged int `json:"acknowledged"`
	// AwaitingReceipt is how many have no verified receipt. This is the number
	// that must never be read as "delivered".
	AwaitingReceipt int `json:"awaiting_receipt"`
	// Incomplete is how many could not be read as packages, which for a directory
	// this device owns means a publication interrupted before its manifest
	// landed. They are inert and are reported so the gap they leave in this
	// device's sequence has an explanation.
	Incomplete int `json:"incomplete"`
	// OldestAwaiting is when the oldest unacknowledged package was created, by
	// this device's own clock. It is what the disconnection inference is made
	// from.
	OldestAwaiting time.Time `json:"oldest_awaiting"`
	// Rejected are receipts that did not verify. See ReceiptProblem.
	Rejected []ReceiptProblem `json:"rejected,omitempty"`
}

// ConflictSummary is one held package, flattened to what a status line shows.
type ConflictSummary struct {
	PackageID    string    `json:"package_id"`
	OriginDevice string    `json:"origin_device"`
	Sequence     uint64    `json:"sequence"`
	DetectedAt   time.Time `json:"detected_at"`
	// Paths are the contested vault-relative paths. They are note paths and they
	// do reach a log line — unavoidably, since the whole point is to tell a person
	// which notes to look at.
	Paths []string `json:"paths"`
}

// PeerStatus is what has arrived from the peer and what this vault has done with
// it. The first three fields are the delivery-versus-application distinction
// stated in the inbound direction.
type PeerStatus struct {
	// Delivered is how many package directories are in the incoming channel.
	Delivered int `json:"delivered"`
	// AwaitingApplication is how many of those this vault has not applied. Their
	// changes are on this disk and are not in this vault.
	AwaitingApplication int `json:"awaiting_application"`
	// Incomplete is the subset of those that cannot be read yet: a package the
	// transport is still copying in, or one that arrived damaged.
	Incomplete int `json:"incomplete"`
	// Applied is how many packages the import ledger still remembers because the
	// peer may redeliver them. Sender retirement evidence prunes older records.
	Applied int `json:"applied"`
	// Pending reports an application that was interrupted and whose outcome is
	// not yet resolved. The next drain resolves it before anything else.
	Pending bool `json:"pending"`
	// LastDeliveryAt is the most recent modification time in the incoming
	// channel, which is when the transport last put something there. It is a
	// local filesystem observation, not the peer's clock.
	LastDeliveryAt time.Time `json:"last_delivery_at"`
	// Conflicts are the packages held for a person.
	Conflicts []ConflictSummary `json:"conflicts,omitempty"`
}

// Status is one read of where synchronization has got to. It is a value: nothing
// in it is live, and everything in it was true at ObservedAt.
type Status struct {
	Root       string    `json:"root"`
	ObservedAt time.Time `json:"observed_at"`
	Enrolled   bool      `json:"enrolled"`
	VaultID    string    `json:"vault_id,omitempty"`
	DeviceID   string    `json:"device_id,omitempty"`
	// RestoreReview is present while synchronization is quarantined after a
	// restore. It carries identities and generations only, never note content.
	RestoreReview *vaultsync.RestoreReview `json:"restore_review,omitempty"`
	// States is every state that holds, in the fixed order below.
	States    []State         `json:"states"`
	Local     LocalStatus     `json:"local"`
	Export    ExportStatus    `json:"export"`
	Transport TransportStatus `json:"transport"`
	Peer      PeerStatus      `json:"peer"`
	// Workers is the durable scheduling record: what each unit last did, how many
	// times in a row it has failed, and when it may run again.
	Workers []WorkerStatus `json:"workers,omitempty"`
	// Problems is everything this read could not determine. See Problem.
	Problems []Problem `json:"problems,omitempty"`
	// CompanionBuild is the build identifier the companion last wrote into its
	// record (internal/buildinfo), empty when none was recorded.
	CompanionBuild string `json:"companion_build,omitempty"`
	// BuildMismatch reports that CompanionBuild and the reader's own build are
	// both known and differ: the companion that last ran is not the binary doing
	// this read, usually because an install replaced the file and the running
	// LaunchAgent was not restarted. It is a warning, not a state, and it does
	// not change NeedsPerson.
	BuildMismatch bool `json:"build_mismatch,omitempty"`
}

// stateOrder is the order states are reported in, most urgent first. Headline
// takes the first of them, so this list is also the answer to "if I can print
// one word, which one".
//
// Blocked leads because it stops the vault recording anything at all. Conflict
// follows because it is the other state only a person can clear. Then the
// failures, then the ordinary work in the direction it flows.
var stateOrder = []State{
	StateRestoreReview,
	StateBlocked,
	StateConflict,
	StateFailing,
	StateDisconnected,
	StatePeerApplication,
	StateLocalSaved,
	StateExportPending,
	StateTransportPending,
	StateUnenrolled,
	StateIdle,
}

// Headline is the single state that best describes this status.
func (s *Status) Headline() State {
	if len(s.States) == 0 {
		return StateIdle
	}
	return s.States[0]
}

// Is reports whether state holds.
func (s *Status) Is(state State) bool {
	for _, held := range s.States {
		if held == state {
			return true
		}
	}
	return false
}

// NeedsPerson reports whether this vault will not make progress on its own.
//
// It is the question `bn-vault-sync -check` answers, so its membership is
// deliberately narrow: work in flight is not a problem, however much of it there
// is, and a conflict, a block, a rejected receipt, a failing worker or an
// unreadable record all are. A disconnection is included because the two
// replicas have already been out of touch long enough that waiting is no longer
// the remedy.
func (s *Status) NeedsPerson() bool {
	switch {
	case s.Is(StateRestoreReview), s.Is(StateBlocked), s.Is(StateConflict), s.Is(StateFailing), s.Is(StateDisconnected):
		return true
	case len(s.Problems) > 0, len(s.Transport.Rejected) > 0:
		return true
	}
	return false
}
