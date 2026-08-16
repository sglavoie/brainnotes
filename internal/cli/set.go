package cli

import "github.com/spf13/cobra"

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
integer -> int, YYYY-MM-DD -> date, else string. Use --type to force
date|datetime|int|bool|float|raw. Wikilink-typed properties are auto-wrapped
before mutation.

Each file is rewritten atomically, but there is no cross-file transaction: if
an error stops the run mid-way, prior writes stand. Use 'git diff' to recover.

Use --dry-run to preview changes without writing. Each affected file is
printed as a unified diff block; unchanged files are omitted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.typeFlag, "type", "", "force value type: date|int|bool|raw (default: auto)")
	f.BoolVarP(&opts.nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}
