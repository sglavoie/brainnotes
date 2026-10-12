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
	review, err := vaultsync.LoadRestoreReview(root)
	if err != nil {
		return nil, err
	}
	if review != nil {
		status.RestoreReview = review
		status.States = []State{StateRestoreReview}
		return status, nil
	}

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
