package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// queryFlagNames lists the `find`-style flags that scope `bn replace`. Used to
// detect whether any scoping flag was set so the command can reject the
// note-args / find-flags combination.
var queryFlagNames = []string{
	"tag", "category", "status", "type", "has", "missing",
	"age", "created", "folder", "orphan",
}

// NewReplaceCmd returns the `replace` subcommand. Exported for tests.
func NewReplaceCmd() *cobra.Command {
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
		Use:   "replace <key> <from> <to> [notes...]",
		Short: "Rewrite a frontmatter value across the vault or a scoped subset",
		Long: `Replace rewrites the value of <key> from <from> to <to> in every matching
note. The scope is selected as follows:

  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): notes matching the filter.
  - Positional notes: only those notes (mutually exclusive with find flags).

For list-valued keys every matching element is replaced in place; scalar
values are replaced when they equal <from>. Wikilink-typed properties have
<from> and <to> auto-wrapped in [[ ]] before comparison. Type coercion of
<from>/<to> follows the same rules as 'bn set' (bool, int, date, string).

Atomicity: each file is rewritten via a temp-file + rename. There is no
transaction across files: if a mid-apply error occurs, prior writes stand.
Use 'git diff' to inspect or recover.

Use --dry-run to preview changes without writing. Each affected file is
printed as a unified diff block; unchanged files are omitted.`,
		Args:          cobra.MinimumNArgs(3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("replace: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			key, fromRaw, toRaw := args[0], args[1], args[2]
			noteArgs := args[3:]

			if !pairKeyRe.MatchString(key) {
				return fmt.Errorf("replace: invalid key %q (allowed: letters, digits, '_', '-')", key)
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(noteArgs) > 0 && anyFlagSet {
				return errors.New("replace: cannot combine positional notes with find filter flags")
			}

			isWikilink := containsString(cfg.Frontmatter.WikilinkProperties, key)
			fromVal, err := coerceReplaceValue(fromRaw, isWikilink)
			if err != nil {
				return fmt.Errorf("replace: invalid <from>: %w", err)
			}
			toVal, err := coerceReplaceValue(toRaw, isWikilink)
			if err != nil {
				return fmt.Errorf("replace: invalid <to>: %w", err)
			}

			candidates, err := collectReplaceCandidates(v, cmd, noteArgs, query.Flags{
				Tags:       tags,
				Categories: categories,
				Status:     status,
				Type:       typeProp,
				Has:        hasProp,
				Missing:    missingProp,
				Age:        age,
				Created:    created,
				Folder:     folder,
				Orphan:     orphan,
				Root:       v.Root,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}

			count := 0
			for _, n := range candidates {
				changed, err := applyReplace(n, key, fromVal, toVal, &cfg.Frontmatter, v.Root, dry)
				if err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: replace interrupted; run 'git diff' to inspect partial state")
					return err
				}
				if !changed {
					continue
				}
				count++
				if dry == nil {
					if _, err := fmt.Fprintf(out, "updated: %s\n", n.Path); err != nil {
						return err
					}
				}
			}
			if dry == nil {
				if _, err := fmt.Fprintf(out, "replaced %d file(s)\n", count); err != nil {
					return err
				}
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

// coerceReplaceValue runs raw through coerceValue (auto-detect) and then wraps
// the result in [[...]] when the target key is wikilink-typed. Auto-coercion
// matches 'bn set' so user input like "true" or "2026-01-02" round-trips.
func coerceReplaceValue(raw string, isWikilink bool) (any, error) {
	v, err := coerceValue(raw, "")
	if err != nil {
		return nil, err
	}
	if isWikilink {
		if s, ok := v.(string); ok {
			return wrapWikilinkValue(s), nil
		}
	}
	return v, nil
}

// collectReplaceCandidates returns the notes the replace should iterate over.
// When noteArgs is non-empty the explicit paths are loaded individually;
// otherwise the full vault is walked and filtered via FromFlags.
func collectReplaceCandidates(v *vault.Vault, cmd *cobra.Command, noteArgs []string, flags query.Flags) ([]*vault.Note, error) {
	if len(noteArgs) > 0 {
		paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), false)
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		out := make([]*vault.Note, 0, len(paths))
		for _, p := range paths {
			n := &vault.Note{Path: p}
			if err := n.Load(); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}

	notes, err := v.Notes()
	if err != nil {
		return nil, err
	}
	if flags.Orphan {
		flags.BacklinkIndex = query.BuildBacklinkIndex(notes)
	}
	pred, err := query.FromFlags(flags)
	if err != nil {
		return nil, err
	}
	out := make([]*vault.Note, 0, len(notes))
	for _, n := range notes {
		if pred(n) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// applyReplace performs the swap on a single note. On dry-run it emits a diff
// block and skips the write; otherwise the file is rewritten atomically.
func applyReplace(n *vault.Note, key string, fromVal, toVal any, fm *config.FrontmatterConfig, vaultRoot string, dry *DryRun) (bool, error) {
	front, err := n.Front()
	if err != nil {
		return false, err
	}
	beforeVal, beforePresent := front.Get(key)
	changed, err := front.ReplaceValue(key, fromVal, toVal)
	if err != nil {
		if errors.Is(err, frontmatter.ErrNotAList) {
			return false, fmt.Errorf("replace: %q: %w", key, err)
		}
		return false, err
	}
	if !changed {
		return false, nil
	}
	if dry != nil {
		block := newFileBlock(displayPath(n.Path, vaultRoot))
		if beforePresent {
			block.removed(key, beforeVal)
		}
		afterVal, afterOK := front.Get(key)
		if afterOK {
			block.added(key, afterVal)
		}
		if err := block.flush(dry.Out); err != nil {
			return false, err
		}
		return !block.empty(), nil
	}
	if err := n.Save(fm); err != nil {
		return false, err
	}
	return true, nil
}
