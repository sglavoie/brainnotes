package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type setOptions struct {
	typeFlag string
	nullSep  bool
	dryRun   bool
}

func runSetCommand(cmd *cobra.Command, args []string, opts setOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("set: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	noteArgs, pairs, err := splitSetArgs(args)
	if err != nil {
		return err
	}
	if opts.nullSep && !usesStdinNoteInput(noteArgs) {
		return errors.New("set: --null requires '-' stdin note input")
	}
	if len(pairs) == 0 {
		return errors.New("set: at least one key=value pair is required")
	}

	parsedPairs, err := parsePairs(pairs, opts.typeFlag, cfg)
	if err != nil {
		return err
	}

	paths, err := resolveSetPaths(cmd, v, cfg, noteArgs, opts.nullSep)
	if err != nil {
		return err
	}
	sort.Strings(paths)

	out := cmd.OutOrStdout()
	var dry *DryRun
	if opts.dryRun {
		dry = &DryRun{Out: out}
	}

	for _, path := range paths {
		changed, err := applySet(path, parsedPairs, &cfg.Frontmatter, v.Root, dry)
		if err != nil {
			return err
		}
		if changed && dry == nil {
			if _, err := fmt.Fprintf(out, "%s: updated\n", path); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveSetPaths(cmd *cobra.Command, v *vault.Vault, cfg *config.Config, noteArgs []string, nullSep bool) ([]string, error) {
	if len(noteArgs) == 0 {
		selected, err := pickNotes(v, cfg, true)
		if err != nil {
			return nil, err
		}
		if len(selected) == 0 {
			return nil, nil
		}
		return selected, nil
	}
	return resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
}
