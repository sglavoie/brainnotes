package noteops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// sourceSaveLedgerDir holds one keyed save's recorded outcome, as one file per
// save id.
//
// Two decisions are encoded in that sentence. It lives under `.bnotes/`, which
// puts it outside `vaultgit.InScope`, so the record never reaches the content
// history: it is this device's bookkeeping about a *request*, not a change to
// the vault, and committing it would add a second path to every save's snapshot
// and a second file to every `bn diff`. And it is one file per id rather than
// one shared ledger, because a shared file would make every keyed save a writer
// of one hot path — two saves of two unrelated notes would contend, and the
// loser would be told its note conflicted when nothing had touched it.
const sourceSaveLedgerDir = ".bnotes/source-saves"

// sourceSaveLedgerCap bounds how many records the directory keeps. A record is
// only ever read by a retry of the request that wrote it, so an old one is dead
// weight; the cap is generous enough that a burst of editor saves cannot evict a
// record a client is still retrying against.
const sourceSaveLedgerCap = 256

// sourceSaveIDMaxLen bounds a save id. The id reaches disk only as a digest of
// itself, so the bound is about refusing an absurd request rather than about
// path safety.
const sourceSaveIDMaxLen = 128

// sourceSaveRecordVersion is the record format this build writes and reads.
const sourceSaveRecordVersion = 1

