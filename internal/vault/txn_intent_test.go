package vault

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// A commit is a sequence of durable state transitions, and the only honest way
// to test what an interruption leaves behind is to actually stop a process at
// each one. os.Exit inside the crash hook skips every deferred cleanup, exactly
// as a kill does, so the parent inspects the same disk a restart would see.
//
// The transaction under test covers all three kinds of change at once — a
// create, an overwrite, and a delete — because they use three different
// primitives and each one has its own half-finished state.
const (
	txnCrashExitCode = 3
	txnCrashPriorB   = "original\n"
	txnCrashNewA     = "created\n"
	txnCrashNewB     = "modified\n"
	txnCrashPriorC   = "doomed\n"
)

// txnCrashPaths are the transaction's destinations in commit order, which is
// sorted path order, so applied:0 is the create, applied:1 the overwrite, and
// applied:2 the delete.
var txnCrashPaths = []string{"a-create.md", "b-modify.md", "c-delete.md"}

// TestTxnCrashHelper is the child process. It plans one fixed transaction and
// terminates at the named durable state transition.
func TestTxnCrashHelper(t *testing.T) {
	if os.Getenv("BN_TXN_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestFileTxnRecoversFromEveryDurableTransition")
	}
	root := os.Getenv("BN_TXN_CRASH_ROOT")
	point := os.Getenv("BN_TXN_CRASH_POINT")
	txnCrashHook = func(fired string) {
		if fired == point {
			os.Exit(txnCrashExitCode)
		}
	}

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("NewFileTxn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a-create.md"), []byte(txnCrashNewA), 0o644, ExpectMissing()); err != nil {
		t.Fatalf("plan create: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "b-modify.md"), []byte(txnCrashNewB), 0o644, ExpectContent([]byte(txnCrashPriorB))); err != nil {
		t.Fatalf("plan overwrite: %v", err)
	}
	if err := txn.Remove(filepath.Join(root, "c-delete.md"), ExpectContent([]byte(txnCrashPriorC))); err != nil {
		t.Fatalf("plan delete: %v", err)
	}
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func newTxnCrashVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b-modify.md"), []byte(txnCrashPriorB), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c-delete.md"), []byte(txnCrashPriorC), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func runTxnCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestTxnCrashHelper", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		"BN_TXN_CRASH_HELPER=1",
		"BN_TXN_CRASH_ROOT="+root,
		"BN_TXN_CRASH_POINT="+point,
	)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != txnCrashExitCode {
		t.Fatalf("helper interrupted at %q = %v, want exit %d\n%s", point, err, txnCrashExitCode, output)
	}
}

// intentRecords lists the intent files currently on disk.
func intentRecords(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(txnIntentRelDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read intent directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), txnIntentSuffix) {
			names = append(names, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(names)
	return names
}

// vaultState reads the three destinations, using a sentinel for absence, so a
// whole outcome can be compared in one line.
func vaultState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := make(map[string]string, len(txnCrashPaths))
	for _, rel := range txnCrashPaths {
		data, err := os.ReadFile(filepath.Join(root, rel))
		switch {
		case err == nil:
			state[rel] = string(data)
		case os.IsNotExist(err):
			state[rel] = "<missing>"
		default:
			t.Fatalf("read %s: %v", rel, err)
		}
	}
	return state
}

func sameState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

var (
	txnCrashBefore = map[string]string{
		"a-create.md": "<missing>",
		"b-modify.md": txnCrashPriorB,
		"c-delete.md": txnCrashPriorC,
	}
	txnCrashAfter = map[string]string{
		"a-create.md": txnCrashNewA,
		"b-modify.md": txnCrashNewB,
		"c-delete.md": "<missing>",
	}
)

