package vaultsyncd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
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
	// Applied is how many packages the import ledger remembers, across the whole
	// history of this enrollment rather than only what is in the channel.
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
}

// stateOrder is the order states are reported in, most urgent first. Headline
// takes the first of them, so this list is also the answer to "if I can print
// one word, which one".
//
// Blocked leads because it stops the vault recording anything at all. Conflict
// follows because it is the other state only a person can clear. Then the
// failures, then the ordinary work in the direction it flows.
var stateOrder = []State{
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
	case s.Is(StateBlocked), s.Is(StateConflict), s.Is(StateFailing), s.Is(StateDisconnected):
		return true
	case len(s.Problems) > 0, len(s.Transport.Rejected) > 0:
		return true
	}
	return false
}

// ReadOptions configure one status read.
type ReadOptions struct {
	// Now is the clock seam.
	Now func() time.Time
	// GitExecutable overrides the git found on PATH.
	GitExecutable string
	// DisconnectedAfter overrides DefaultDisconnectedAfter. A negative value
	// turns the inference off, which is what a test that is not about it wants.
	DisconnectedAfter time.Duration
}

func (o ReadOptions) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

func (o ReadOptions) disconnectedAfter() time.Duration {
	if o.DisconnectedAfter == 0 {
		return DefaultDisconnectedAfter
	}
	return o.DisconnectedAfter
}

// Read reports where synchronization has got to, without taking a lock and
// without changing anything.
//
// It returns an error only when the vault root itself is unusable or the
// enrollment record cannot be read, because those are the two facts every other
// answer here is relative to. Everything else that fails is a [Problem] on the
// returned status: a status command must be able to print *something*, and the
// occasions it cannot read a record are exactly the occasions somebody needs to
// be told which one.
func Read(ctx context.Context, root string, opts ReadOptions) (*Status, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	status := &Status{Root: root, ObservedAt: opts.now()}
	local, err := vaultsync.Load(root)
	switch {
	case errors.Is(err, vaultsync.ErrNotEnrolled):
		status.States = []State{StateUnenrolled}
		return status, nil
	case err != nil:
		return nil, err
	}
	status.Enrolled, status.VaultID, status.DeviceID = true, local.VaultID, local.DeviceID

	status.readHistory(ctx, local, opts)
	status.readExport(root)
	status.readTransport(root, local)
	status.readPeer(root)
	status.readWorkers(root, opts.now())
	status.States = deriveStates(status, opts)
	return status, nil
}

// readHistory fills in the local half from internal/vaultgit's read-only door.
//
// An unmanaged vault is recorded as unmanaged rather than as a problem: a vault
// with no content history exports nothing, which is a thing to know and not a
// thing to fix. Every other failure is a problem, including the block marker's
// own read error — a marker that cannot be read means blocked, so the vault is
// reported blocked *and* the read failure is named.
func (s *Status) readHistory(ctx context.Context, local *vaultsync.Enrollment, opts ReadOptions) {
	history, err := vaultgit.OpenHistory(ctx, s.Root, vaultgit.HistoryOptions{GitExecutable: opts.GitExecutable})
	if err != nil {
		if !vaultgit.IsUnmanaged(err) {
			s.problem("content history", err)
		}
		return
	}
	s.Local.Managed = true
	watch := history.Watch()
	s.Local.SavedAt, s.Local.LastCommitAt = watch.FirstChangeAt, watch.LastCommitAt

	record, blocked, err := history.Blocked()
	if err != nil {
		s.problem("block marker", err)
	}
	if blocked {
		s.Local.Blocked = true
		s.Local.BlockReason = record.Reason
		s.Local.BlockRecovery = record.Recovery
	}
	s.readCursor(ctx, history, local)
}

// readCursor measures the distance between the export cursor and the branch tip.
//
// The count is over commits rather than over what they would publish, and it is
// deliberately not refined: working out which of them are imported or private is
// the exporter's job and costs a tree read each. An upper bound that is never
// wrong about *zero* is what a status needs, and zero is exact here.
func (s *Status) readCursor(ctx context.Context, history *vaultgit.History, local *vaultsync.Enrollment) {
	record, err := vaultexport.LoadState(s.Root)
	if err != nil {
		s.problem("export record", err)
		return
	}
	s.Export.Branch, s.Export.Cursor = record.Branch, record.Commit
	s.Export.BaselinePending = record.Baseline != ""
	s.Export.Reserved = record.Pending != nil
	s.checkHalt(record, history.Branch(), local)

	tip, err := history.Tip(ctx)
	if err != nil {
		if !errors.Is(err, vaultgit.ErrUnbornBranch) {
			s.problem("branch tip", err)
		}
		return
	}
	s.Export.Tip = tip
	// A baseline that has not finished has no cursor to measure from, and the
	// commits behind it are not pending in the sense this field means: they were
	// never going to be replayed. BaselinePending is the answer for that vault.
	if s.Export.BaselinePending {
		return
	}
	commits, err := history.CommitsAfter(ctx, record.Commit, tip)
	if err != nil {
		s.problem("unexported commits", err)
		return
	}
	s.Export.PendingCommits = len(commits)
	// The third halt, inferred from the count rather than asked for. `cursor..tip`
	// is empty exactly when every commit the tip reaches the cursor reaches too,
	// so an empty range whose ends differ means the tip is *behind* the cursor —
	// which is `vaultexport`'s ErrHistoryRewritten, wearing the same nothing-left
	// -to-publish reading checkHalt exists to refuse. A branch rewritten the other
	// way is already loud: the range is non-empty, so the export runs and refuses.
	if len(commits) == 0 && record.Commit != "" && record.Commit != tip {
		s.problem("export cursor", fmt.Errorf(
			"the branch no longer contains the export cursor: %s is behind %s", tip, record.Commit))
	}
}

