package vaultgit

import (
	"errors"
	"fmt"
	"strings"
)

// SnapshotError reports a mutation that completed on disk and whose safety
// snapshot did not.
//
// It exists because the honest answer to that situation is neither "success" nor
// an ordinary failure. The change was written; nothing was rolled back and
// nothing can be, because the content the caller asked for is the content on
// disk. What changed is that the vault's content history no longer covers it,
// and a local failure has latched blocked state so the next mutation is refused
// until a person resolves it. Reporting this as a plain error that reads like
// "the rename failed" would send a user to re-run a command that already ran.
//
// It lives here rather than in a caller because it is the engine's half of the
// contract: every layer that takes a snapshot — note mutations, durable state,
// restores — owes the user the same sentence when one does not land.
type SnapshotError struct {
	// Operation is the sanitized operation token, for example "rename".
	Operation string
	// EntryID is the journal entry whose snapshot failed, when there was one.
	EntryID string
	// Blocked reports whether the failure latched persistent blocked state.
	Blocked bool
	// Err is the engine's classified failure.
	Err error
}

func (e *SnapshotError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "the change"
	}
	fmt.Fprintf(&b, "%s completed and is on disk, but its Git safety snapshot did not: %s", operation, snapshotErrorText(e.Err))
	if e.EntryID != "" {
		fmt.Fprintf(&b, " (journal entry %s)", e.EntryID)
	}
	b.WriteString(". Nothing was rolled back")
	if e.Blocked {
		b.WriteString(", and further BrainNotes mutations are refused until this is resolved")
	}
	b.WriteString(".")
	return b.String()
}

func (e *SnapshotError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func snapshotErrorText(err error) string {
	if err == nil {
		return "the reason was not reported"
	}
	return err.Error()
}

// IsSnapshotFailure reports whether err means the mutation landed but its
// snapshot did not. A caller must never present it as "nothing happened".
func IsSnapshotFailure(err error) bool {
	var snapshot *SnapshotError
	return errors.As(err, &snapshot)
}