// A process killed at any durable state transition must restart into exactly one
// of two states: the transaction never started, or it finished. The intent
// record is what decides which, and the boundary is the moment the record
// becomes durable — before it, nothing is recoverable because nothing was
// applied; after it, every remaining rename is completed.
func TestFileTxnRecoversFromEveryDurableTransition(t *testing.T) {
	cases := []struct {
		point    string
		recorded bool
		want     map[string]string
	}{
		// Staged and journaled, but no record exists yet, so nothing was applied
		// and nothing has to be.
		{point: txnCrashStaged, recorded: false, want: txnCrashBefore},
		// The record is durable and no destination has been touched: recovery
		// rolls all three forward.
		{point: txnCrashIntent, recorded: true, want: txnCrashAfter},
		{point: txnCrashApplied(0), recorded: true, want: txnCrashAfter},
		{point: txnCrashApplied(1), recorded: true, want: txnCrashAfter},
		{point: txnCrashApplied(2), recorded: true, want: txnCrashAfter},
		// Everything landed and is durable; only the record is left to clear.
		{point: txnCrashSynced, recorded: true, want: txnCrashAfter},
	}

	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			root := newTxnCrashVault(t)
			runTxnCrashHelper(t, root, tc.point)

			records := intentRecords(t, root)
			if tc.recorded != (len(records) == 1) {
				t.Fatalf("intent records after %q = %v, want recorded=%v", tc.point, records, tc.recorded)
			}

			if err := recoverTxnIntents(root); err != nil {
				t.Fatalf("recoverTxnIntents: %v", err)
			}
			got := vaultState(t, root)
			if !sameState(got, tc.want) {
				t.Fatalf("state after recovery = %v, want %v", got, tc.want)
			}
			if left := intentRecords(t, root); len(left) != 0 {
				t.Fatalf("recovery left %v behind", left)
			}

			// Idempotence: a second pass has nothing to decide and must change
			// nothing, which is what makes recovery safe to run on every open and
			// every write.
			if err := recoverTxnIntents(root); err != nil {
				t.Fatalf("second recoverTxnIntents: %v", err)
			}
			if again := vaultState(t, root); !sameState(again, got) {
				t.Fatalf("a second recovery pass changed the vault: %v -> %v", got, again)
			}
		})
	}
}

// Opening the vault is the earliest moment a restart can heal itself, and it is
// the one every command goes through.
func TestVaultOpenRecoversAnInterruptedTransaction(t *testing.T) {
	root := newTxnCrashVault(t)
	runTxnCrashHelper(t, root, txnCrashIntent)

	if _, err := Open(&config.Config{Vault: root}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := vaultState(t, root); !sameState(got, txnCrashAfter) {
		t.Fatalf("state after Open = %v, want %v", got, txnCrashAfter)
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("Open left %v behind", left)
	}
}

// Taking a path lock is what every in-vault writer does before it touches the
// filesystem, so recovery runs there too: a process that never opened a Vault
// still cannot mutate a vault holding an unfinished transaction.
func TestPathLockRecoversAnInterruptedTransaction(t *testing.T) {
	root := newTxnCrashVault(t)
	runTxnCrashHelper(t, root, txnCrashApplied(0))

	release, err := LockPaths(root, filepath.Join(root, "unrelated.md"))
	if err != nil {
		t.Fatalf("LockPaths: %v", err)
	}
	release()
	if got := vaultState(t, root); !sameState(got, txnCrashAfter) {
		t.Fatalf("state after locking = %v, want %v", got, txnCrashAfter)
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("locking left %v behind", left)
	}
}

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
	runTxnCrashHelper(t, root, txnCrashIntent)

	external := "someone else saved this\n"
	if err := os.WriteFile(filepath.Join(root, "b-modify.md"), []byte(external), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-create.md"), []byte(external), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	if err := recoverTxnIntents(root); err != nil {
		t.Fatalf("recoverTxnIntents: %v", err)
	}
	got := vaultState(t, root)
	if got["b-modify.md"] != external {
		t.Fatalf("recovery overwrote a foreign edit: %q", got["b-modify.md"])
	}
	if got["a-create.md"] != external {
		t.Fatalf("recovery overwrote a file that appeared at the created path: %q", got["a-create.md"])
	}
	// The delete was untouched by the external writer, so it still completes.
	if got["c-delete.md"] != "<missing>" {
		t.Fatalf("the interrupted delete was not completed: %q", got["c-delete.md"])
	}
	if left := intentRecords(t, root); len(left) != 0 {
		t.Fatalf("recovery left %v behind", left)
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