// checkHalt names two of the three producer halts that no measurement on this
// status would show. The third is inferred from the commit count; see
// readCursor.
//
// internal/vaultexport refuses to publish under a cursor recorded in an earlier
// enrollment generation, or one that follows a branch this vault is no longer
// on, and both refusals survive every retry until a person runs Reset. Neither
// leaves anything behind for readCursor to count: the epoch is checked *after*
// an interrupted publication is resolved, and resolving one advances the cursor
// durably, so the halted vault measures as having nothing left to publish. A
// halted producer that reports nothing pending is a vault that looks permanently
// up to date — and, once the export worker stops being asked, one whose failure
// streak is cleared as an ordinary idle worker's would be.
//
// It is a Problem rather than a field because a Problem already acts: it needs a
// person, and exportSkip runs the export anyway on a status that could not
// determine something, so the refusal becomes a recorded worker failure carrying
// its own evidence rather than a silence.
//
// The conditions mirror vaultexport's own, including the case it forgives: a
// cursor with nothing behind it adopts the current epoch rather than halting,
// which is every vault between enrollment and its first export.
func (s *Status) checkHalt(record *vaultexport.State, branch string, local *vaultsync.Enrollment) {
	if record.Epoch != local.Epoch && (record.Commit != "" || record.Baseline != "") {
		s.problem("export cursor", fmt.Errorf(
			"the export cursor was recorded under enrollment epoch %d and this vault is on epoch %d",
			record.Epoch, local.Epoch))
	}
	if record.Branch != "" && record.Branch != branch {
		s.problem("export cursor", fmt.Errorf(
			"the export cursor follows %s and this vault is on %s", record.Branch, branch))
	}
}

// readExport is the half of the export record that can be read without a
// content history, so a vault whose repository is broken still reports its
// producer position.
func (s *Status) readExport(root string) {
	if s.Local.Managed {
		return
	}
	record, err := vaultexport.LoadState(root)
	if err != nil {
		s.problem("export record", err)
		return
	}
	s.Export.Branch, s.Export.Cursor = record.Branch, record.Commit
	s.Export.BaselinePending = record.Baseline != ""
	s.Export.Reserved = record.Pending != nil
}

// readTransport counts this device's published packages against the receipts the
// peer left in its channel.
func (s *Status) readTransport(root string, local *vaultsync.Enrollment) {
	published, err := listPackages(vaultsync.OutgoingDir(root))
	if err != nil {
		s.problem("outgoing channel", err)
		return
	}
	s.Transport.Published = len(published)
	for _, pkg := range published {
		s.countPublished(root, local, pkg)
	}
}

// countPublished decides which of the three things one published package is.
func (s *Status) countPublished(root string, local *vaultsync.Enrollment, pkg pkgEntry) {
	manifest, err := vaultsync.ReadManifest(pkg.dir)
	if err != nil {
		s.Transport.Incomplete++
		return
	}
	switch acknowledged, err := verifyReceipt(root, manifest, local); {
	case err != nil:
		s.Transport.Rejected = append(s.Transport.Rejected, ReceiptProblem{
			PackageID: manifest.PackageID,
			Detail:    agent.SanitizedDiagnostic(err),
		})
		s.awaiting(manifest.CreatedAt)
	case acknowledged:
		s.Transport.Acknowledged++
	default:
		s.awaiting(manifest.CreatedAt)
	}
}

// awaiting counts one package with no verified receipt and keeps the oldest
// creation time, which is what the disconnection inference is made from.
func (s *Status) awaiting(createdAt time.Time) {
	s.Transport.AwaitingReceipt++
	if createdAt.IsZero() {
		return
	}
	if s.Transport.OldestAwaiting.IsZero() || createdAt.Before(s.Transport.OldestAwaiting) {
		s.Transport.OldestAwaiting = createdAt.UTC()
	}
}

