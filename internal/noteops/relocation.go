package noteops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// RelocationOptions tunes CommitRenamePlan and CommitMovePlan.
type RelocationOptions struct {
	// Force allows replacing an existing destination.
	Force bool
	// Out receives one "<rel>: updated" line per rewritten note.
	Out io.Writer
	// batch marks a journal other changes are recorded under too — a bulk move —
	// and batchMark is where this item's captures began. A relocation that fails
	// and compensates forgets only its own captures from such an entry; an entry
	// it owns is discarded, since nothing it recorded changed.
	batch     bool
	batchMark int
}

// RelocationIncompleteError reports a rename or move that failed after it had
// rewritten backlinks and could not put all of them back: the note is still at
// its old path while Paths hold rewritten links. The journal entry is kept, so
// its snapshots still hold what those notes said before.
type RelocationIncompleteError struct {
	Op    string
	Paths []string
	Err   error
}

func (e *RelocationIncompleteError) Error() string {
	return fmt.Sprintf("%s: note was not moved and the rewritten links in %s could not be restored: %v",
		e.Op, strings.Join(e.Paths, ", "), e.Err)
}

func (e *RelocationIncompleteError) Unwrap() error { return e.Err }

// CommitRenamePlan is the mutating tail of a rename, run under j after every
// path the plan touches has been captured: it re-checks the destination,
// rewrites the backlinks, and moves the note without replacing a destination
// that appeared since planning unless opts.Force.
//
// A failure after the first rewrite compensates: every note this call rewrote is
// put back from the bytes it held, under a precondition that it still holds what
// this call wrote, so the operation either lands whole or leaves the vault as it
// found it. Only a compensation that is itself refused returns a
// *RelocationIncompleteError.
func CommitRenamePlan(j *Journal, vaultRoot string, plan *RenamePlan, fmCfg *config.FrontmatterConfig, opts RelocationOptions) error {
	return relocation{
		op:      "rename",
		target:  plan.Target,
		newPath: plan.NewPath,
		files:   plan.FileOrder,
		check:   CheckRenameDestination,
		edit: func(n *vault.Note) error {
			return rewriteNoteLinks(n, plan.OldTitle, plan.NewTitle, fmCfg, wikilink.RewriteTitle)
		},
	}.commit(j, vaultRoot, fmCfg, opts)
}

// CommitMovePlan is CommitRenamePlan for a move.
func CommitMovePlan(j *Journal, vaultRoot string, plan *MovePlan, fmCfg *config.FrontmatterConfig, opts RelocationOptions) error {
	return relocation{
		op:      "move",
		target:  plan.Target,
		newPath: plan.NewPath,
		files:   plan.FileOrder,
		check:   CheckMoveDestination,
		edit: func(n *vault.Note) error {
			return rewriteNoteLinks(n, plan.OldRel, plan.NewRel, fmCfg, wikilink.RewriteMove)
		},
	}.commit(j, vaultRoot, fmCfg, opts)
}

// relocation is what rename and move share once the journal holds their
// captures: two ends, a rewrite set, and the edit applied to each note in it.
type relocation struct {
	op      string
	target  string
	newPath string
	files   []string
	check   func(target, newPath string, force bool) error
	edit    func(n *vault.Note) error
}

// commit runs the destination check as the last step before anything is
// written, so a note created at the destination since planning is refused
// before a single backlink changes. RenameFile's no-replace move covers the
// window that remains while the backlinks are rewritten.
func (r relocation) commit(j *Journal, vaultRoot string, fmCfg *config.FrontmatterConfig, opts RelocationOptions) error {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	relocationTestHooks.run(relocationTestHooks.beforeCheck, r.newPath)
	var log rewriteLog
	if err := r.check(r.target, r.newPath, opts.Force); err != nil {
		return r.compensate(j, vaultRoot, log, opts, err)
	}
	for _, file := range r.files {
		if err := log.rewrite(vaultRoot, file, fmCfg, r.edit); err != nil {
			return r.compensate(j, vaultRoot, log, opts, fmt.Errorf("%s: rewrite %s: %w", r.op, file, err))
		}
		if _, err := fmt.Fprintf(out, "%s: updated\n", relOrAbs(vaultRoot, file)); err != nil {
			return r.compensate(j, vaultRoot, log, opts, err)
		}
	}
	relocationTestHooks.run(relocationTestHooks.beforeMove, r.newPath)
	moved, err := renameFile(vaultRoot, r.target, r.newPath, RenameFileOptions{Force: opts.Force, Op: r.op})
	if err != nil && !moved {
		return r.compensate(j, vaultRoot, log, opts, err)
	}
	return err
}

// relocationTestHooks lets a test act inside the two windows a relocation has to
// survive: after the captures but before the final destination check, and after
// the rewrites but before the move. Both are nil outside tests.
var relocationTestHooks relocationHooks

