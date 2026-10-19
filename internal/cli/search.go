package cli

import (
	"github.com/spf13/cobra"
)

// searchOpenExec is the editor invocation used by `search --open`.
// Tests swap it to avoid exec-replacing the test process.
var searchOpenExec = func(editor, file string, line int) error {
	return runEditorAtLine(editor, file, line)
}

// NewSearchCmd returns the `search` subcommand. Exported for tests.
func NewSearchCmd() *cobra.Command {
	opts := searchOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "search [pattern] [note...]",
		Short: "Search note bodies for a pattern",
		Long: `Search note bodies for a text pattern and print matching note paths (one per line).
By default the search is case-insensitive and treats the pattern as a literal string.

Output modes:
  paths (default)  One absolute path per matching note, sorted.
  --context N      Each match with N lines of surrounding body context (grep-style).
  --count          Match count per note, sorted by count descending then path.
  --open           Open the first matching result in the configured editor at the matching line.
  --json           A JSON array of {path, abs_path, title, count, matches: [{line, text}]}.

--limit N caps how many notes the paths, --count, --context and --json modes
report, after their own ordering.

Like grep, search exits 1 when nothing matches (and when an interactive picker
selects nothing), and 2 on an error.

Find flags (--tag, --category, --status, --type, --folder, etc.) scope the search to
matching notes. Positional note arguments and find flags are mutually exclusive.

Examples:
  bn search "activation energy" --tag physics
  bn search "TODO" --context 2
  bn search "meeting" --count
  bn search "activation energy" --open
  bn find --tag drafts | bn search "review" -`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	opts.ff.register(cmd)
	f.BoolVarP(&opts.regex, "regex", "e", false, "treat pattern as a regular expression")
	f.BoolVar(&opts.caseSensitive, "case-sensitive", false, "case-sensitive match (default: case-insensitive)")
	f.BoolVarP(&opts.nullSep, "null", "0", false, "separate paths with NUL instead of newline, on stdin (with -) and stdout")
	f.BoolVarP(&opts.interactive, "interactive", "i", false, "pipe matching note paths into fzf")
	f.IntVarP(&opts.contextN, "context", "C", 0, "show N lines of context around each match")
	f.BoolVar(&opts.count, "count", false, "print match count per note, sorted by count desc")
	f.BoolVar(&opts.openResult, "open", false, "open the first matching result in the configured editor at the matching line")
	f.IntVar(&opts.limit, "limit", 0, "report at most N matching notes (paths, --count, --context, --json)")
	f.BoolVar(&opts.asJSON, "json", false, `print a JSON array of {"path", "abs_path", "title", "count", "matches"} records`)

	return cmd
}
