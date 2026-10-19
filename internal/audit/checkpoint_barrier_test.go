package audit

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// barrierDoor is one function that must reach the checkpoint barrier.
//
// The list is the executable form of internal/checkpoint's "what participates"
// section. A checkpoint's promise is about *every* durable change, so it is
// exactly as true as this list is complete — and a list in prose is one nobody
// notices going stale.
type barrierDoor struct {
	// file is the repository-relative source file.
	file string
	// recv is the receiver type for a method, empty for a plain function. It is
	// spelled without the pointer star.
	recv string
	// fn is the function or method name.
	fn string
	// calls is what the body must contain. Most doors join directly; two
	// packages route through a local joinBarrier helper, which appears in this
	// table on its own line so the indirection is still checked rather than
	// merely tolerated.
	calls []string
	// why says what would be captured half-done without this join. It is what a
	// person removing a row has to argue against.
	why string
}

var (
	joinsBarrier = []string{
		"checkpoint.Join", "checkpoint.JoinWithin",
		"checkpoint.JoinContext", "checkpoint.JoinContextWithin",
	}
	holdsBarrier   = []string{"checkpoint.Hold"}
	joinsViaHelper = []string{"joinBarrier"}
)

// barrierDoors is every door through which this program makes a durable change.
//
// Two kinds of entry are deliberately absent. Pure readers are not here because
// a checkpoint does not exclude reads. And neither is internal/backup's restore
// path: backup.Restore is a leaf whose production caller is
// noteops.RestoreBackup, which is on this list and which wraps the extraction,
// the preflight and the snapshot in one join.
var barrierDoors = []barrierDoor{
	{
		file:  "internal/noteops/journal.go",
		fn:    "BeginOperation",
		calls: []string{"BeginOperationContext"},
		why:   "every journaled note mutation begins here, and the join must precede the first vault lock",
	},
	{
		file:  "internal/noteops/journal.go",
		fn:    "BeginOperationContext",
		calls: joinsBarrier,
		why:   "the context-aware implementation owns the admission and passes its nesting token through Git finalization",
	},
	{
		file:  "internal/noteops/journal.go",
		fn:    "SnapshotEntry",
		calls: []string{"SnapshotEntryContext"},
		why:   "re-snapshotting a finalized entry writes the content history outside BeginOperation",
	},
	{
		file:  "internal/noteops/journal.go",
		fn:    "SnapshotEntryContext",
		calls: joinsBarrier,
		why:   "import recovery reuses its outer admission while a top-level recovery starts a fresh one",
	},
	{
		file:  "internal/noteops/backup_restore.go",
		fn:    "RestoreBackup",
		calls: []string{"RestoreBackupContext"},
		why:   "the context-free door every non-CLI caller restores through",
	},
	{
		file:  "internal/noteops/backup_restore.go",
		fn:    "RestoreBackupContext",
		calls: holdsBarrier,
		why:   "--wipe empties the vault before it writes anything back, so the window holds a vault with no notes in it",
	},
	{
		file:  "internal/vaultstate/vaultstate.go",
		fn:    "Save",
		calls: []string{"SaveContext"},
		why:   "the one write path for brief.json, cockpit.json and recall.json, each of which is also snapshotted",
	},
	{
		file:  "internal/vaultstate/vaultstate.go",
		fn:    "SaveContext",
		calls: joinsBarrier,
		why:   "the context-aware sidecar writer owns the admission through its exact-path Git snapshot",
	},
	{
		file:  "internal/vaultgit/preflight.go",
		recv:  "Manager",
		fn:    "acquireWithin",
		calls: joinsBarrier,
		why:   "the autosave watcher commits the content history on its own schedule, with no joined caller above it",
	},
	{
		file:  "internal/vaultgit/manager.go",
		recv:  "Manager",
		fn:    "checkpointMutation",
		calls: joinsBarrier,
		why:   "block, watch, and push retry records mutate operational Git state outside the safety lock",
	},
	{
		file:  "internal/vaultgit/manager.go",
		recv:  "Manager",
		fn:    "ExternalCommitDue",
		calls: joinsBarrier,
		why:   "polling conditionally republishes the persisted watch schedule",
	},
	{
		file:  "internal/vaultimport/lock.go",
		fn:    "lockImports",
		calls: []string{"lockImportsContext"},
		why:   "one import is one join: Apply, Resolve, Recover and Drain all pass through this guard",
	},
	{
		file:  "internal/vaultimport/lock.go",
		fn:    "lockImportsContext",
		calls: joinsBarrier,
		why:   "the import guard's join, shared by imports and by re-enrollment's reset, which rides its held checkpoint",
	},
	{
		file:  "internal/vaultexport/state_reset.go",
		fn:    "joinBarrierContext",
		calls: joinsBarrier,
		why:   "the context-aware helper supports both fresh export writes and provenance nested under an import",
	},
	{
		file:  "internal/vaultexport/state_reset.go",
		fn:    "joinBarrier",
		calls: []string{"joinBarrierContext"},
		why:   "the helper the three export state writers route through",
	},
	{
		file:  "internal/vaultexport/export.go",
		fn:    "Export",
		calls: joinsViaHelper,
		why:   "publishing moves the cursor and the path ledger, which a copy must not catch between",
	},
	{
		file:  "internal/vaultexport/state.go",
		fn:    "RecordImportContext",
		calls: []string{"joinBarrierContext"},
		why:   "the context-aware provenance writer keeps the import-wide admission through ledger publication",
	},
	{
		file:  "internal/vaultexport/state.go",
		fn:    "RecordImport",
		calls: []string{"RecordImportContext"},
		why:   "provenance written between a copy's start and its end would let the peer's change echo back",
	},
	{
		file:  "internal/vaultexport/state_reset.go",
		fn:    "Reset",
		calls: joinsViaHelper,
		why:   "re-baselining rewrites the whole export record",
	},
	{
		file:  "internal/vaultsync/enrollment_store.go",
		fn:    "joinBarrier",
		calls: joinsBarrier,
		why:   "the helper both enrollment writers route through",
	},
	{
		file:  "internal/vaultsync/enrollment_store.go",
		fn:    "createEnrollment",
		calls: joinsViaHelper,
		why:   "enrollment is what says who this vault accepts packages from",
	},
	{
		file:  "internal/vaultsync/enrollment_store.go",
		fn:    "updateEnrollment",
		calls: joinsViaHelper,
		why:   "an epoch advance captured half-written is a vault that accepts the wrong packages",
	},
	{
		file:  "internal/vaultsync/restore_review.go",
		fn:    "createRestoreReview",
		calls: joinsBarrier,
		why:   "the fail-closed restore marker must be included wholly in every checkpoint",
	},
	{
		file:  "internal/vaultsync/restore_review.go",
		fn:    "replaceRestoreReview",
		calls: joinsBarrier,
		why:   "a new restore operation must replace any archived marker as one fail-closed checkpoint transition",
	},
	{
		file:  "internal/vaultsync/restore_review.go",
		fn:    "UpdateRestoreReviewContext",
		calls: joinsBarrier,
		why:   "a checkpoint must not capture a review decision between its restartable phases",
	},
	{
		file:  "internal/vaultsync/restore_review.go",
		fn:    "ReplaceEnrollmentForReviewContext",
		calls: joinsBarrier,
		why:   "the reviewed device identity, epoch and peer set are one causal transition",
	},
	{
		file:  "internal/vaultsync/restore_review.go",
		fn:    "ClearRestoreReviewContext",
		calls: joinsBarrier,
		why:   "the quarantine may disappear only inside the checkpoint that finished resetting causal state",
	},
	{
		file:  "internal/vaultexport/state_reset.go",
		fn:    "RebaselineAfterReviewContext",
		calls: []string{"joinBarrierContext"},
		why:   "a reviewed baseline replaces the producer cursor and sequence ledger as one record",
	},
	{
		file:  "internal/vaultimport/state_validate.go",
		fn:    "ResetAfterReviewContext",
		calls: []string{"lockImportsContext"},
		why:   "stale applications and receipts must disappear atomically under the reviewed enrollment",
	},
	{
		file:  "internal/vaultrecovery/review_apply.go",
		fn:    "ApplyPlan",
		calls: holdsBarrier,
		why:   "archiving channels, replacing enrollment and resetting both causal ledgers is one restore transition",
	},
	{
		file:  "internal/vaultsyncd/record.go",
		recv:  "Record",
		fn:    "save",
		calls: joinsBarrier,
		why:   "a worker transition is what a restored vault resumes its schedule from",
	},
	{
		file:  "internal/vaultsyncd/worker.go",
		fn:    "runRetire",
		calls: joinsBarrier,
		why:   "package and receipt retirement is a separately durable sync transition",
	},
	{
		file:  "internal/attachments/store.go",
		recv:  "Store",
		fn:    "Stage",
		calls: joinsBarrier,
		why:   "a staged upload is three durable steps — blob, destination claim, record — and is one join",
	},
	{
		file:  "internal/attachments/store_release.go",
		recv:  "Store",
		fn:    "Discard",
		calls: joinsBarrier,
		why:   "discarding removes the same three artifacts staging created",
	},
	{
		file:  "internal/attachments/store_release.go",
		recv:  "Store",
		fn:    "Forget",
		calls: joinsBarrier,
		why:   "forgetting removes staging files after a commit, and a copy must not hold half of them",
	},
	{
		file:  "internal/attachments/sweep.go",
		recv:  "Store",
		fn:    "Sweep",
		calls: joinsBarrier,
		why:   "the sweep removes expired staging trees, which is a removal like any other",
	},
	{
		file:  "internal/recall/state_persistence.go",
		recv:  "StateStore",
		fn:    "mutateLocked",
		calls: joinsBarrier,
		why:   "it joins before its own vault lock, because vaultstate.Save would otherwise join from inside one",
	},
	{
		file:  "internal/brief/state_file.go",
		recv:  "FileStateStore",
		fn:    "writeState",
		calls: joinsBarrier,
		why:   "activity kept outside the vault is copied together with it, so the pair is only consistent under one barrier",
	},
	{
		file:  "internal/backup/backup.go",
		fn:    "Create",
		calls: holdsBarrier,
		why:   "this repository's own checkpoint client: a walk of a live vault reads each file at a different instant",
	},
	{
		file:  "internal/cli/checkpoint_run.go",
		fn:    "runCheckpointRun",
		calls: holdsBarrier,
		why:   "the invocable door a deployment snapshot in another language reaches the barrier through",
	},
}