// sourceSaveRecord is one keyed save's persisted outcome.
//
// It holds digests and a vault-relative path, never note bytes, so it is as safe
// to read, mail, or paste into an issue as a transaction intent is.
//
// There is deliberately no journal id. The undo entry's identity does not exist
// until the transaction's first capture, which happens inside the same Commit
// that writes this record, so a record written atomically with the note cannot
// name it. The atomicity is worth more than the field: it is what makes the
// record's *existence* proof that the bytes landed. A replay therefore reports
// the resulting revision and whether the note was created, and leaves the undo
// entry to `bn history`.
type sourceSaveRecord struct {
	Version int    `json:"version"`
	SaveID  string `json:"save_id"`
	// Digest is the sha256 of the content that was submitted, which is also the
	// revision the note ended at. It is stored so a reused id carrying different
	// content is recognized rather than answered with somebody else's outcome.
	Digest   string `json:"digest"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Created  bool   `json:"created"`
	// Attachments is what the save put in the vault beside the note, so a replay
	// can tell the client its uploads landed. Omitted when there were none, and
	// absent from every record written before attachments existed — which decodes
	// to the empty list and is the truth about those saves.
	Attachments []SavedAttachment `json:"attachments,omitempty"`
}

// sourceSaveLedgerState is what is on disk at a save id's record path.
type sourceSaveLedgerState struct {
	record sourceSaveRecord
	// usable is false when nothing is there, and when what is there does not
	// decode as a record this build understands.
	usable bool
	// expect pins whatever was found, so a record this save has to replace is
	// replaced by compare-and-swap rather than by a blind overwrite.
	expect vault.FileExpectation
}

// sourceSaveLedgerWrite is the record write a save carries in its transaction.
type sourceSaveLedgerWrite struct {
	destination PathResolution
	expect      vault.FileExpectation
	record      sourceSaveRecord
}

// validateSourceSaveID refuses an id that could not be stored.
//
// The charset is narrow on purpose. The id is hashed before it becomes a path,
// so nothing here is load-bearing for containment — but an id is also echoed
// back in a refusal message, and one restricted to this alphabet cannot smuggle
// a newline or a control character into a log line.
func validateSourceSaveID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if len(id) > sourceSaveIDMaxLen {
		return Errorf(CodeInvalidRequest, "source: save id exceeds %d characters", sourceSaveIDMaxLen)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			continue
		default:
			return Errorf(CodeInvalidRequest, "source: save id may hold only letters, digits, hyphens and underscores")
		}
	}
	return nil
}

// sourceSaveLedgerPath resolves the record path for saveID.
func sourceSaveLedgerPath(vaultRoot, saveID string) (PathResolution, error) {
	sum := sha256.Sum256([]byte(saveID))
	rel := sourceSaveLedgerDir + "/" + hex.EncodeToString(sum[:]) + ".json"
	dest, err := PathForRel(vaultRoot, rel)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, "source: resolve save record path", err)
	}
	return dest, nil
}

// readSourceSaveLedgerState loads the record at absPath, treating an undecodable
// one as unusable rather than as a failure.
//
// Corrupt fails open, and the reason is the transaction intent's reason: the
// record is written whole inside a transaction, so a file that does not decode
// was never a complete record. A version this build does not understand is
// treated the same way, which is the one place this departs from the intent
// record's "unknown fails closed" — an intent guards note bytes, while this
// guards a retry, and refusing forever would wedge a save id on a vault that a
// newer build touched once. Either way the bytes found are pinned in expect, so
// replacing them is still a compare-and-swap.
func readSourceSaveLedgerState(absPath string) (sourceSaveLedgerState, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sourceSaveLedgerState{expect: vault.ExpectMissing()}, nil
		}
		return sourceSaveLedgerState{}, WrapError(CodeInternal, "source: read save record", err)
	}
	state := sourceSaveLedgerState{expect: vault.ExpectHash(vault.HashBytes(data))}
	var record sourceSaveRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version != sourceSaveRecordVersion {
		return state, nil
	}
	state.record = record
	state.usable = true
	return state, nil
}

// planSourceSaveLedger looks up saveID's record and decides what this save owes
// it. It returns at most one of a record write and a replayed outcome.
//
// Four answers. No id at all: nothing is recorded, and the save is protected by
// its base revision and by convergence alone. No record yet: this save will
// write one. A record whose digest matches these exact bytes: the save already
// happened, so its outcome is replayed rather than applied a second time — that
// is the duplicate submission and the lost response, which are one request twice
// and must not become two writes. A record whose digest or path differs: the id
// has been reused for different content, and replaying the old outcome is the
// one answer that would silently discard what was submitted, so it is refused
// loudly instead.
func planSourceSaveLedger(vaultRoot string, dest PathResolution, saveID, digest string) (*sourceSaveLedgerWrite, *SaveSourceResult, error) {
	saveID = strings.TrimSpace(saveID)
	if saveID == "" {
		return nil, nil, nil
	}
	ledger, err := sourceSaveLedgerPath(vaultRoot, saveID)
	if err != nil {
		return nil, nil, err
	}
	state, err := readSourceSaveLedgerState(ledger.AbsPath)
	if err != nil {
		return nil, nil, err
	}
	if !state.usable {
		return &sourceSaveLedgerWrite{destination: ledger, expect: state.expect}, nil, nil
	}
	if state.record.Digest != digest || state.record.RelPath != dest.RelPath {
		return nil, nil, Errorf(CodeConflict,
			"source: save id %q already recorded a different save of %s, so nothing was written; retry with a fresh save id",
			saveID, state.record.RelPath)
	}
	return nil, replayedSourceSave(dest, state.record), nil
}

// replayedSourceSave renders a recorded outcome as this call's answer.
//
// Changed is false and Replayed is true, which together say the honest thing:
// the note holds the submitted content, and this particular call did not put it
// there.
func replayedSourceSave(dest PathResolution, record sourceSaveRecord) *SaveSourceResult {
	return &SaveSourceResult{
		Destination: dest,
		Revision:    record.Revision,
		Created:     record.Created,
		Replayed:    true,
		Attachments: record.Attachments,
	}
}

// stageSourceSaveLedger adds the record to the note's own transaction.
//
// Staging it here rather than writing it after the commit is what makes the
// record trustworthy: the two land together or neither does, so a record that
// exists proves the note's bytes are on disk and the window where a save has
// happened but nothing remembers it does not exist. It also survives the one
// failure that most needs it — a snapshot that fails after the bytes landed
// leaves the vault refusing further mutations, and a replay reads the record
// without attempting one.
func stageSourceSaveLedger(txn *vault.FileTxn, ledger *sourceSaveLedgerWrite) error {
	if ledger == nil {
		return nil
	}
	data, err := json.Marshal(ledger.record)
	if err != nil {
		return WrapError(CodeInternal, "source: render save record", err)
	}
	if err := txn.Write(ledger.destination.AbsPath, append(data, '\n'), 0, ledger.expect); err != nil {
		return ClassifyTxnError("source: plan save record", err)
	}
	return nil
}

// pruneSourceSaveLedger drops the oldest records once the directory holds more
// than sourceSaveLedgerCap of them.
//
// This is housekeeping and it is deliberately *outside* the transaction. A
// removal inside it would put another path in the write's lock set and another
// record in its undo entry, and two saves that picked the same victim would
// conflict over a file neither of them owns. Outside, a removal that races
// another one is simply already done.
//
// Ordering is by modification time, which is a hint rather than a guarantee, and
// the cost of getting it wrong is stated here rather than waved away: an evicted
// record costs a replay. While the note still holds the recorded bytes the
// resubmission converges and the answer is the same by a different route, but
// once the note has moved on — the case the record exists for — convergence is
// unavailable and the resubmission is refused as a stale revision against an
// edit the client's own save landed. No content is lost either way, which is why
// a cap this generous is an acceptable trade rather than a silent one. Every
// failure is ignored for the same reason — an unwritable `.bnotes` must not turn
// a landed save into an error.
//
// Only this package's own record files are considered, by name. Nothing else
// should be in the directory, and the containment check on every source write
// refuses a destination under `.bnotes/` outright — but the one direct removal
// on this surface should not be the place that depends on that being true.
func pruneSourceSaveLedger(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type aged struct {
		name string
		at   time.Time
	}
	aging := make([]aged, 0, len(entries))
	for _, entry := range entries {
		// The cheap predicates decide whether this is one of ours before anything
		// is stat-ed, so an unrelated entry costs no syscall and "not a record" is
		// not folded into the same branch as "could not be read".
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		aging = append(aging, aged{name: entry.Name(), at: info.ModTime()})
	}
	if len(aging) <= sourceSaveLedgerCap {
		return
	}
	if sourceLedgerPruneHook != nil {
		sourceLedgerPruneHook()
	}
	sort.Slice(aging, func(i, j int) bool { return aging[i].at.Before(aging[j].at) })
	for i := 0; i+sourceSaveLedgerCap < len(aging); i++ {
		//bn:direct-write removes one of this package's own expired idempotency records, matched by the .json name it wrote, under a .bnotes/ directory no note may be created in; the file holds digests rather than note content, is out of the content history's scope, and its loss costs a replay rather than a note
		_ = os.Remove(filepath.Join(dir, aging[i].name))
	}
}

// sourceLedgerPruneHook is a test seam for proving pruning remains inside the
// operation's checkpoint admission. Production leaves it nil.
var sourceLedgerPruneHook func()
