package noteops

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// JournalRoot returns the vault-local undo journal directory.
func JournalRoot(vaultRoot string) string {
	return filepath.Join(vaultRoot, filepath.FromSlash(config.DefaultJournalDir))
}

// NewRecorder creates a journal recorder using the vault's default journal root.
func NewRecorder(vaultRoot, command string, opts ...journal.RecorderOption) (*journal.Recorder, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "bn"
	}
	recorder, err := journal.NewRecorder(vaultRoot, JournalRoot(vaultRoot), command, opts...)
	if err != nil {
		// The recorder consults the mutation guard before it creates anything, and
		// that refusal is a policy answer about the vault's location rather than a
		// journal malfunction. Calling it a journal error would report it as a 500
		// and bury the one sentence that says what to do about it.
		if vault.IsForbiddenMutation(err) {
			return nil, WrapError(CodeForbidden, err.Error(), err)
		}
		return nil, WrapError(CodeJournal, "create journal entry", err)
	}
	return recorder, nil
}

// Journal is one mutating operation's safety and undo context.
//
// It owns the whole lifecycle of a change: the refusals that must happen before
// anything exists on disk, the lazily created undo recorder, and the local Git
// snapshot that must land before the operation may be called a success. Nothing
// mutates a vault outside this type except the low-level persistence packages
// listed in TestNoUnannotatedDirectWrites.
type Journal struct {
	vaultRoot string
	// operation is the short token that names what kind of change this is —
	// "rename", "capture", "undo". It is what reaches the commit message, and it
	// is deliberately not derived from command: command holds the full bn
	// invocation, note names and property values included, and putting that in a
	// commit message would publish note content to every clone of the vault.
	operation string
	command   string
	opts      []journal.RecorderOption
	recorder  *journal.Recorder
	// safety is the engine resolved at Begin, or nil for a vault with no content
	// history. It is captured once so the snapshot at the end goes to the same
	// engine the preflight at the start consulted.
	safety Safety
}

// BeginOperation is the one path a journaled mutation starts through.
//
// It runs both refusals before the operation is allowed to create anything: the
// storage-location policy over the vault root and every target the caller can
// name yet, and then the Git safety preflight, which is what catches a vault
// left blocked by an earlier failure or holding a deletion nothing explains.
// Both run ahead of recorder creation, lock acquisition, folder creation, and
// temporary files, because those artifacts are exactly what a refusal is
// supposed to prevent from existing.
//
// operation names the change for the commit message and must be a fixed token
// chosen by the code, never anything derived from user input. command is the
// full invocation and goes only into the undo journal, which stays local.
//
// Targets the caller cannot know yet are not a problem: the same policy is
// enforced again underneath every individual write, so passing what is known
// makes the refusal early rather than making it possible.
func BeginOperation(vaultRoot, operation, command string, targets []string, opts ...journal.RecorderOption) (*Journal, error) {
	if err := guardMutation(vaultRoot, targets...); err != nil {
		return nil, err
	}
	safety, err := SafetyFor(vaultRoot)
	if err != nil {
		return nil, err
	}
	if safety != nil {
		if err := safety.Preflight(context.Background()); err != nil {
			return nil, classifySafetyError("git safety preflight", err)
		}
	}
	return &Journal{
		vaultRoot: vaultRoot,
		operation: strings.TrimSpace(operation),
		command:   strings.TrimSpace(command),
		opts:      append([]journal.RecorderOption(nil), opts...),
		safety:    safety,
	}, nil
}

// Capture snapshots path's pre-mutation state, creating the recorder on demand.
func (j *Journal) Capture(path string) error {
	if j == nil {
		return nil
	}
	if j.recorder == nil {
		recorder, err := NewRecorder(j.vaultRoot, j.command, j.opts...)
		if err != nil {
			return err
		}
		j.recorder = recorder
	}
	if err := j.recorder.Capture(path); err != nil {
		return WrapError(CodeJournal, "capture journal state", err)
	}
	return nil
}

// Finalize commits the undo entry and then records the local Git snapshot for
// it. It returns nil when nothing was captured.
//
// The order is the contract. The manifest is written and made durable first,
// because it is the only thing that authorizes committing a deletion; the
// snapshot is taken second, staging exactly the paths that manifest names. An
// operation is a success only when both have happened, so a snapshot failure is
// returned as a *SnapshotError rather than swallowed — the change is on disk and
// the caller has to be told that the history does not cover it.
//
// A no-op operation captures nothing, finalizes nothing, and commits nothing, so
// running one on a clean vault cannot leave an empty commit behind.
func (j *Journal) Finalize() (*journal.Entry, error) {
	if j == nil || j.recorder == nil {
		return nil, nil
	}
	entry, err := j.recorder.Finalize()
	if err != nil {
		return nil, WrapError(CodeJournal, "finalize journal entry", err)
	}
	if err := j.snapshot(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// snapshot records the finalized entry in the vault's content history.
func (j *Journal) snapshot(entry *journal.Entry) error {
	if j == nil || j.safety == nil || entry == nil {
		return nil
	}
	if _, err := j.safety.CommitJournalEntry(context.Background(), entry.ID, j.operation); err != nil {
		return &SnapshotError{
			Operation: j.operation,
			EntryID:   entry.ID,
			Blocked:   vaultgitBlocks(err),
			Err:       err,
		}
	}
	return nil
}

// Operation reports the token this journal's snapshot commits under.
func (j *Journal) Operation() string {
	if j == nil {
		return ""
	}
	return j.operation
}
