package vaultgit

import (
	"errors"
	"strings"
)

// latch persists the block marker for a failure that warrants one and returns
// the error to the caller.
//
// The marker is a reporting convenience, not the safety mechanism. If this
// write fails, the condition that produced it is still on disk — the deleted
// file is still missing, the ref still will not advance — and the next
// preflight re-derives the same verdict from the same evidence. That is why a
// marker write failure is joined onto the error rather than replacing it: the
// caller still learns the real problem first.
func (m *Manager) latch(err error) error {
	var safety *SafetyError
	if !errors.As(err, &safety) || !safety.Kind.blocking() {
		return err
	}
	record := BlockRecord{
		Kind:       safety.Kind,
		Reason:     safety.Reason,
		Operation:  safety.Op,
		ObservedAt: m.now(),
		Paths:      safety.Paths,
		OldOID:     safety.OldOID,
		NewOID:     safety.NewOID,
		Recovery:   recoveryFor(safety.Kind, safety.Paths),
	}
	if saveErr := m.state.SaveBlock(record); saveErr != nil {
		safety.Err = errors.Join(safety.Err, saveErr)
		// The marker did not land, so if a recovery intent is outstanding it is now
		// the only durable statement that a snapshot was attempted and never proved
		// to have finished. It stays, and every later Open and Preflight refuses on
		// it until recovery proves the snapshot landed or a person clears it.
		return err
	}
	// The marker says everything the intent said and more, and unlike the intent
	// it is a verdict rather than a question, so the record is retired here. A
	// failure to retire it is harmless: the next recovery pass reads it, cannot
	// prove it landed, and refuses — which is what the marker already does.
	if clearErr := m.clearIntent(); clearErr != nil {
		safety.Err = errors.Join(safety.Err, clearErr)
	}
	return err
}

// withOp stamps an operation name onto a SafetyError raised by a helper that
// does not know which caller it is serving.
func withOp(err error, op string) error {
	var safety *SafetyError
	if errors.As(err, &safety) && safety.Op == "" {
		safety.Op = op
	}
	return err
}

// recoveryFor builds the manual procedure written into the block marker. Every
// step names a path or an object identifier and reads no note content, so the
// marker stays safe to share.
func recoveryFor(kind Kind, paths []string) []string {
	switch kind {
	case KindUnexplainedDeletion:
		return deletionRecovery(paths)
	case KindAmbiguousChange:
		steps := []string{
			"Nothing was committed and nothing was pushed.",
			"Run `git -C <vault> status` to see the change BrainNotes would not classify; it is usually a file replaced by a directory or a symlink.",
			"Resolve it by hand so the path is an ordinary file, an ordinary directory, or absent.",
			"Delete the block marker to allow BrainNotes mutations again.",
		}
		if len(paths) > 0 {
			steps = append(steps, "Paths involved: "+strings.Join(paths, ", "))
		}
		return steps
	case KindRefConflict:
		return []string{
			"Nothing was committed and nothing was pushed; the branch is wherever the other writer left it.",
			"Check for another `bn` process or watcher writing to this vault: `git -C <vault> log --oneline -10`.",
			"Once only one writer remains, delete the block marker; the next mutation snapshots the current working tree.",
		}
	case KindCommitFailed:
		return []string{
			"Nothing was committed and nothing was pushed; the working tree still holds every change.",
			"Check free disk space and the permissions on the vault's .git directory.",
			"Recover any object named in this record with `git -C <vault> cat-file blob <oid>`.",
			"Delete the block marker once the cause is fixed; the next mutation retries the snapshot.",
		}
	default:
		return nil
	}
}
