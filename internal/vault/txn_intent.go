package vault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The recovery-intent protocol.
//
// FileTxn stages every replacement before it touches a destination, so a crash
// before the first rename leaves the vault untouched. What a crash *in the
// middle* of the rename sequence leaves is a partly applied transaction, and
// nothing on disk says which half landed: a note holding its old bytes looks
// exactly like a note whose write was never planned.
//
// The intent record is that missing statement. It is written and fsynced —
// together with its containing directory — after staging and journaling are
// durable and strictly before the first destructive step, and it is removed only
// after every destination has been renamed and every parent directory synced. So
// a record that survives a restart means exactly one thing: a transaction was
// prepared and may or may not have finished, and these are the paths it was
// going to change.
//
// Recovery rolls forward, never back. By the time a destination has been
// replaced, the bytes it displaced have already been unlinked (removeSuperseded
// runs inside the swap), so the pre-transaction content is not available to
// restore. What *is* still available is the staged replacement, so recovery
// finishes the transaction using the same content-judging primitives the forward
// path uses. Each one refuses to overwrite content it cannot prove, which is what
// makes recovery safe to run against a vault another writer has since edited.
//
// Every decision is derived from the filesystem rather than from a phase counter:
// a path that already holds the planned bytes is done, a path that still holds
// the bytes staging read is completed, and a path holding neither belongs to a
// writer outside this transaction and is left alone. That is what makes recovery
// idempotent — running it twice, or running it after it already succeeded, is a
// sequence of no-ops.
const (
	// txnIntentRelDir holds one record per prepared transaction. It sits beside
	// the lock directory under .bnotes, which Walk already prunes and which the
	// Git safety engine's tracked scope already excludes, so an intent can never
	// be mistaken for vault content or committed as one.
	txnIntentRelDir = ".bnotes/txn"
	// txnIntentSuffix names an intent record. The stem is the operation ID.
	txnIntentSuffix = ".intent"
	// txnIntentVersion is the on-disk record version. A record written by a build
	// that understands more than this one is refused rather than guessed at.
	txnIntentVersion = 1
	// txnPhasePrepared is the only phase a record is ever written in: every
	// change is staged and durable, and the destinations may or may not have been
	// reached. There is deliberately no second phase — advancing one would cost an
	// fsync per change to record something recovery reads off the filesystem
	// anyway, and a phase counter that lags the filesystem is worse than no
	// counter at all.
	txnPhasePrepared = "prepared"
	// txnIntentRecovery is the operator-facing instruction stored in every record.
	// It never names content, only paths and digests.
	txnIntentRecovery = "bn finishes this automatically on the next vault open or write. " +
		"To resolve it by hand: for each change, compare the file at \"path\" against " +
		"\"prior_sha256\" and \"new_sha256\". A path matching new_sha256 is already done; a path " +
		"matching prior_sha256 can be completed from the file named by \"staged\"; a path matching " +
		"neither was changed by another writer and only a person can decide which version wins. " +
		"Delete this file once every listed path is resolved."
)

// txnIntentChange is one planned change as the record describes it. It carries
// digests and sizes, never note content: the bytes themselves live in the staged
// temp file, which is already on disk and already fsynced.
type txnIntentChange struct {
	Path        string `json:"path"`
	Remove      bool   `json:"remove"`
	Staged      string `json:"staged,omitempty"`
	Mode        uint32 `json:"mode,omitempty"`
	PriorExists bool   `json:"prior_exists"`
	PriorSHA256 string `json:"prior_sha256,omitempty"`
	PriorSize   int64  `json:"prior_size"`
	NewSHA256   string `json:"new_sha256,omitempty"`
	NewSize     int64  `json:"new_size"`
}

// txnIntentRecord is the versioned on-disk record.
type txnIntentRecord struct {
	Version    int               `json:"version"`
	ID         string            `json:"id"`
	Root       string            `json:"root"`
	Phase      string            `json:"phase"`
	ObservedAt time.Time         `json:"observed_at"`
	Recovery   string            `json:"recovery"`
	Changes    []txnIntentChange `json:"changes"`
}

// TxnIntentError reports a prepared transaction that restart recovery could not
// finish. The record is deliberately left on disk, so every later mutation
// retries the recovery and keeps failing until it converges or a person resolves
// the named paths. That is the fail-closed answer to not knowing whether an
// interrupted transaction landed.
type TxnIntentError struct {
	// IntentPath is the record that could not be cleared.
	IntentPath string
	// Paths lists the destinations recovery could not decide, in commit order.
	Paths []string
	// Err is the underlying failure.
	Err error
}

func (e *TxnIntentError) Error() string {
	if e == nil {
		return "vault: transaction intent recovery failed"
	}
	if len(e.Paths) == 0 {
		return fmt.Sprintf("vault: interrupted transaction %s could not be recovered: %v", e.IntentPath, e.Err)
	}
	return fmt.Sprintf(
		"vault: interrupted transaction %s could not be recovered for %s: %v; the vault refuses further writes until it is resolved",
		e.IntentPath, strings.Join(e.Paths, ", "), e.Err)
}

