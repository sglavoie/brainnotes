package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

var relinkMediaExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".webp": {},
	".pdf": {}, ".mp4": {}, ".mov": {}, ".mp3": {}, ".wav": {}, ".ogg": {}, ".heic": {},
}

type relinkTarget struct {
	key      string
	relNoExt string
	title    string
}

type relinkIndex struct {
	byPathKey  map[string]relinkTarget
	byTitleKey map[string][]relinkTarget
}

type relinkChange struct {
	line           int
	frontmatterKey string
	oldRaw         string
	newRaw         string
}

type relinkResidue struct {
	relPath        string
	line           int
	frontmatterKey string
	raw            string
}

func NewRelinkCmd() *cobra.Command {
	var dryRun bool

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:   "relink [notes...]",
		Short: "Repair unresolved wikilinks with safe deterministic rewrites",
		Long: `Relink scans unresolved wikilinks and applies only deterministic rewrites.

Safe rewrites in v1:
  - exact path/key match after path normalization (e.g. backslashes -> '/')
  - exact title match (case-insensitive, optional .md) when unique

Ambiguous targets are skipped and reported for manual follow-up.

Scope:
  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): matching notes only.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten via a temp-file + rename. There is no
transaction across files: if a mid-apply error occurs, prior writes stand.
Use 'git diff' to inspect or recover.

Use --dry-run to preview changes without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("relink: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(args) > 0 && anyFlagSet {
				return errors.New("relink: cannot combine positional notes with find filter flags")
			}

			candidates, err := collectReplaceCandidates(cmd, v, cfg, args, ff)
			if err != nil {
				return err
			}
			allNotes, err := v.Notes()
			if err != nil {
				return err
			}

			g := query.BuildGraphForRoot(v.Root, allNotes)
			idx := newRelinkIndex(v.Root, g)

			out := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}
			var journaler *noteops.Journal
			if dry == nil {
				journaler, err = noteops.BeginOperation(v.Root, "relink", commandInvocation(cmd, args), nil)
				if err != nil {
					return err
				}
			}

			relinked := 0
			var residues []relinkResidue
			for _, n := range candidates {
				changed, noteResidues, err := applyRelink(n, idx, g, &cfg.Frontmatter, v.Root, dry, journaler)
				if err != nil {
					_, _ = fmt.Fprintln(stderr, "WARN: relink interrupted; run 'git diff' to inspect partial state")
					return err
				}
				residues = append(residues, noteResidues...)
				if !changed {
					continue
				}
				relinked++
				if dry == nil {
					fmt.Fprintf(out, "relinked: %s\n", displayPath(n.Path, v.Root))
				}
			}
			if _, err := journaler.Finalize(); err != nil {
				return err
			}

			if err := printRelinkResidues(stderr, residues); err != nil {
				return err
			}
			if len(residues) > 0 {
				fmt.Fprintln(stderr, "\nhint: run 'bn find --broken-links --show-broken' for manual follow-up")
			}
			if dry == nil {
				summary := fmt.Sprintf("relinked %d file(s)", relinked)
				if len(residues) > 0 {
					summary += fmt.Sprintf(", %d unresolved skipped", len(residues))
				}
				fmt.Fprintln(out, summary)
			}

			return nil
		},
	}

	ff.register(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}
