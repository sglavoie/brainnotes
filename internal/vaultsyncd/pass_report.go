package vaultsyncd

import (
	"fmt"
)

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
			Message:  "acknowledged and backup-covered packages were removed from the outgoing channel",
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
	StateRestoreReview:    "synchronization is quarantined until restored peers are reviewed and re-enrolled",
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
