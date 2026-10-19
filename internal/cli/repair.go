package cli

import (
	"github.com/spf13/cobra"
)

// NewRepairCmd returns the `repair` subcommand. Exported for tests.
func NewRepairCmd() *cobra.Command {
	opts := repairOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "repair [note...]",
		Short: "Apply safe mechanical fixes to frontmatter schema violations",
		Long: `Repair inspects frontmatter against the property definitions in
[frontmatter.properties] and applies safe, deterministic fixes. Fixes include:

  - type coercion when the stored value converts losslessly (e.g. "true" -> bool)
  - allowed-value canonicalization (e.g. case-only normalization)
  - wikilink-property wrapping (bare strings -> [[...]])
  - list deduplication (preserving order)

Issues that cannot be fixed mechanically are reported as skipped.

The scope is selected as follows:

  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): notes matching the filter.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten via a temp-file + rename. There is no
transaction across files: if a mid-apply error occurs, prior writes stand.
Use 'git diff' to inspect or recover.

Use --dry-run to preview changes without writing. Each affected file is
printed with a --- path header and frontmatter[key]: old -> new lines;
unchanged files are omitted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepairCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	opts.ff.register(cmd)
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")
	f.StringVar(&opts.property, "property", "", "repair only this schema property")

	return cmd
}
