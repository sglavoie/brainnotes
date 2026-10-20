package cli

import (
	"github.com/spf13/cobra"
)

// NewReplaceCmd returns the `replace` subcommand. Exported for tests.
func NewReplaceCmd() *cobra.Command {
	opts := replaceOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "replace <key> <from> <to> [note...]",
		Short: "Rewrite a frontmatter value across the vault or a scoped subset",
		Long: `Replace rewrites the value of <key> from <from> to <to> in every matching
note. The scope is selected as follows:

  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): notes matching the filter.
  - Positional notes: only those notes (mutually exclusive with find flags).

For list-valued keys every matching element is replaced in place; scalar
values are replaced when they equal <from>. Wikilink-typed properties have
<from> and <to> auto-wrapped in [[ ]] before comparison. Type coercion of
<from>/<to> follows the same rules as 'bn set' (bool, int, date, string).

Atomicity: each file is rewritten via a temp-file + rename, and the run is one
undo journal entry: reverse a completed run with 'bn undo' (entry IDs:
'bn history'). There is no transaction across files: if a mid-apply error
occurs, prior writes stand and the unfinished entry cannot be undone. Use
'git diff' to inspect or recover.

Use --dry-run to preview changes without writing. Each affected file is
printed with a --- path header and frontmatter[key]: old -> new lines;
unchanged files are omitted.`,
		Args:          cobra.MinimumNArgs(3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReplaceCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	opts.ff.register(cmd)
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")

	return cmd
}
