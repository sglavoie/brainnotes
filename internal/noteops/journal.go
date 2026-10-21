package noteops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	ctx       context.Context
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
	// barrier is this operation's place in the checkpoint barrier, held from
	// before the first refusal until Release. It is nil only for the internal
	// journal built by SnapshotEntry, which finishes an operation somebody else
	// joined for and is itself called from inside a join.
	barrier func()
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
//
// It also joins the vault's checkpoint barrier, before either refusal and so
// before any lock, and the caller owns that join until it calls Release. Every
// caller must `defer` Release immediately: an operation whose join is dropped
// only on the success path holds the barrier open on every error return, and
// under bn serve that blocks every later checkpoint in the process. Finalize
// does not release, because an operation is not over when its manifest is — the
// snapshot that makes the change part of the history is inside the window a
// checkpoint must not cut.
func BeginOperation(vaultRoot, operation, command string, targets []string, opts ...journal.RecorderOption) (*Journal, error) {
	return BeginOperationContext(context.Background(), vaultRoot, operation, command, targets, opts...)
}

// contextOrBackground is ctx, or a background context for an options struct a
// caller built without one. Every option type that carries a context reads it
// through this, so leaving it unset keeps the pre-context behaviour.
func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// BeginOperationContext is BeginOperation for a caller that may already own a
// checkpoint admission. The returned context token is kept by the Journal and
// passed to Git safety, so its nested durable work cannot deadlock a checkpoint
// that is waiting for this operation to finish.
func BeginOperationContext(ctx context.Context, vaultRoot, operation, command string, targets []string, opts ...journal.RecorderOption) (*Journal, error) {
	// An operation whose caller has already gone — a CLI interrupted before
	// the write began — is refused here, before it creates anything.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	joinedCtx, barrier, err := checkpoint.JoinContext(ctx, vaultRoot)
	if err != nil {
		return nil, classifyBarrierError(err)
	}
	j := &Journal{
		ctx:       joinedCtx,
		vaultRoot: vaultRoot,
		operation: strings.TrimSpace(operation),
		command:   strings.TrimSpace(command),
		opts:      append([]journal.RecorderOption(nil), opts...),
		barrier:   barrier,
	}
	if err := j.begin(targets); err != nil {
		j.Release()
		return nil, err
	}
	return j, nil
}

// begin runs the two refusals that must happen before the operation is allowed
// to create anything.
func (j *Journal) begin(targets []string) error {
	if err := guardMutation(j.vaultRoot, targets...); err != nil {
		return err
	}
	safety, err := SafetyForContext(j.ctx, j.vaultRoot)
	if err != nil {
		return err
	}
	if safety != nil {
		if err := safety.Preflight(j.ctx); err != nil {
			return classifySafetyError("git safety preflight", err)
		}
	}
	j.safety = safety
	return nil
}

// Release drops this operation's place in the checkpoint barrier.
//
// It is idempotent and safe on a nil receiver, so the one shape every caller
// needs — `defer j.Release()` on the line after BeginOperation, whether or not
// the journal is nil in a dry run — is always correct.
func (j *Journal) Release() {
	if j == nil || j.barrier == nil {
		return
	}
	j.barrier()
	j.barrier = nil
}

// classifyBarrierError turns a refused barrier join into the vocabulary the rest
// of this package speaks, so a command refused because a backup is running says
// so instead of reporting a journal malfunction.
func classifyBarrierError(err error) error {
	if errors.Is(err, checkpoint.ErrBarrierHeld) {
		return WrapError(CodeBusy, "a checkpoint is copying this vault; try again shortly", err)
	}
	if vault.IsForbiddenMutation(err) {
		return WrapError(CodeForbidden, err.Error(), err)
	}
	return WrapError(CodeJournal, "join the checkpoint barrier", err)
}

