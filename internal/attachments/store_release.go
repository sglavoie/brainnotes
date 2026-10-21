package attachments

import (
	"fmt"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
)

// Discard drops one of owner's uploads at the client's request.
//
// Cancelling is not best-effort the way post-commit cleanup is: a client that
// asked deserves to hear that it failed, because the alternative is a file it
// thinks it removed sitting in the staging area until the sweep age is reached.
func (s *Store) Discard(owner, id string) error {
	barrier, err := checkpoint.Join(s.root)
	if err != nil {
		return fmt.Errorf("attachments: %w", err)
	}
	defer barrier()
	staged, err := s.Lookup(owner, id)
	if err != nil {
		return err
	}
	return s.remove(staged.ID, staged.RelPath)
}

// Forget drops uploads that have already been committed.
//
// Every failure is swallowed, and that is the difference between this and
// [Store.Discard]: by the time this is called the bytes are in the vault and the
// note is saved, so a staging file that could not be removed is untidiness and
// must never turn a landed save into an error. [Store.Sweep] collects whatever
// this leaves.
func (s *Store) Forget(committed []Staged) {
	// A refused join is swallowed like every other failure here: the note is
	// already saved, and leaving the staging files for Sweep is the correct
	// outcome, not a reason to make the caller wait out a checkpoint.
	barrier, err := checkpoint.Join(s.root)
	if err != nil {
		return
	}
	defer barrier()
	for _, staged := range committed {
		_ = s.remove(staged.ID, staged.RelPath)
	}
}

// drop is Stage's own cleanup for an upload that never completed.
func (s *Store) drop(id, rel string) { _ = s.remove(id, rel) }

// remove takes an upload's three artifacts back, record first.
//
// The order is the reverse of the order they were created in, and it is the
// same reason: the record is what makes the upload visible, so removing it first
// means nothing can start depending on the upload while the rest is going away.
// A failure part-way leaves a blob or a claim with no record, which is precisely
// what [Store.Sweep] collects.
//
//bn:direct-write removes files this package created inside .bnotes/uploads, each named by a validated upload id or by a digest of a destination it claimed; nothing here names a path a caller supplied
func (s *Store) remove(id, rel string) error {
	if err := validID(id); err != nil {
		return err
	}
	var failures []error
	if err := os.Remove(s.recordPath(id)); err != nil && !os.IsNotExist(err) {
		failures = append(failures, err)
	}
	if rel != "" {
		if err := s.releaseClaim(rel, id); err != nil {
			failures = append(failures, err)
		}
	}
	if err := os.Remove(s.blobPath(id)); err != nil && !os.IsNotExist(err) {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("attachments: discard upload %s: %w", id, failures[0])
	}
	return nil
}

// releaseClaim gives up a destination claim, but only the one this upload made.
//
// Reading the id back before removing is what keeps a late cleanup from freeing
// a name some other upload has since taken: the claim file is created O_EXCL and
// only ever removed through here, so a claim holding a different id belongs to
// somebody else and is left alone.
//
// A stale claim that outlives the commit is harmless in the other direction —
// the file it named now exists in the vault, so the next upload of that name
// would be diverted by the destination check anyway.
//
//bn:direct-write removes one claim file under .bnotes/uploads/names, named by a digest this package computed, and only after reading back the upload id that created it
func (s *Store) releaseClaim(rel, id string) error {
	claim := s.namePath(rel)
	held, err := os.ReadFile(claim)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(held)) != id {
		return nil
	}
	if err := os.Remove(claim); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
