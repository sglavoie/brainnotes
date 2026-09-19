package noteops

import (
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// UndoOptions controls one restore of a journal entry.
type UndoOptions struct {
	// Force overwrites paths whose current state no longer matches the journaled
	// post-command state.
	Force bool
	// DryRun previews the restore and writes nothing.
	DryRun bool
	// Command is the full invocation recorded in the new journal entry the
	// restore itself produces.
	Command string
	// Require pins each recorded path to the state the restore was authorized
	// against, and is forwarded verbatim to journal.RestoreOptions.Require — see
	// there for why a caller that showed somebody what would be replaced has to
	// state it inside the transaction rather than check it beforehand.
	Require map[string]vault.FileExpectation
}

// Undo restores one journal entry through the same begin/finalize contract every
// other mutation goes through.
//
// An undo is not a privileged rewind. It reads snapshots the vault already
// holds, but what it does to the working tree is a write like any other: it can
// overwrite a note, and it can remove one. So it gets the same treatment —
// location policy and Git safety preflight before the first snapshot is read,
// its own journal entry recording what it is about to change, and a local
// snapshot of the result before it is called a success. The journal entry is
// what lets the safety engine commit a removal an undo performs: without it the
// engine would see a note vanish with nothing explaining it, and would block the
// vault over a change bn made deliberately.
//
// A dry run never begins an operation. Previewing an undo stays available on a
// vault that cannot currently be written — which is exactly when a person most
// wants to look at one.
//
// A returned plan can still carry a RestorePlan.Cleanup: the restore landed, was
// journaled and was snapshotted, and a transaction temp file outlived it. That
// is deliberately not this function's error, because stopping here over it would
// skip Finalize and leave the writes unauthorized — but it is a fact a caller
// must pass on rather than drop, since nothing else will mention the file again.
func Undo(vaultRoot, entryID string, opts UndoOptions) (*journal.RestorePlan, error) {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return nil, Errorf(CodeInvalidRequest, "undo: entry id is required")
	}
	restoreOpts := journal.RestoreOptions{Force: opts.Force, DryRun: opts.DryRun, Require: opts.Require}
	if opts.DryRun {
		return journal.Restore(vaultRoot, JournalRoot(vaultRoot), entryID, restoreOpts)
	}

	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "bn undo " + entryID
	}
	// Targets are left unnamed: which paths an entry touches is only known after
	// it is loaded, and loading it is already inside Restore. The vault root is
	// guarded here, and every individual path is guarded again by the transaction
	// underneath, so nothing escapes — the early refusal is simply coarser.
	j, err := BeginOperation(vaultRoot, "undo", command, nil)
	if err != nil {
		return nil, err
	}
	restoreOpts.Journal = j

	plan, err := journal.Restore(vaultRoot, JournalRoot(vaultRoot), entryID, restoreOpts)
	if err != nil {
		return nil, err
	}
	if _, err := j.Finalize(); err != nil {
		return nil, err
	}
	return plan, nil
}
