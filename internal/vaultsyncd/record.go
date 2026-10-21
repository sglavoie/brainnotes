package vaultsyncd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/sglavoie/brainnotes/internal/agent"
	"github.com/sglavoie/brainnotes/internal/buildinfo"
	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// recordVersion is the schema version of the companion record. Unlike the export
// and import records this one is checked as a *discard* condition rather than a
// refusal; see LoadRecord.
const recordVersion = 1

// recordFileName is the record's name inside the synchronization state
// directory.
const recordFileName = "companion.json"

// recordPerm matches the other records in the directory. It holds worker names,
// timestamps, and sanitized failure text — no note content and no secrets.
const recordPerm fs.FileMode = 0o644

// WorkerName identifies one unit of a pass. The three of them are the whole of
// what this companion does.
type WorkerName string

const (
	// WorkerImport drains the incoming channel.
	WorkerImport WorkerName = "import"
	// WorkerExport publishes this device's commits.
	WorkerExport WorkerName = "export"
	// WorkerRetire removes published packages the peer has acknowledged.
	WorkerRetire WorkerName = "retire"
)

// workerOrder is the order a pass runs its units in, and it is the lock-ordering
// invariant this package rests on. See the package documentation: import must
// precede export so an applied change is in the export ledger before the
// exporter reaches its commit, and neither may overlap the other.
var workerOrder = []WorkerName{WorkerImport, WorkerExport, WorkerRetire}

// WorkerRecord is what one worker's scheduling looks like between runs.
type WorkerRecord struct {
	// LastRunAt and LastSuccessAt are when the worker last ran at all and when it
	// last ran without failing. They differ exactly while something is wrong.
	LastRunAt     time.Time `json:"last_run_at"`
	LastSuccessAt time.Time `json:"last_success_at"`
	// Failures is the consecutive failure count, reset by any success. It is what
	// the backoff is computed from, and it is persisted so a companion that is
	// restarted every minute by a supervisor cannot retry a failing operation
	// every minute forever.
	Failures int `json:"failures,omitempty"`
	// LastError is the sanitized text of the last failure, kept until a success
	// clears it. It is the evidence a status read reports, and keeping it is the
	// difference between a vault that says "exports have been failing since
	// Tuesday" and one that looks idle.
	LastError string `json:"last_error,omitempty"`
	// NextAttemptAfter is when the worker may run again. It is derived from
	// Failures and stored rather than recomputed so that the schedule survives a
	// restart exactly as it stood.
	NextAttemptAfter time.Time `json:"next_attempt_after"`
}

// Record is the companion's own durable state: scheduling and failure evidence,
// and deliberately nothing else.
//
// Nothing here is needed to reconstruct what has been synchronized. The export
// cursor, the import ledger, the conflict records, the published packages and
// the receipts are all owned by other packages and are what a restart resumes
// from. This file only decides *when* to try next, which is why losing it is
// cheap.
type Record struct {
	Version int `json:"version"`
	// Build is the build identifier (internal/buildinfo) of the companion that
	// last wrote the record. A status read compares it with its own, which is
	// how a companion binary that was replaced on disk but never restarted is
	// noticed. Empty when the writer could not say what it was.
	Build   string                      `json:"build,omitempty"`
	Workers map[WorkerName]WorkerRecord `json:"workers,omitempty"`
}

// ErrNewerRecord is LoadRecord's refusal of a record written by a newer schema.
// Unlike every other unusable record it is not discarded: an older companion
// that reset it would overwrite a newer companion's schedule and failure
// evidence with a format that newer companion then has to throw away. The record
// is left exactly as it is and the pass does no work until a matching build runs.
var ErrNewerRecord = errors.New("vaultsyncd: the companion record was written by a newer bn-vault-sync")

// RecordPath returns the absolute path of the companion record for the vault at
// root.
//
// Derived from vaultsync.StateDir for the reason vaultexport.StatePath is: a
// literal path beside a derived one is how a record ends up outside the
// directory the portable policy excludes, at which point this device's failure
// log becomes content the peer receives.
func RecordPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), recordFileName)))
}

