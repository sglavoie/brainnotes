package vault

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A record names paths, digests, and sizes so a person can resolve it by hand.
// It must never name content: it lives inside the vault, and the Git safety
// engine's scope policy is not the only thing standing between an operational
// artifact and a note's text.
func TestIntentRecordHoldsNoNoteContent(t *testing.T) {
	root := newTxnCrashVault(t)
	runTxnCrashHelper(t, root, txnCrashIntent)

	records := intentRecords(t, root)
	if len(records) != 1 {
		t.Fatalf("intent records = %v, want exactly one", records)
	}
	raw, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	for _, secret := range []string{txnCrashNewA, txnCrashNewB, txnCrashPriorB, txnCrashPriorC} {
		if strings.Contains(string(raw), strings.TrimSpace(secret)) {
			t.Fatalf("the record leaks note content %q:\n%s", secret, raw)
		}
	}

	var record txnIntentRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Version != txnIntentVersion || record.Phase != txnPhasePrepared {
		t.Fatalf("record header = version %d phase %q", record.Version, record.Phase)
	}
	if len(record.Changes) != len(txnCrashPaths) || record.Recovery == "" {
		t.Fatalf("record = %+v", record)
	}
	for _, change := range record.Changes {
		if change.Remove {
			if change.PriorSHA256 == "" || change.PriorSize == 0 {
				t.Fatalf("a removal records no prior identity: %+v", change)
			}
			continue
		}
		if change.NewSHA256 == "" || change.Staged == "" {
			t.Fatalf("a write records no replacement: %+v", change)
		}
	}
}

// Recovery rolls forward, and rolling forward must never overwrite a writer that
// got there first. A note edited by hand after the crash keeps its content and
// the record is still cleared, because a conflict a person has to judge is a
// decided outcome rather than a permanent block.
func TestRecoveryLeavesAForeignEditAlone(t *testing.T) {
	root := newTxnCrashVault(t)
	runTxnCrashHelper(t, root, txnCrashHandoff)

	external := "someone else saved this\n"
	if err := os.WriteFile(filepath.Join(root, "b-modify.md"), []byte(external), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Nested"), 0o755); err != nil {
		t.Fatalf("external mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Nested", "a-create.md"), []byte(external), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	if err := recoverTxnIntents(root); err != nil {
		t.Fatalf("recoverTxnIntents: %v", err)
	}
	got := vaultState(t, root)
	if got["b-modify.md"] != external {
		t.Fatalf("recovery overwrote a foreign edit: %q", got["b-modify.md"])
	}
	if got["Nested/a-create.md"] != external {
		t.Fatalf("recovery overwrote a file that appeared at the created path: %q", got["Nested/a-create.md"])
	}
	// The delete was untouched by the external writer, so it still completes.
	if got["c-delete.md"] != "<missing>" {
		t.Fatalf("the interrupted delete was not completed: %q", got["c-delete.md"])
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) != 3 {
		t.Fatalf("foreign edits must preserve every undecidable staged and prior copy, got %v", leftovers)
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("recovery left %v behind", left)
	}
}

func TestRecoveryPreservesRollbackCopyAcrossProcessDeath(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n", "z.md": "old-z\n"})
	cmd := exec.Command(os.Args[0], "-test.run=^TestTxnRollbackPreserveCrashHelper$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), txnRollbackPreserveEnv+"=1", "BN_TXN_CRASH_ROOT="+root)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != txnCrashExitCode {
		t.Fatalf("rollback helper = %v, want exit %d\n%s", err, txnCrashExitCode, output)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recoverTxnIntents(root); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "foreign\n" {
		t.Fatalf("foreign destination = %q", got)
	}
	paths := stagedTempPaths(t, root)
	if len(paths) != 1 || readFileString(t, paths[0]) != "old-a\n" {
		t.Fatalf("preserved rollback copies = %v, want one old-content copy", paths)
	}
}

// A record another process is committing right now is live, not abandoned. The
// exclusive flock its owner holds says so, and recovery must skip it rather than
// race the owner to the same destinations.
func TestRecoverySkipsALiveTransaction(t *testing.T) {
	if !lockingSupported {
		t.Skip("advisory locking is unavailable on this platform")
	}
	root := t.TempDir()
	intent, err := writeTxnIntent(root, []txnIntentChange{{
		Path:        filepath.Join(root, "held.md"),
		PriorExists: false,
		NewSHA256:   HashBytes([]byte("never written\n")),
	}})
	if err != nil {
		t.Fatalf("writeTxnIntent: %v", err)
	}
	defer func() { _ = intent.clear() }()

	if err := recoverTxnIntents(root); err != nil {
		t.Fatalf("recoverTxnIntents: %v", err)
	}
	if left := intentRecords(t, root); len(left) != 1 {
		t.Fatalf("recovery took over a live transaction's record: %v", left)
	}

	// Once the owner releases it, the same record is recoverable.
	if err := intent.clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("clear left %v behind", left)
	}
}

// A record that cannot be decoded proves the crash landed while it was still
// being written, because both it and its directory entry are fsynced before the
// first rename. Nothing was applied, so it is litter and self-heals.
func TestUnreadableIntentRecordIsDiscarded(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(txnIntentRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "truncated"+txnIntentSuffix), []byte(`{"version":1,"cha`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := recoverTxnIntents(root); err != nil {
		t.Fatalf("recoverTxnIntents: %v", err)
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("a truncated record survived: %v", left)
	}
}

// A record from a build that knows more than this one is intact and describes
// work this build cannot reason about. It fails closed: the record stays and
// every mutation is refused until a person resolves it.
func TestUnknownIntentVersionBlocksMutations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(txnIntentRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	record := txnIntentRecord{Version: txnIntentVersion + 1, ID: "future", Phase: txnPhasePrepared}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(dir, "future"+txnIntentSuffix)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := recoverTxnIntents(root); !IsTxnIntentRecoveryFailure(err) {
		t.Fatalf("recoverTxnIntents = %v, want an intent recovery failure", err)
	}
	if left := intentRecords(t, root); len(left) != 1 {
		t.Fatalf("a record this build cannot read must not be discarded: %v", left)
	}

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("NewFileTxn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "note.md"), []byte("x\n"), 0o644, ExpectMissing()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := txn.Commit(nil); !IsTxnIntentRecoveryFailure(err) {
		t.Fatalf("Commit = %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.md")); !os.IsNotExist(err) {
		t.Fatal("a refused commit still wrote its note")
	}
}

// The record decides which paths get renamed, so its claims are re-checked
// rather than trusted: a record naming a path outside the vault is refused
// instead of acted on.
func TestIntentRecordCannotReachOutsideTheVault(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(outside, []byte("not ours\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dir := filepath.Join(root, filepath.FromSlash(txnIntentRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	record := txnIntentRecord{
		Version: txnIntentVersion,
		ID:      "escape",
		Phase:   txnPhasePrepared,
		Changes: []txnIntentChange{{
			Path:        outside,
			Remove:      true,
			PriorExists: true,
			PriorSHA256: HashBytes([]byte("not ours\n")),
		}},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "escape"+txnIntentSuffix), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := recoverTxnIntents(root); !IsTxnIntentRecoveryFailure(err) {
		t.Fatalf("recoverTxnIntents = %v, want a refusal", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the out-of-vault file was touched: %v", err)
	}
}