// captureRelocation snapshots the pre-mutation state of a plan that moves a note
// from one path to another: both ends, and then every note whose links are about
// to be rewritten. The first capture creates the journal entry directory, so
// this is what makes the operation exist on disk.
//
// Rename and move share it because they share the shape — two ends and a
// rewrite set — and a capture one of them forgot would be an operation the undo
// manifest cannot reverse.
func captureRelocation(j *Journal, target, newPath string, rewritten []string) error {
	if err := j.Capture(target); err != nil {
		return err
	}
	if err := j.Capture(newPath); err != nil {
		return err
	}
	for _, p := range rewritten {
		if err := j.Capture(p); err != nil {
			return err
		}
	}
	return nil
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
// so the undo bytes are durable before history records the change; the
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
//
// The result is examined, not just the error. A snapshot that reports leaving
// one of the entry's paths out has not recorded this mutation, and calling that
// a success would tell the caller the history covers a change it does not. The
// engine already refuses rather than returning such a result, so this is the
// second half of a two-sided guarantee rather than a live branch — the operation
// that owes the user the sentence is the one that confirms it is not needed.
func (j *Journal) snapshot(entry *journal.Entry) error {
	if j == nil || j.safety == nil || entry == nil {
		return nil
	}
	result, err := j.safety.CommitJournalEntry(j.ctx, entry.ID, j.operation)
	if err != nil {
		return &SnapshotError{
			Operation: j.operation,
			EntryID:   entry.ID,
			Blocked:   vaultgitBlocks(err),
			Err:       err,
		}
	}
	if unrecorded := result.Unrecorded(); len(unrecorded) > 0 {
		return &SnapshotError{
			Operation: j.operation,
			EntryID:   entry.ID,
			Err:       fmt.Errorf("the snapshot left out %s", strings.Join(unrecorded, ", ")),
		}
	}
	return nil
}

// SnapshotEntry records an already-finalized journal entry in the vault's
// content history, under operation as the commit's token.
//
// It is Finalize's second half without its first, for the one caller that cannot
// use Finalize: a recovery pass resuming an operation whose process died between
// the manifest and the snapshot. The recorder that wrote that manifest is gone
// with the process, so there is nothing left to Finalize — but the entry is on
// disk, complete, and the change it describes is not in the history yet.
//
// An entry the history already holds is a no-op: the engine checks for its
// manifest under the same lock it commits beneath, because a caller that
// checked beforehand could have been overtaken by the watcher committing the
// same finalized entry. A caller that can cheaply tell the snapshot already
// happened should still say so by not calling — this answers the question with
// a `git ls-tree` — but it owes nothing for correctness.
//
// A vault with no content history has no engine and nothing to record, which is
// the same nil answer Finalize gives it.
func SnapshotEntry(vaultRoot, entryID, operation string) error {
	return SnapshotEntryContext(context.Background(), vaultRoot, entryID, operation)
}

// SnapshotEntryContext is SnapshotEntry with an explicit checkpoint nesting
// context for import recovery and other already-admitted callers.
func SnapshotEntryContext(ctx context.Context, vaultRoot, entryID, operation string) error {
	// A snapshot is a durable change to the content history, so it joins the
	// barrier like any other write. Its callers are recovery passes that have
	// usually joined already, and a nested join is counted rather than taken, so
	// this costs them nothing and covers the one that has not.
	joinedCtx, barrier, err := checkpoint.JoinContext(ctx, vaultRoot)
	if err != nil {
		return classifyBarrierError(err)
	}
	defer barrier()
	safety, err := SafetyForContext(joinedCtx, vaultRoot)
	if err != nil {
		return err
	}
	j := &Journal{ctx: joinedCtx, vaultRoot: vaultRoot, operation: strings.TrimSpace(operation), safety: safety}
	return j.snapshot(&journal.Entry{ID: entryID})
}

// EntryID reports the undo entry this operation will finalize under, or the
// empty string when nothing has been captured and no entry exists yet.
//
// The recorder is created lazily by the first Capture, which is deliberate — a
// refused operation must not leave an entry directory behind — so the identity
// exists only once the operation has actually touched something. Between that
// first capture and Finalize it is knowable, and that window is the only place a
// caller can record something that has to be durable before the snapshot: the
// provenance of an applied change, which the exporter reads back to recognize a
// commit as the peer's rather than this device's.
//
// An empty answer is not an error. It is what a no-op operation reports, and the
// caller that needs an identity is the one that knows whether it planned any
// change at all.
func (j *Journal) EntryID() string {
	if j == nil {
		return ""
	}
	return j.recorder.EntryID()
}

// Operation reports the token this journal's snapshot commits under.
func (j *Journal) Operation() string {
	if j == nil {
		return ""
	}
	return j.operation
}
