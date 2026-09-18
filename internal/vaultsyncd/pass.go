package vaultsyncd

import (
	"context"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// companion is one running instance: the vault it holds, and the small amount of
// in-process state that keeps a log from repeating itself.
//
// Nothing here is needed for correctness. Every announcement it suppresses is a
// restatement of something the durable record or a status read already says, so
// a restarted companion that announces a condition a second time has lost
// nothing but a duplicate line.
type companion struct {
	root string
	opts Options
	// headline is the last state announced, so a vault that stays in one state
	// produces one line rather than one per pass.
	headline State
	// deferred is the last skip reason announced per worker, for the same reason.
	deferred map[WorkerName]string
	// rejected is the set of packages whose bad receipts have been announced. A
	// rejected receipt needs a person and will not clear on its own, so it must
	// be said once and not every fifteen seconds.
	rejected map[string]bool
}

func newCompanion(root string, opts Options) *companion {
	return &companion{
		root:     root,
		opts:     opts,
		deferred: map[WorkerName]string{},
		rejected: map[string]bool{},
	}
}

func (c *companion) emit(event Event) {
	if c.opts.Observe == nil {
		return
	}
	event.At = c.opts.now()
	event.Vault = c.root
	c.opts.Observe(event)
}

// vaultTryLock takes the single-instance lock.
//
// vault.TryLockPath rather than vault.LockPaths, for the reason
// internal/vaultimport's guard gives: LockPaths escalates to the exclusive vault
// guard, and the transaction this companion is about to drive escalates to the
// same guard from inside. Wait-free is also the behavior a second instance
// wants — report and exit, rather than queue behind a process that intends to
// run forever.
func vaultTryLock(root string) (func(), bool, error) {
	release, acquired, err := vault.TryLockPath(LockPath(root))
	if err != nil {
		return release, false, fmt.Errorf("vaultsyncd: take the single-instance lock: %w", err)
	}
	return release, acquired, nil
}

// pass runs the three workers once, in workerOrder, and reports what they did.
//
// The order is the lock-ordering invariant the package documentation states, and
// the serialization is what makes it hold: the units run one after another in
// this goroutine, so no two of them can be inside two subsystem locks at once.
func (c *companion) pass(ctx context.Context) *Pass {
	pass := &Pass{Root: c.root, StartedAt: c.opts.now()}
	before, err := Read(ctx, c.root, c.opts.readOptions())
	if err != nil {
		pass.FinishedAt = c.opts.now()
		c.emit(Event{
			Kind:    EventProblem,
			Subject: "vault",
			Message: "the vault could not be read, so this pass did nothing",
			Detail:  sanitize(err),
		})
		return pass
	}
	pass.Enrolled = before.Enrolled
	if !before.Enrolled {
		c.announce(EventUnenrolled, StateUnenrolled, "the vault is not enrolled in synchronization")
		pass.Status, pass.FinishedAt = before, c.opts.now()
		return pass
	}

	record, loadErr := LoadRecord(c.root)
	if loadErr != nil {
		c.emit(Event{
			Kind:    EventProblem,
			Subject: "companion record",
			Message: "the companion record was unusable and has been started again",
			Detail:  sanitize(loadErr),
		})
	}
	state := &passState{before: before, record: record}
	for _, name := range workerOrder {
		// Between units, never inside one. This is the whole of graceful shutdown:
		// see Run.
		if ctx.Err() != nil {
			break
		}
		unit := c.runUnit(ctx, name, state)
		state.observe(unit)
		pass.Units = append(pass.Units, unit)
	}
	pass.FinishedAt = c.opts.now()
	pass.Status = c.observe(ctx)
	return pass
}

// observe reads the vault again, after the work, and announces what changed.
//
// A cancelled context skips the read rather than making it fail: the git calls
// inside it would refuse, and a companion that is shutting down would report a
// vault that "could not be read" when the truth is that nobody asked.
func (c *companion) observe(ctx context.Context) *Status {
	if ctx.Err() != nil {
		return nil
	}
	status, err := Read(ctx, c.root, c.opts.readOptions())
	if err != nil {
		c.emit(Event{
			Kind:    EventProblem,
			Subject: "vault",
			Message: "the vault could not be read after this pass",
			Detail:  sanitize(err),
		})
		return nil
	}
	c.announce(EventStatus, status.Headline(), headlineMessage(status.Headline()))
	for _, problem := range status.Problems {
		c.emit(Event{
			Kind:    EventProblem,
			Subject: problem.Subject,
			Message: "a fact about this vault could not be determined",
			Detail:  problem.Detail,
		})
	}
	return status
}

// announce emits a state-shaped event only when the state differs from the one
// last announced.
func (c *companion) announce(kind EventKind, state State, message string) {
	if c.headline == state {
		return
	}
	c.headline = state
	c.emit(Event{Kind: kind, State: state, Message: message})
}

// passState is what one pass knows while it runs: the vault as it stood before
// any of the work, the scheduling record the units update, and what an earlier
// unit in this same pass has already done.
//
// The last part is why this is a struct rather than two arguments. The status is
// read once, before the first unit, so a decision made from it is stale for
// every unit after the first — and the one that matters is the export's, because
// an application commits and a pass that skipped its export on a
// nothing-to-publish reading would leave that commit sitting until the next tick.
type passState struct {
	before *Status
	record *Record
	// imported reports that a unit in this pass applied something, and therefore
	// that this vault has a commit the pre-pass status could not have seen.
	imported bool
}

// observe folds a finished unit's result back into what the rest of the pass
// knows.
func (s *passState) observe(unit UnitResult) {
	if unit.Import != nil && unit.Import.Changed > 0 {
		s.imported = true
	}
}

// backoffSkip is the one skip that is *not* evidence the worker is healthy: the
// last attempt failed and the wait has not elapsed, so nothing has been learned
// since.
const backoffSkip = "the backoff after the last failure has not elapsed"

// runUnit decides whether one worker runs, runs it, and records the outcome.
func (c *companion) runUnit(ctx context.Context, name WorkerName, state *passState) UnitResult {
	record := state.record
	unit := UnitResult{Worker: name}
	if !record.ready(name, c.opts.now()) {
		unit.Skipped = backoffSkip
		c.deferFor(name, unit.Skipped)
		return unit
	}
	if reason := c.skipReason(name, state); reason != "" {
		unit.Skipped = reason
		c.deferFor(name, reason)
		// Due, asked, and nothing to do. That is not a failing worker, whatever the
		// last attempt did — see Record.declined.
		if record.declined(name) {
			c.persist(record)
		}
		return unit
	}
	unit.Ran = true
	err := c.invoke(ctx, name, &unit)
	switch {
	case err == nil:
		c.deferred[name] = ""
		record.succeeded(name, c.opts.now())
	case skippable(err):
		// Declined rather than failed: nothing here lengthens a backoff, so a vault
		// that is merely busy or unmanaged never accumulates one. A refusal that
		// proves there was nothing to do clears the streak as well, exactly as the
		// skip decided above does; one that only proves the worker could not look
		// leaves it. See vacant.
		unit.Ran, unit.Skipped = false, sanitize(err)
		c.deferFor(name, unit.Skipped)
		if vacant(err) && record.declined(name) {
			c.persist(record)
		}
		return unit
	default:
		unit.Error = sanitize(err)
		record.failed(name, c.opts.now(), err)
		c.deferred[name] = ""
		c.announceFailure(name, record.worker(name), unit.Error)
	}
	// Reported after a failure too, because a worker that failed part-way has
	// usually already done something durable and the partial outcome is the
	// evidence of it.
	c.report(name, unit)
	c.persist(record)
	return unit
}

// persist saves the scheduling record, reporting rather than refusing when it
// cannot.
//
// A companion that stops working because it could not write down *when to try
// next* would be trading a whole vault's synchronization for a scheduling
// detail. The failure is announced, because a backoff that is not durable is a
// backoff a restart forgets, and that is worth knowing.
func (c *companion) persist(record *Record) {
	if err := record.save(c.root); err != nil {
		c.emit(Event{
			Kind:    EventProblem,
			Subject: "companion record",
			Message: "the companion record could not be written, so this schedule will not survive a restart",
			Detail:  sanitize(err),
		})
	}
}

// invoke runs one worker and attaches its outcome to the unit.
func (c *companion) invoke(ctx context.Context, name WorkerName, unit *UnitResult) error {
	switch name {
	case WorkerImport:
		outcome, err := runImport(c.root, c.opts)
		unit.Import = outcome
		return err
	case WorkerExport:
		outcome, err := runExport(ctx, c.root, c.opts)
		unit.Export = outcome
		return err
	case WorkerRetire:
		outcome, err := runRetire(c.root)
		unit.Retire = outcome
		return err
	}
	return fmt.Errorf("vaultsyncd: unknown worker %q", name)
}

// skipReason reports why a worker that is due is still not running this pass, or
// "" if it is. The backoff is decided by the caller, because a skip decided here
// means the worker was asked and had nothing to do — which is the reading
// Record.declined acts on.
//
// A skip is a scheduling decision, never a failure: nothing here lengthens a
// failure streak, so a vault that is idle, blocked, or unmanaged retries at the
// ordinary interval the moment its condition clears.
func (c *companion) skipReason(name WorkerName, state *passState) string {
	switch name {
	case WorkerImport:
		// A blocked content history refuses every mutation, so every application
		// this pass attempted would fail identically and lengthen a backoff for a
		// condition no retry can clear. The vault says so through StateBlocked
		// instead, which is the thing a person can act on.
		if state.before.Local.Blocked {
			return "the content history is blocked, so nothing can be applied"
		}
	case WorkerExport:
		return exportSkip(state)
	case WorkerRetire:
	}
	return ""
}

// exportSkip decides whether there is anything to publish.
//
// A status that could not determine something runs the export anyway. The
// alternative is worse than the wasted work: an unreadable export record reads
// here as "no pending commits", and skipping on it would turn a broken producer
// into a vault that looks permanently up to date. Running it makes the same
// failure a recorded worker failure with evidence attached.
//
// An application earlier in this pass runs it for the same reason in the other
// direction: the reading this decision is made from predates that commit, and a
// resolution revision — the commit that tells the peer which version won — is
// exactly the kind of change nobody wants held for a tick.
func exportSkip(state *passState) string {
	before := state.before
	if state.imported || len(before.Problems) > 0 {
		return ""
	}
	if !before.Local.Managed {
		return "the vault has no content history to publish from"
	}
	if before.Export.PendingCommits == 0 && !before.Export.BaselinePending && !before.Export.Reserved {
		return "the content history holds nothing this device has not published"
	}
	return ""
}

// deferFor announces a skip once per distinct reason, per worker.
func (c *companion) deferFor(name WorkerName, reason string) {
	if c.deferred[name] == reason {
		return
	}
	c.deferred[name] = reason
	c.emit(Event{Kind: EventDeferred, Worker: name, Message: reason})
}

func (c *companion) announceFailure(name WorkerName, worker WorkerRecord, detail string) {
	c.emit(Event{
		Kind:             EventFailed,
		Worker:           name,
		Message:          fmt.Sprintf("the %s worker failed", name),
		Detail:           detail,
		Failures:         worker.Failures,
		NextAttemptAfter: worker.NextAttemptAfter,
	})
}

// report turns one worker's outcome into events, and says nothing about a
// worker that did nothing.
func (c *companion) report(name WorkerName, unit UnitResult) {
	switch name {
	case WorkerImport:
		c.reportImport(unit.Import)
	case WorkerExport:
		c.reportExport(unit.Export)
	case WorkerRetire:
		c.reportRetire(unit.Retire)
	}
}

func (c *companion) reportImport(outcome *ImportOutcome) {
	if outcome == nil {
		return
	}
	if outcome.Answered > 0 {
		c.emit(Event{
			Kind:     EventImported,
			Worker:   WorkerImport,
			Message:  "packages from the peer were applied to this vault",
			Packages: outcome.Answered,
			Changed:  outcome.Changed,
		})
	}
	if outcome.Held > 0 {
		c.emit(Event{
			Kind:      EventHeld,
			Worker:    WorkerImport,
			Message:   "packages from the peer are waiting in the incoming channel",
			Held:      outcome.Held,
			Conflicts: outcome.Conflicts,
			Paths:     capPaths(outcome.ConflictPaths),
		})
	}
	// Both of these are failures the drain reported without refusing, so nothing
	// else in this pass will mention them. See ImportOutcome.
	c.problem("import bookkeeping", outcome.Unfinished)
	c.problem("import housekeeping", outcome.Cleanup)
}

func (c *companion) reportExport(outcome *ExportOutcome) {
	if outcome == nil || (outcome.Packages == 0 && len(outcome.Burned) == 0) {
		return
	}
	c.emit(Event{
		Kind:     EventExported,
		Worker:   WorkerExport,
		Message:  "this device's commits were published to the outgoing channel",
		Packages: outcome.Packages,
		Detail:   burnedDetail(outcome.Burned),
	})
}

func (c *companion) reportRetire(outcome *RetireOutcome) {
	if outcome == nil {
		return
	}
	if removed := len(outcome.Retired) + len(outcome.Released); removed > 0 {
		c.emit(Event{
			Kind:     EventRetired,
			Worker:   WorkerRetire,
			Message:  "acknowledged packages were removed from the outgoing channel",
			Packages: removed,
			Awaiting: outcome.Awaiting,
		})
	}
	for _, rejected := range outcome.Rejected {
		if c.rejected[rejected.PackageID] {
			continue
		}
		c.rejected[rejected.PackageID] = true
		c.emit(Event{
			Kind:      EventRejected,
			Worker:    WorkerRetire,
			Message:   "a receipt arrived for one of this device's packages and did not verify",
			PackageID: rejected.PackageID,
			Detail:    rejected.Detail,
		})
	}
}

func (c *companion) problem(subject, detail string) {
	if detail == "" {
		return
	}
	c.emit(Event{
		Kind:    EventProblem,
		Subject: subject,
		Message: "a step after the change was durable did not complete",
		Detail:  detail,
	})
}

// burnedDetail names sequence numbers spent on an interrupted publication. See
// ExportOutcome.Burned: nothing else records where the gap came from.
func burnedDetail(burned []uint64) string {
	if len(burned) == 0 {
		return ""
	}
	return fmt.Sprintf("%d sequence number(s) were burned by an interrupted publication", len(burned))
}

// headlines is the one sentence each state deserves when it is announced. A
// closed vocabulary rather than a format string, so nothing a vault contains can
// reach a log line through it.
var headlines = map[State]string{
	StateUnenrolled:       "the vault is not enrolled in synchronization",
	StateIdle:             "both replicas are up to date as far as this device can tell",
	StateLocalSaved:       "edits are on disk and not yet in the content history",
	StateExportPending:    "the content history holds commits this device has not published",
	StateTransportPending: "packages are published and the peer has not acknowledged them",
	StatePeerApplication:  "packages from the peer are on this disk and not yet in this vault",
	StateConflict:         "a package is held because this vault disagrees with what it expected",
	StateBlocked:          "the content history is blocked and a person must clear it",
	StateDisconnected:     "nothing has been heard from the peer for long enough to be worth checking",
	StateFailing:          "a worker has been failing and is retrying under a backoff",
}

// headlineMessage reports that sentence, falling back to the state's own name so
// a state added without a sentence is still legible rather than blank.
func headlineMessage(state State) string {
	if message, named := headlines[state]; named {
		return message
	}
	return string(state)
}
