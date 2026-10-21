package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewSetCmd returns the `set` subcommand. Exported for tests.
func NewSetCmd() *cobra.Command {
	var opts setOptions

	cmd := &cobra.Command{
		Use:   "set [note...] <key[=|+=|-=]value...>",
		Short: "Set frontmatter properties on one or more notes",
		Long: `Set writes or updates frontmatter properties on one or more notes.

Positional arguments are split into notes and pairs: any arg containing an
operator ('=', '+=', '-=') is treated as a pair, and all preceding args are
note paths. Use '-' as the sole note arg to read paths from stdin (composes
with 'bn find'). When no note args are given, an fzf multi-select picker
launches to choose notes interactively.

Operators:
  key=value    Set the property (overwriting any prior value).
  key+=value   Append to a list property (no-op if value already present).
  key-=value   Remove a value from a list property (no-op if absent).

'+=' and '-=' require the key to be defined with type = "list" in
[frontmatter.properties]. Values are comma-split for list properties exactly
as with '='. Mixing operators in one invocation is allowed and applied
left-to-right.

A configured property is stored as the kind its definition declares. For
properties with no definition, coercion is automatic: true/false -> bool,
integer -> int, YYYY-MM-DD -> date, else string. Use --value-type to force
date|datetime|int|bool|float|raw. Wikilink-typed properties are auto-wrapped
before mutation. (--type is a deprecated spelling of --value-type: elsewhere
--type filters notes by their type property.)

Each file is rewritten atomically and the run is one undo journal entry:
reverse a completed run with 'bn undo' (entry IDs: 'bn history'). There is no
cross-file transaction: if an error stops the run mid-way, prior writes stand
and the unfinished entry cannot be undone. Use 'git diff' to recover.

Use --dry-run to preview changes without writing. Each affected file is
printed with a --- path header and frontmatter[key]: old -> new lines;
unchanged files are omitted.`,
		Example: `  bn set References/Books/Dune.md status=Reading rating=7
  bn set note.md tags+=project --dry-run
  bn set note.md created=2026-01-01 --value-type date
  bn find --tag drafts | bn set - status=done`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("type") {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "set: warning: --type is deprecated; use --value-type")
			}
			return runSetCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.typeFlag, "value-type", "", "force value type: date|datetime|int|bool|float|raw (default: auto)")
	// --type is the old spelling. It still works, hidden, with a note on
	// stderr, because on every find-scoped command --type is a note filter.
	// It is hidden rather than MarkDeprecated, whose note pflag prints in its
	// own fixed wording ("Flag --type has been deprecated, ..."); RunE prints
	// bn's `set: warning:` line instead.
	f.StringVar(&opts.typeFlag, "type", "", "deprecated spelling of --value-type")
	_ = f.MarkHidden("type")
	f.BoolVarP(&opts.nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}