// LoadRecord reads the companion record, and reports an empty one for anything
// it cannot use.
//
// The discard is the decision worth defending. Every other durable record in
// this system fails closed — an unreadable export cursor or import ledger stops
// the world, because continuing would mean re-sending a change or re-applying
// one. Nothing in this file has that property: the worst a forgotten backoff can
// do is retry a failing operation once, immediately, and every operation it
// schedules is independently idempotent and independently guarded. Failing
// closed here would let a truncated scheduling file stop a vault from
// synchronizing at all, which is a strictly worse outcome than one early retry.
//
// The error is returned alongside the empty record rather than swallowed, so a
// caller that wants to say so can. Nothing is obliged to act on it — except for
// a record from a newer schema, which wraps ErrNewerRecord and is the one case
// the empty record must not be saved over; see ErrNewerRecord.
func LoadRecord(root string) (*Record, error) {
	fresh := &Record{Version: recordVersion, Workers: map[WorkerName]WorkerRecord{}}
	data, err := os.ReadFile(RecordPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return fresh, nil
		}
		return fresh, fmt.Errorf("vaultsyncd: read companion record: %w", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return fresh, fmt.Errorf("vaultsyncd: parse companion record: %w", err)
	}
	if record.Version > recordVersion {
		return fresh, fmt.Errorf("%w: it is version %d and this build writes %d; run the matching build rather than letting this one reset it",
			ErrNewerRecord, record.Version, recordVersion)
	}
	if record.Version != recordVersion {
		return fresh, fmt.Errorf("vaultsyncd: companion record is version %d, this build writes %d",
			record.Version, recordVersion)
	}
	if record.Workers == nil {
		record.Workers = map[WorkerName]WorkerRecord{}
	}
	return &record, nil
}

// save writes the record durably.
//
// vault.GuardMutation then vault.WriteStateFile, rather than vaultstate.Save,
// for the reason vaultexport.saveState gives: Save's third guarantee is a
// snapshot into the content history, `.bnotes/sync/` is out of that history's
// scope by design, and Save would silently drop the snapshot rather than refuse.
// Taking the two guarantees that do apply — the storage-location guard and the
// path-continuous replacement — states what is actually true of this file.
//
// It takes no lock. Two companions cannot both be running against one vault, and
// the single-instance lock in Run is what makes that true; see runLocked.
// It does join the checkpoint barrier, which is not a lock in the same sense:
// the barrier is not about excluding another companion, it is about not being
// copied half-written. A worker transition recorded here is what a restored
// vault resumes from, and one captured mid-replacement would resume from a state
// no pass ever reached.
func (r *Record) save(root, build string) error {
	barrier, err := checkpoint.Join(root)
	if err != nil {
		return fmt.Errorf("vaultsyncd: %w", err)
	}
	defer barrier()
	r.Version = recordVersion
	if buildinfo.Known(build) {
		r.Build = build
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("vaultsyncd: encode companion record: %w", err)
	}
	data = append(data, '\n')
	recordPath := RecordPath(root)
	if err := vault.GuardMutation(root, recordPath); err != nil {
		return err
	}
	if err := vault.WriteStateFile(recordPath, data, recordPerm); err != nil {
		return fmt.Errorf("vaultsyncd: write companion record: %w", err)
	}
	return nil
}

// worker reports one worker's record, zero-valued when it has never run.
func (r *Record) worker(name WorkerName) WorkerRecord {
	if r.Workers == nil {
		return WorkerRecord{}
	}
	return r.Workers[name]
}

// succeeded records a clean run: the streak is cleared, the evidence with it,
// and there is nothing to wait for before the next attempt.
func (r *Record) succeeded(name WorkerName, at time.Time) {
	r.set(name, WorkerRecord{LastRunAt: at.UTC(), LastSuccessAt: at.UTC()})
}

