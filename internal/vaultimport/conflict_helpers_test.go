package vaultimport

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// interruptAfterCommit drives one package through the applier as far as the
// content transaction and stops there, leaving the disk a crash at crashApplied
// would leave. It is done in-process rather than through the subprocess helper
// because the state under test is the attempt record's contents rather than the
// timing, and the steps are the production ones either way.
func interruptAfterCommit(t *testing.T, r *replicas, packageDir string) {
	t.Helper()
	session, err := accept(r.root(), packageDir, Options{Now: r.clock.Now})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := session.read(packageDir); err != nil {
		t.Fatalf("read: %v", err)
	}
	found, err := session.inspect()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(found.conflicts) != 0 {
		t.Fatalf("the package conflicts before it was applied: %v", found.conflicts)
	}
	session.mark(found)
	entryID, err := journal.NewEntryID(r.clock.Now())
	if err != nil {
		t.Fatalf("NewEntryID: %v", err)
	}
	operation, err := noteops.BeginOperation(r.root(), operationToken, "bn sync apply --test",
		session.targets(), session.opts.recorderOptions(entryID)...)
	if err != nil {
		t.Fatalf("BeginOperation: %v", err)
	}
	txn, err := session.plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	declareRecorded(t, session, entryID)
	if err := session.commit(txn, operation); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// declareRecorded takes the two steps apply takes before the transaction, in
// its order: provenance first, then the attempt.
func declareRecorded(t *testing.T, session *applier, entryID string) {
	t.Helper()
	if err := session.recordProvenance(entryID); err != nil {
		t.Fatalf("recordProvenance: %v", err)
	}
	if err := session.declare(entryID); err != nil {
		t.Fatalf("declare: %v", err)
	}
}

// importState reads this vault's import record the way a second process would.
func importState(t *testing.T, r *replicas) *State {
	t.Helper()
	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state
}

// heldOn returns the durable hold recorded for one package, failing when there
// is none: a refusal nobody wrote down is the state this session exists to end.
func heldOn(t *testing.T, r *replicas, packageID string) *ConflictRecord {
	t.Helper()
	state := importState(t, r)
	record, held := state.Conflicts[packageID]
	if !held {
		t.Fatalf("%s was refused without being held; the record holds %v", packageID, state.Conflicts)
	}
	if record.PackageID != packageID {
		t.Fatalf("the hold for %s names %s", packageID, record.PackageID)
	}
	return record
}

// onlyContested returns the single contested path of a hold.
func onlyContested(t *testing.T, record *ConflictRecord) PathConflict {
	t.Helper()
	if len(record.Paths) != 1 {
		t.Fatalf("the hold names %d contested paths, want 1: %+v", len(record.Paths), record.Paths)
	}
	return record.Paths[0]
}

// requireBranch asserts that one branch was both named and kept, and that the
// bytes it points at are the bytes it claims. Recording a digest is cheap and a
// record that only did that would pass an assertion about existence while
// offering a person nothing to merge.
func requireBranch(t *testing.T, r *replicas, packageID string, branch Branch, want, context string) {
	t.Helper()
	if !branch.Exists {
		t.Fatalf("%s: recorded as absent, want the content %q", context, want)
	}
	if branch.SHA256 != vaultsync.HashBytes([]byte(want)) {
		t.Fatalf("%s: recorded digest %s, which is not the digest of %q", context, branch.SHA256, want)
	}
	if branch.Bytes == "" {
		t.Fatalf("%s: named %s but kept no copy of it", context, branch.SHA256)
	}
	dir, err := conflictDir(r.root(), packageID)
	if err != nil {
		t.Fatalf("%s: conflictDir: %v", context, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, branch.Bytes))
	if err != nil {
		t.Fatalf("%s: read the retained copy: %v", context, err)
	}
	if string(data) != want {
		t.Fatalf("%s: the retained copy holds %q, want %q", context, data, want)
	}
}

func requireAbsentBranch(t *testing.T, branch Branch, context string) {
	t.Helper()
	if branch.Exists || branch.SHA256 != "" || branch.Bytes != "" {
		t.Fatalf("%s: recorded %+v, want an absence", context, branch)
	}
}

// requireHeldQuietly asserts that a decision which applied nothing also left
// nothing: no recovery entry, no attempt for a later import to trip over, no
// movement in the content history, and nothing of the retained store inside it.
func requireHeldQuietly(t *testing.T, r *replicas, head string, commits int) {
	t.Helper()
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("an import that applied nothing left %d recovery entries behind", len(entries))
	}
	if state := importState(t, r); state.Pending != nil {
		t.Fatal("an import that applied nothing declared an attempt, which strands every later import")
	}
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("an import that applied nothing moved the content history")
	}
	if got := commitCount(t, r.pair.Local); got != commits {
		t.Fatalf("the vault holds %d commits, want %d", got, commits)
	}
	// The retained branches are copies of notes that are not those notes, so the
	// store they live in must be outside what the content history tracks.
	store := path.Join(vaultsync.StateDir(), conflictsName)
	if vaultgit.InScope(store) {
		t.Fatalf("%q is inside the content history's scope", store)
	}
}

// requireConvergedResult asserts the shape of an application that had nothing to
// do: recorded as applied so a dependent can proceed, and acknowledged as
// convergence without claiming a recovery entry for content it never wrote.
func requireConvergedResult(t *testing.T, r *replicas, manifest *vaultsync.Manifest, result *Result) {
	t.Helper()
	if !result.Converged {
		t.Fatal("a package this vault had already applied was not reported as converged")
	}
	requireConvergedReceipt(t, result.Receipt)
	if result.ReceiptPath == "" {
		t.Fatal("the convergence acknowledgment was not published")
	}
	if result.PackageID != manifest.PackageID {
		t.Fatalf("the result names %s, want %s", result.PackageID, manifest.PackageID)
	}
	record, applied := importState(t, r).Applied[manifest.PackageID]
	if !applied {
		t.Fatal("a converged package was not recorded as applied, so a dependent would wait on it forever")
	}
	requireConvergedRecord(t, record)
}

func requireConvergedReceipt(t *testing.T, receipt *vaultsync.Receipt) {
	t.Helper()
	if receipt == nil || !receipt.Converged || receipt.RecoveryEntry != "" {
		t.Fatalf("a converged package returned receipt %+v, want an explicit convergence acknowledgment", receipt)
	}
}

func requireConvergedRecord(t *testing.T, record Record) {
	t.Helper()
	if !record.Converged || record.JournalEntry != "" || record.Receipt == nil || !record.Receipt.Converged {
		t.Fatalf("the record is %+v, want a converged one naming no entry and holding its receipt", record)
	}
}

// foldsNames reports whether this filesystem answers one name for two spellings.
// It is asked of a throwaway directory beside the fixture vaults rather than of
// the vault itself, so the probe cannot be mistaken for a note.
func foldsNames(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "bn-fold-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("probe the filesystem: %v", err)
	}
	defer func() {
		if err := os.Remove(probe); err != nil {
			t.Fatalf("remove the probe: %v", err)
		}
	}()
	_, err := os.Lstat(filepath.Join(dir, "BN-FOLD-PROBE"))
	return err == nil
}