type relocationHooks struct {
	beforeCheck func(newPath string)
	beforeMove  func(newPath string)
}

func (relocationHooks) run(hook func(string), newPath string) {
	if hook != nil {
		hook(newPath)
	}
}

// compensate puts back every note log recorded and returns cause. Nothing the
// relocation captured is changed afterwards, so its captures are dropped: an
// entry it owns is discarded rather than left as an unfinalized directory
// nothing can list, and a batch entry forgets only this item's captures.
func (r relocation) compensate(j *Journal, vaultRoot string, log rewriteLog, opts RelocationOptions, cause error) error {
	if err := log.restore(j, vaultRoot); err != nil {
		return WrapError(CodeInternal, fmt.Sprintf("%s: rewritten links could not be restored", r.op),
			&RelocationIncompleteError{Op: r.op, Paths: log.paths(vaultRoot), Err: errors.Join(cause, err)})
	}
	forget := j.discardUnfinalized
	if opts.batch {
		forget = func() error { return j.forgetSince(opts.batchMark) }
	}
	if err := forget(); err != nil {
		return errors.Join(cause, WrapError(CodeJournal, "drop unused journal captures", err))
	}
	return cause
}

// rewriteRecord is one note a relocation rewrote: the bytes it held before and
// the hash this relocation left it with.
type rewriteRecord struct {
	path      string
	before    []byte
	afterHash string
}

type rewriteLog []rewriteRecord

// rewrite loads path, applies edit, and saves it, recording the pre-image when
// the save changed the file. The bytes are read before Load and must hash to
// what Load saw, so the pre-image is exactly the revision the edit was applied
// to, never an older one an editor replaced in between.
func (l *rewriteLog) rewrite(vaultRoot, path string, fmCfg *config.FrontmatterConfig, edit func(n *vault.Note) error) error {
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	n := vault.NewNote(vaultRoot, path)
	if err := n.Load(); err != nil {
		return err
	}
	beforeHash := vault.HashBytes(before)
	if n.SourceHash() != beforeHash {
		return Errorf(CodeConflict, "%s changed while it was being read; retry", relOrAbs(vaultRoot, path))
	}
	if err := edit(n); err != nil {
		return err
	}
	saveErr := n.Save(fmCfg)
	// A save can fail after its bytes landed (an unsynced directory), and those
	// bytes are this relocation's to put back.
	if after := n.SourceHash(); after != beforeHash {
		*l = append(*l, rewriteRecord{path: path, before: before, afterHash: after})
	}
	return saveErr
}

// restore writes every recorded pre-image back in one transaction, each under
// the precondition that the note still holds what the relocation wrote. A note
// edited since is a conflict rather than an overwrite: its newer content wins,
// and the caller reports the relocation incomplete.
func (l rewriteLog) restore(j *Journal, vaultRoot string) error {
	if len(l) == 0 {
		return nil
	}
	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return err
	}
	for _, rec := range l {
		if err := txn.Write(rec.path, rec.before, 0, vault.ExpectHash(rec.afterHash)); err != nil {
			return err
		}
	}
	return txn.Commit(j)
}

func (l rewriteLog) paths(vaultRoot string) []string {
	out := make([]string, 0, len(l))
	for _, rec := range l {
		out = append(out, relOrAbs(vaultRoot, rec.path))
	}
	return out
}

// rewriteNoteLinks applies rewrite to n's body and to its link-bearing
// frontmatter properties, in place.
func rewriteNoteLinks(n *vault.Note, oldName, newName string, fmCfg *config.FrontmatterConfig, rewrite rewriteFunc) error {
	body, err := n.Body()
	if err != nil {
		return err
	}
	if newBody := rewrite(body, oldName, newName); newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return err
		}
	}
	front, err := n.Front()
	if err != nil {
		return err
	}
	rewriteFrontmatterProps(front, oldName, newName, fmCfg, rewrite)
	return nil
}

// discardUnfinalized removes the entry this operation captured into, for an
// operation that compensated every change away and so has nothing to record.
func (j *Journal) discardUnfinalized() error {
	if j == nil || j.recorder == nil {
		return nil
	}
	if err := journal.DiscardUnfinalized(JournalRoot(j.vaultRoot), j.recorder.EntryID()); err != nil {
		return err
	}
	j.recorder = nil
	return nil
}

// captureMark is where the next capture will land, for forgetSince.
func (j *Journal) captureMark() int {
	if j == nil {
		return 0
	}
	return j.recorder.Mark()
}

// forgetSince drops the captures made after mark from a shared entry.
func (j *Journal) forgetSince(mark int) error {
	if j == nil || j.recorder == nil {
		return nil
	}
	return j.recorder.ForgetSince(mark)
}