// TestEveryWriterJoinsTheBarrier is what keeps internal/checkpoint's participant
// list from silently losing a member.
//
// A checkpoint promises that everything acknowledged is durable and nothing is
// part-way through. That promise is not held by the barrier package — it is held
// by every writer agreeing to join it, and a writer that stops agreeing removes
// the promise for the whole vault while leaving every test of the barrier itself
// passing. Nothing about a missing join is visible at runtime until a copy taken
// during that write is restored, which is the worst possible moment to find out.
//
// Like the direct-write audit beside it, this is deliberately syntactic. It
// cannot tell a correctly placed join from one taken after a lock; the lock
// ordering is argued in internal/checkpoint's documentation and exercised by its
// concurrency tests. What this can do, and what nothing else does, is notice a
// door that no longer reaches the barrier at all.
func TestEveryWriterJoinsTheBarrier(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, door := range barrierDoors {
		if strings.TrimSpace(door.why) == "" {
			t.Errorf("%s has no reason recorded", door)
			continue
		}
		body, err := functionBody(filepath.Join(root, filepath.FromSlash(door.file)), door.recv, door.fn)
		if err != nil {
			// A rename would otherwise leave this row passing over nothing.
			t.Errorf("%s: %v", door, err)
			continue
		}
		if !bodyCallsAny(body, door.calls) {
			t.Errorf("%s does not call any of %s.\nIt is a barrier participant because %s.\nEither join the barrier there or remove the row and say why in internal/checkpoint's documentation.",
				door, strings.Join(door.calls, " or "), door.why)
		}
	}
}

