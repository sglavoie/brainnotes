package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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

	noteArgs, parsedPairs, err := parseSetArgs(args, opts, cfg)
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
	var journaler *noteops.Journal
	if dry == nil {
		journaler, err = noteops.BeginOperationContext(commandContext(cmd), v.Root, "set", commandInvocation(cmd, args), paths)
		if err != nil {
			return err
		}
	}
	defer journaler.Release()

	if err := applySetPaths(paths, parsedPairs, cfg, v.Root, dry, journaler, out); err != nil {
		return err
	}
	if _, err := journaler.Finalize(); err != nil {
		return err
	}
	return nil
}

// parseSetArgs splits the positional arguments into notes and key=value pairs
// and parses the pairs. The refusal order is the reported order: the --null
// input-shape refusal precedes the empty-pairs refusal, which precedes any
// per-pair parse error.
func parseSetArgs(args []string, opts setOptions, cfg *config.Config) ([]string, []setPair, error) {
	noteArgs, pairs, err := splitSetArgs(args)
	if err != nil {
		return nil, nil, err
	}
	if opts.nullSep && !usesStdinNoteInput(noteArgs) {
		return nil, nil, errors.New("set: --null requires '-' stdin note input")
	}
	if len(pairs) == 0 {
		return nil, nil, errors.New("set: at least one key=value pair is required")
	}
	parsedPairs, err := parsePairs(pairs, opts.typeFlag, cfg)
	if err != nil {
		return nil, nil, err
	}
	return noteArgs, parsedPairs, nil
}

// applySetPaths applies the pairs to each path in order, reporting only the
// notes that actually changed and only outside --dry-run (where applySet has
// already rendered the preview).
func applySetPaths(paths []string, pairs []setPair, cfg *config.Config, vaultRoot string, dry *DryRun, journaler *noteops.Journal, out io.Writer) error {
	for _, path := range paths {
		changed, err := applySet(path, pairs, &cfg.Frontmatter, vaultRoot, dry, journaler)
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
