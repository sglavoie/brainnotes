package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func NewRenameKeyCmd() *cobra.Command {
	var dryRun bool

	ff := new(findFlags)

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

			candidates, err := collectReplaceCandidates(v, cmd, noteArgs, ff.toQueryFlags(v.Root, nil))
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
					preview := noteops.NotePreview{RelPath: displayPath(n.Path, v.Root)}
					preview.Changes = append(preview.Changes,
						noteops.FrontmatterChange(oldK, true, oldVal, false, nil),
						noteops.FrontmatterChange(newK, false, nil, true, oldVal))
					if err := noteops.RenderNotePreview(dry.Out, preview); err != nil {
						return err
					}
				} else {
					if err := n.Save(&cfg.Frontmatter); err != nil {
						return err
					}
				}
				count++
			}

			if dry == nil {
				summary := fmt.Sprintf("renamed %s -> %s in %d file(s)", oldK, newK, count)
				if skipped > 0 {
					summary += fmt.Sprintf(", %d skipped (conflict)", skipped)
				}
				fmt.Fprintln(out, summary)

				// Config-update warnings are only useful when files were
				// actually rewritten; suppress during --dry-run and no-op
				// runs to avoid confusing users.
				if count > 0 {
					if _, defined := cfg.Frontmatter.Definition(oldK); defined {
						fmt.Fprintf(stderr, "WARN: key %q is defined in [frontmatter.properties]; update your brainnotes.toml to use %q\n", oldK, newK)
					}
				}
			}

			return nil
		},
	}

	f := cmd.Flags()
	ff.register(cmd)
	f.BoolVar(&dryRun, "dry-run", false, "preview changes without writing")

	return cmd
}