// readPeer counts what the transport has delivered against what this vault has
// applied.
func (s *Status) readPeer(root string) {
	record, err := vaultimport.LoadState(root)
	if err != nil {
		s.problem("import record", err)
		return
	}
	s.Peer.Applied = len(record.Applied)
	s.Peer.Pending = record.Pending != nil
	s.Peer.Conflicts = summarizeConflicts(record)

	delivered, err := listPackages(vaultsync.IncomingDir(root))
	if err != nil {
		s.problem("incoming channel", err)
		return
	}
	s.Peer.Delivered = len(delivered)
	s.Peer.LastDeliveryAt = newestDelivery(vaultsync.IncomingDir(root))
	for _, pkg := range delivered {
		// By directory name rather than by the manifest inside it. The name is the
		// identity the ledger records, and a package still being copied in has no
		// readable manifest — counting it as unapplied by reading one would count it
		// as incomplete instead, which is true and is a different question.
		if _, applied := record.Applied[pkg.name]; applied {
			continue
		}
		s.Peer.AwaitingApplication++
		if _, err := vaultsync.ReadManifest(pkg.dir); err != nil {
			s.Peer.Incomplete++
		}
	}
}

// summarizeConflicts flattens the held packages, sorted so two reads of one
// vault produce the same report.
func summarizeConflicts(record *vaultimport.State) []ConflictSummary {
	summaries := make([]ConflictSummary, 0, len(record.Conflicts))
	for packageID, conflict := range record.Conflicts {
		if conflict == nil {
			continue
		}
		summary := ConflictSummary{
			PackageID:    packageID,
			OriginDevice: conflict.OriginDevice,
			Sequence:     conflict.Sequence,
			DetectedAt:   conflict.DetectedAt,
		}
		for _, path := range conflict.Paths {
			summary.Paths = append(summary.Paths, path.Path)
		}
		sort.Strings(summary.Paths)
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].PackageID < summaries[j].PackageID })
	if len(summaries) == 0 {
		return nil
	}
	return summaries
}

// readWorkers reports the durable scheduling record.
//
// An unreadable record does not stop a companion — see [LoadRecord] for why it
// is discarded rather than refused — but it is still a [Problem] here, and that
// is the difference between the two callers. A companion that throws the record
// away loses one retry; a *reader* that threw the error away would be reporting
// an empty worker list, which is indistinguishable from a vault whose workers
// have never failed. A worker failing for two days behind a truncated record
// would then read as healthy and `-check` would exit zero, which is exactly the
// silence this package exists to refuse.
func (s *Status) readWorkers(root string, now time.Time) {
	record, err := LoadRecord(root)
	if err != nil {
		s.problem("companion record", err)
	}
	s.Workers = record.statuses(now)
}

// deriveStates turns the measurements into the set of states that hold, in
// stateOrder. It is a pure function of the status so that the states and the
// numbers beside them can never tell different stories.
func deriveStates(s *Status, opts ReadOptions) []State {
	held := map[State]bool{
		StateBlocked:          s.Local.Blocked,
		StateConflict:         len(s.Peer.Conflicts) > 0,
		StateFailing:          anyFailing(s.Workers),
		StateDisconnected:     disconnected(s, opts),
		StatePeerApplication:  s.Peer.AwaitingApplication > 0,
		StateLocalSaved:       !s.Local.SavedAt.IsZero(),
		StateExportPending:    s.Export.PendingCommits > 0 || s.Export.BaselinePending || s.Export.Reserved,
		StateTransportPending: s.Transport.AwaitingReceipt > 0,
	}
	states := make([]State, 0, len(stateOrder))
	for _, state := range stateOrder {
		if held[state] {
			states = append(states, state)
		}
	}
	if len(states) == 0 {
		return []State{StateIdle}
	}
	return states
}

func anyFailing(workers []WorkerStatus) bool {
	for _, worker := range workers {
		if worker.Failures > 0 {
			return true
		}
	}
	return false
}

// disconnected is the staleness inference StateDisconnected documents. Both
// halves are required: a package waiting a long time means nothing on its own if
// the peer has been sending things all along, since the peer may simply be
// slow to apply one package — and that is application, not transport.
func disconnected(s *Status, opts ReadOptions) bool {
	window := opts.disconnectedAfter()
	if window < 0 || s.Transport.AwaitingReceipt == 0 || s.Transport.OldestAwaiting.IsZero() {
		return false
	}
	deadline := s.ObservedAt.Add(-window)
	if !s.Transport.OldestAwaiting.Before(deadline) {
		return false
	}
	return s.Peer.LastDeliveryAt.IsZero() || s.Peer.LastDeliveryAt.Before(deadline)
}

// problem records a failure against what was being read, with the error text
// sanitized: a status line reaches logs and terminals that a vault's contents
// must not.
func (s *Status) problem(subject string, err error) {
	s.Problems = append(s.Problems, Problem{Subject: subject, Detail: agent.SanitizedDiagnostic(err)})
}

// resolveRoot is the one refusal every entry point in this package shares.
func resolveRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("vaultsyncd: vault root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("vaultsyncd: resolve vault root %q: %w", root, err)
	}
	return abs, nil
}