// TestBarrierDoorsAreTheJoinSites is the other direction of the same check.
//
// The table above proves every listed door still joins. It cannot prove the
// table lists every door, because a new writer package is invisible to a list
// that does not mention it. So this asks the source instead: every production
// call to checkpoint.Join, checkpoint.JoinContext, or checkpoint.Hold must be
// inside a function the table names.
//
// A new join is therefore not an error — it is a prompt to add the row, with the
// sentence saying what it protects. That sentence is the part a future reader
// needs and the part nobody writes unless something asks.
func TestBarrierDoorsAreTheJoinSites(t *testing.T) {
	t.Parallel()

	listed := map[string]bool{}
	for _, door := range barrierDoors {
		listed[door.String()] = true
	}

	unlisted, err := collectUnlistedBarrierSites(repositoryRoot(t), listed)
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	sort.Strings(unlisted)
	for _, site := range unlisted {
		t.Errorf("%s reaches the checkpoint barrier but is not in barrierDoors; add a row saying what it protects", site)
	}
}

// collectUnlistedBarrierSites walks every production Go file under root and
// gathers the functions that reach the barrier without a row naming them.
func collectUnlistedBarrierSites(root string, listed map[string]bool) ([]string, error) {
	var unlisted []string
	err := walkProductionFiles(root, func(path, rel string) error {
		// The barrier's own package joins and holds all over itself; it is the
		// implementation, not a participant.
		if filepath.ToSlash(filepath.Dir(rel)) == "internal/checkpoint" {
			return nil
		}
		sites, err := barrierCallSites(path, rel)
		if err != nil {
			return err
		}
		for _, site := range sites {
			if !listed[site] {
				unlisted = append(unlisted, site)
			}
		}
		return nil
	})
	return unlisted, err
}

func (d barrierDoor) String() string {
	if d.recv == "" {
		return d.file + ":" + d.fn
	}
	return d.file + ":" + d.recv + "." + d.fn
}
