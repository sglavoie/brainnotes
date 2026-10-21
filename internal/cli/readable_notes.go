package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// loadReadableNotes is the note load for read-only commands (find, search,
// links, backlinks, values, graph, related, context). One note with malformed
// frontmatter must not take the whole vault offline for reading, so it is left
// out and named on stderr, and the command answers from the rest. Mutating
// commands keep the strict v.Notes(): a rewrite planned over a vault with
// silently missing notes could leave links to them stale. `lint` reads the
// skipped list itself, because there an unparsable note is a finding.
func loadReadableNotes(cmd *cobra.Command, v *vault.Vault) ([]*vault.Note, error) {
	return loadReadableNotesTo(cmd.ErrOrStderr(), cmd.Name(), v)
}

func loadReadableNotesTo(errOut io.Writer, command string, v *vault.Vault) ([]*vault.Note, error) {
	notes, skipped, err := v.NotesLenient()
	if err != nil {
		return nil, err
	}
	warnSkippedNotes(errOut, command, v.Root, skipped)
	return notes, nil
}

// warnSkippedNotes prints one line per skipped note. A write failure on stderr
// is ignored: the warning is advisory and must not fail a read.
func warnSkippedNotes(errOut io.Writer, command, root string, skipped []vault.SkippedNote) {
	for _, s := range skipped {
		_, _ = fmt.Fprintf(errOut, "%s: warning: skipped %s: %v\n",
			command, displayPath(s.Path, root), s.Err)
	}
}