// failed records a failure, lengthens the streak, and schedules the retry.
//
// The previous success time is carried forward. It is the field that says how
// long this has been broken, and a failure that reset it would erase the one
// number an operator needs to judge whether to act now or after lunch.
func (r *Record) failed(name WorkerName, at time.Time, err error) {
	previous := r.worker(name)
	at = at.UTC()
	failures := previous.Failures + 1
	r.set(name, WorkerRecord{
		LastRunAt:        at,
		LastSuccessAt:    previous.LastSuccessAt,
		Failures:         failures,
		LastError:        agent.SanitizedDiagnostic(err),
		NextAttemptAfter: at.Add(backoffFor(failures)),
	})
}

// declined records that a worker was due, was asked, and had nothing to do. It
// reports whether that changed anything.
//
// The streak and its evidence are cleared; LastRunAt and LastSuccessAt are left
// exactly as they were, because nothing ran and claiming otherwise would erase
// the number that says how long this has been broken.
//
// Clearing is the point. A streak is otherwise only ever cleared by a *success*,
// so a worker that failed and then stopped being asked would carry StateFailing
// — and `bn-vault-sync -check`'s exit 1 — forever, on the evidence of a failure
// nothing is retrying any more. That is reachable rather than theoretical: an
// export that failed is backed off for minutes, and a person who repairs what
// broke it — or simply publishes by hand — leaves nothing to publish by the time
// it is due. A worker that is due and finds nothing to do is not a failing
// worker.
//
// Two doors reach it: a skip decided before the worker runs, and a refusal from
// the worker that proves the same thing — see vacant, which is also why
// ErrImportBusy is not one of them.
//
// What clearing must not do is hide a producer that has *halted*, since that
// vault also has nothing to publish. It does not, because such a vault never
// reaches here: the halts are reported as problems by the status read (see
// Status.readCursor), a status carrying a problem runs the export anyway, and a
// worker that runs and refuses is recorded as failing rather than declining.
func (r *Record) declined(name WorkerName) bool {
	previous := r.worker(name)
	if previous.Failures == 0 && previous.LastError == "" && previous.NextAttemptAfter.IsZero() {
		return false
	}
	previous.Failures, previous.LastError, previous.NextAttemptAfter = 0, "", time.Time{}
	r.set(name, previous)
	return true
}

func (r *Record) set(name WorkerName, worker WorkerRecord) {
	if r.Workers == nil {
		r.Workers = map[WorkerName]WorkerRecord{}
	}
	r.Workers[name] = worker
}

// ready reports whether a worker may run at now. A worker with no record has
// never run and is always ready.
//
// A stored deadline further ahead than MaxBackoff is ready too. No failure can
// schedule one that far out, so it is evidence the wall clock stepped backwards
// since it was written, and honouring it would park the worker for as long as
// the clock had jumped.
func (r *Record) ready(name WorkerName, now time.Time) bool {
	next := r.worker(name).NextAttemptAfter
	now = now.UTC()
	return next.IsZero() || !now.Before(next) || next.Sub(now) > MaxBackoff
}

// WorkerStatus is one worker's scheduling as a status read reports it.
type WorkerStatus struct {
	Name             WorkerName `json:"name"`
	LastRunAt        time.Time  `json:"last_run_at"`
	LastSuccessAt    time.Time  `json:"last_success_at"`
	Failures         int        `json:"failures,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	NextAttemptAfter time.Time  `json:"next_attempt_after"`
	// Ready is whether the worker may run at the instant the status was read.
	Ready bool `json:"ready"`
}

// statuses reports every worker that has ever run, in workerOrder.
//
// A worker with no record is left out rather than reported as a zero: a
// companion that has never run has nothing to say about its workers, and three
// rows of zeroes would read as three workers that ran and did nothing.
func (r *Record) statuses(now time.Time) []WorkerStatus {
	var statuses []WorkerStatus
	for _, name := range workerOrder {
		worker, recorded := r.Workers[name]
		if !recorded {
			continue
		}
		statuses = append(statuses, WorkerStatus{
			Name:             name,
			LastRunAt:        worker.LastRunAt,
			LastSuccessAt:    worker.LastSuccessAt,
			Failures:         worker.Failures,
			LastError:        worker.LastError,
			NextAttemptAfter: worker.NextAttemptAfter,
			Ready:            r.ready(name, now),
		})
	}
	return statuses
}
