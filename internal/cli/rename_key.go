package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func NewRenameKeyCmd() *cobra.Command {
	var (
		tags        []string
		categories  []string
		status      string
		typeProp    string
		hasProp     string
		missingProp string
		age         string
		created     string
		folder      string
		orphan      bool
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "rename-key <old-key> <new-key> [notes...]",
		Short: "Rename a frontmatter key across the vault or a scoped subset",
		Long: `Rename-key changes a frontmatter key from <old-key> to <new-key> in every
matching note. The value is preserved exactly (no type coercion, no wikilink
wrapping). Notes where <old-key> is absent are skipped silently. Notes where
<new-key> already exists are skipped with a warning.

The scope is selected as follows:

  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): notes matching the filter.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten via a temp-file + rename. There is no
transaction across files: if a mid-apply error occurs, prior writes stand.
Use 'git diff' to inspect or recover.

Use --dry-run to preview changes without writing.`,
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("rename-key: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			oldK, newK := args[0], args[1]
			noteArgs := args[2:]

			if !pairKeyRe.MatchString(oldK) {
				return fmt.Errorf("rename-key: invalid key %q (allowed: letters, digits, '_', '-')", oldK)
			}
			if !pairKeyRe.MatchString(newK) {
				return fmt.Errorf("rename-key: invalid key %q (allowed: letters, digits, '_', '-')", newK)
			}
			if oldK == newK {
				return errors.New("rename-key: old and new key are the same")
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(noteArgs) > 0 && anyFlagSet {
				return errors.New("rename-key: cannot combine positional notes with find filter flags")
			}

			candidates, err := collectReplaceCandidates(v, cmd, noteArgs, query.Flags{
				Tags:         tags,
				Categories:   categories,
				Status:       status,
				Type:         typeProp,
				Has:          hasProp,
				Missing:      missingProp,
				Age:          age,
				Created:      created,
				Folder:       folder,
				Orphan:       orphan,
				MinBacklinks: -1,
				MaxBacklinks: -1,
				MinLinks:     -1,
				MaxLinks:     -1,
				Root:         v.Root,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}

			count := 0
			skipped := 0
			for _, n := range candidates {
				front, err := n.Front()
				if err != nil {
					return err
				}
				if !front.Has(oldK) {
					continue
				}

				oldVal, _ := front.Get(oldK)
				changed, err := front.RenameKey(oldK, newK)
				if err != nil {
					if errors.Is(err, frontmatter.ErrKeyConflict) {
						fmt.Fprintf(stderr, "WARN: %s: skipped, key %q already exists\n",
							displayPath(n.Path, v.Root), newK)
						skipped++
						continue
					}
					return err
				}
				if !changed {
					continue
				}

				if dry != nil {
					block := newFileBlock(displayPath(n.Path, v.Root))
					block.removed(oldK, oldVal)
					block.added(newK, oldVal)
					if err := block.flush(dry.Out); err != nil {
						return err
					}
				} else {
					if err := n.Save(&cfg.Frontmatter); err != nil {
						return err
					}
				}
				count++
			}

			summary := fmt.Sprintf("renamed %s -> %s in %d file(s)", oldK, newK, count)
			if skipped > 0 {
				summary += fmt.Sprintf(", %d skipped (conflict)", skipped)
			}
			fmt.Fprintln(out, summary)

			if containsString(cfg.Frontmatter.ListProperties, oldK) {
				fmt.Fprintf(stderr, "WARN: key %q is in [frontmatter].list_properties; update your config.toml to use %q\n", oldK, newK)
			}
			if containsString(cfg.Frontmatter.WikilinkProperties, oldK) {
				fmt.Fprintf(stderr, "WARN: key %q is in [frontmatter].wikilink_properties; update your config.toml to use %q\n", oldK, newK)
			}
			if _, inSchema := cfg.Frontmatter.Schema[oldK]; inSchema {
				fmt.Fprintf(stderr, "WARN: key %q is in [frontmatter.schema]; update your config.toml to use %q\n", oldK, newK)
			}

			return nil
		},
	}

	f := cmd.Flags()
	f.StringSliceVar(&tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	f.StringVar(&status, "status", "", "filter by status property")
	f.StringVar(&typeProp, "type", "", "filter by type/kind property")
	f.StringVar(&hasProp, "has", "", "require property present")
	f.StringVar(&missingProp, "missing", "", "require property absent")
	f.StringVar(&age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&orphan, "orphan", false, "only notes nothing in the vault links to")
	f.BoolVar(&dryRun, "dry-run", false, "preview changes without writing")

	return cmd
}
