package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func NewRepairCmd() *cobra.Command {
	var (
		dryRun   bool
		property string
	)

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:   "repair [notes...]",
		Short: "Apply safe mechanical fixes to frontmatter schema violations",
		Long: `Repair inspects frontmatter against the property schema defined in
config.toml and applies safe, deterministic fixes. Fixes include:

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
printed as a unified diff block; unchanged files are omitted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("repair: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			if len(cfg.Frontmatter.Schema) == 0 {
				writeSchemaHint(cmd.ErrOrStderr(), "repair", cfg.SourcePath)
				return nil
			}

			schemaToUse := cfg.Frontmatter.Schema
			if property != "" {
				rule, ok := cfg.Frontmatter.Schema[property]
				if !ok {
					return fmt.Errorf("repair: property %q not in schema", property)
				}
				schemaToUse = map[string]config.PropertySchema{property: rule}
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(args) > 0 && anyFlagSet {
				return errors.New("repair: cannot combine positional notes with find filter flags")
			}

			candidates, err := collectReplaceCandidates(v, cmd, args, ff.toQueryFlags(v.Root, nil))
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}

			repaired := 0
			totalSkips := 0
			hasMissingRequired := false
			for _, n := range candidates {
				front, err := n.Front()
				if err != nil {
					return err
				}

				result := schema.PlanRepairs(front, schemaToUse, cfg.Frontmatter.WikilinkProperties)
				if property != "" {
					result = filterRepairResult(result, property)
				}
				if len(result.Actions) == 0 && len(result.Skips) == 0 {
					continue
				}

				for _, s := range result.Skips {
					relPath := displayPath(n.Path, v.Root)
					fmt.Fprintf(stderr, "SKIP: %s: %s: %s (%s)\n", relPath, s.Property, s.Detail, s.Kind)
					totalSkips++
					if s.Kind == "missing_required" {
						hasMissingRequired = true
					}
				}

				if len(result.Actions) == 0 {
					continue
				}

				changed, err := applyRepairs(n, result, &cfg.Frontmatter, v.Root, dry)
				if err != nil {
					_, _ = fmt.Fprintln(stderr, "WARN: repair interrupted; run 'git diff' to inspect partial state")
					return err
				}
				if !changed {
					continue
				}
				repaired++
				if dry == nil {
					fmt.Fprintf(out, "repaired: %s\n", displayPath(n.Path, v.Root))
				}
			}

			if dry == nil {
				summary := fmt.Sprintf("repaired %d file(s)", repaired)
				if totalSkips > 0 {
					summary += fmt.Sprintf(", %d skipped", totalSkips)
				}
				fmt.Fprintln(out, summary)
			}
			if hasMissingRequired {
				fmt.Fprintln(stderr, "\nhint: set missing properties with: bn set <note> <property>=<value>")
			}
			return nil
		},
	}

	f := cmd.Flags()
	ff.register(cmd)
	f.BoolVar(&dryRun, "dry-run", false, "preview changes without writing")
	f.StringVar(&property, "property", "", "repair only this schema property")

	return cmd
}

func filterRepairResult(r schema.RepairResult, prop string) schema.RepairResult {
	var out schema.RepairResult
	for _, a := range r.Actions {
		if a.Property == prop {
			out.Actions = append(out.Actions, a)
		}
	}
	for _, s := range r.Skips {
		if s.Property == prop {
			out.Skips = append(out.Skips, s)
		}
	}
	return out
}