func (e *TxnIntentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTxnIntentRecoveryFailure reports whether err is an unfinished transaction
// intent that is blocking further mutation.
func IsTxnIntentRecoveryFailure(err error) bool {
	var intent *TxnIntentError
	return errors.As(err, &intent)
}

// txnCrashHook is nil in production. Tests set it in a subprocess and call
// os.Exit from it, which skips every deferred cleanup exactly the way a killed
// process does — the only way to observe what a real interruption leaves on
// disk. It follows the same seam convention as txnHooks and the volume probe.
var txnCrashHook func(point string)

const (
	// txnCrashStaged fires once the staged replacements and the journal entry are
	// durable and before any record of them exists.
	txnCrashStaged = "staged"
	// txnCrashIntent fires once the record is durable and before the first rename.
	txnCrashIntent = "intent"
	// txnCrashSynced fires once every destination and parent directory is durable
	// and before the record is cleared.
	txnCrashSynced = "synced"
)

// txnCrashApplied names the point just after the change at index reached its
// destination. Each one is a durable state transition of its own.
func txnCrashApplied(index int) string {
	return fmt.Sprintf("applied:%d", index)
}

func txnCrash(point string) {
	if txnCrashHook != nil {
		txnCrashHook(point)
	}
}

// txnIntent is a live record plus the exclusive flock that marks it live. The
// lock is held on the record's own descriptor for the whole commit, so a
// recovering process can tell an in-flight transaction from an abandoned one
// without a heartbeat, a PID, or a timeout.
type txnIntent struct {
	dir  string
	path string
	file *os.File
	done bool
}

// writeIntent records the transaction's prepared state. It runs after staging
// and journaling, so every byte the record refers to is already on disk.
func (t *FileTxn) writeIntent() (*txnIntent, error) {
	changes := make([]txnIntentChange, 0, len(t.order))
	for _, path := range t.order {
		change := t.changes[path]
		record := txnIntentChange{
			Path:        path,
			Remove:      change.remove,
			Staged:      change.staged,
			Mode:        uint32(change.mode.Perm()),
			PriorExists: change.priorExists,
		}
		if change.priorExists {
			record.PriorSHA256 = HashBytes(change.priorData)
			record.PriorSize = int64(len(change.priorData))
		}
		if !change.remove {
			record.NewSHA256 = change.contentDigest()
			record.NewSize = change.contentSize()
		}
		changes = append(changes, record)
	}
	return writeTxnIntent(t.root, changes)
}

func writeTxnIntent(root string, changes []txnIntentChange) (*txnIntent, error) {
	realRoot, dir, err := txnIntentPaths(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vault: create intent directory %q: %w", dir, err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("vault: intent identifier: %w", err)
	}
	record := txnIntentRecord{
		Version:    txnIntentVersion,
		ID:         hex.EncodeToString(raw[:]),
		Root:       realRoot,
		Phase:      txnPhasePrepared,
		ObservedAt: time.Now().UTC(),
		Recovery:   txnIntentRecovery,
		Changes:    changes,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("vault: encode intent: %w", err)
	}

	path := filepath.Join(dir, record.ID+txnIntentSuffix)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("vault: create intent %q: %w", path, err)
	}
	intent := &txnIntent{dir: dir, path: path, file: file}
	if lockingSupported {
		held, lockErr := tryLock(file, true)
		if lockErr != nil {
			intent.abandon()
			return nil, fmt.Errorf("vault: lock intent %q: %w", path, lockErr)
		}
		if !held {
			// The name is random and freshly created under O_EXCL, so this cannot
			// happen without something else writing into the intent directory.
			intent.abandon()
			return nil, fmt.Errorf("vault: intent %q is already held", path)
		}
	}
	if _, err := file.Write(data); err != nil {
		intent.abandon()
		return nil, fmt.Errorf("vault: write intent %q: %w", path, err)
	}
	// The record and its directory entry both have to survive power loss before
	// the first rename runs, or a crash would leave a partly applied transaction
	// with nothing on disk to describe it.
	if err := file.Sync(); err != nil {
		intent.abandon()
		return nil, fmt.Errorf("vault: fsync intent %q: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		intent.abandon()
		return nil, err
	}
	return intent, nil
}

// clear removes the record and makes its absence durable. It is idempotent and
// safe to defer: the commit calls it explicitly once every destination is
// durable, and the deferred call then does nothing.
//
// The record is unlinked before the flock is released, so a recovering process
// never sees a lockable record for a transaction that already finished.
func (i *txnIntent) clear() error {
	if i == nil || i.done {
		return nil
	}
	i.done = true
	var removeErr error
	if err := os.Remove(i.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		removeErr = fmt.Errorf("vault: remove intent %q: %w", i.path, err)
	}
	syncErr := syncDir(i.dir)
	i.close()
	return errors.Join(removeErr, syncErr)
}

// abandon drops a record that was never completed. It is only reachable while
// the record is still being created, so nothing destructive has run and there is
// nothing for a later recovery to finish.
func (i *txnIntent) abandon() {
	if i == nil {
		return
	}
	i.done = true
	_ = os.Remove(i.path)
	i.close()
}

func (i *txnIntent) close() {
	if i == nil || i.file == nil {
		return
	}
	if lockingSupported {
		_ = unlockFile(i.file)
	}
	_ = i.file.Close()
	i.file = nil
}

// txnIntentPaths resolves the vault root and the intent directory under it.
// Resolution matters for the same reason it does for lock names: two processes
// reaching one vault by different symlinked names must meet on the same
// directory, or each would recover only its own records.
func txnIntentPaths(root string) (realRoot, dir string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", errors.New("vault: intent needs a root")
	}
	realRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("vault: resolve vault root %q: %w", root, err)
	}
	return realRoot, filepath.Join(realRoot, filepath.FromSlash(txnIntentRelDir)), nil
}
