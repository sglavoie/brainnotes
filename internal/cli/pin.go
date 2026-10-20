package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/pins"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewPinCmd returns the `pin` subcommand. Exported for tests.
func NewPinCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "pin <note>",
		Short: "Pin a note to the end of the pinned list",
		Long: `Pin appends a note to the pinned list by writing a 'pinned' float rank into
its frontmatter. The rank is one more than the highest existing rank, so the
note lands at the end of the list.

The note is named by title (case-insensitive) or by path. Pinning a note that
is already pinned is a no-op that leaves its rank alone; reordering is done in
the web UI ('bn serve'), not from the CLI.

The rank is written as a float regardless of whether 'brainnotes.toml' declares
a [frontmatter.properties.pinned] block, so pinning works in any vault.

Use --dry-run to preview the change without writing.`,
		Example: `  bn pin "Reading List"
  bn pin Projects/Reading\ List.md
  bn pin --dry-run "Reading List"`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPin(cmd, args, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the pin without writing")
	return cmd
}

// NewUnpinCmd returns the `unpin` subcommand. Exported for tests.
func NewUnpinCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "unpin <note>",
		Short: "Remove a note from the pinned list",
		Long: `Unpin removes the 'pinned' property from a note's frontmatter entirely,
rather than writing 'pinned: false'. That keeps '--has pinned' and
'--missing pinned' in agreement with '--pinned'.

Unpinning a note that is not pinned is a silent no-op.

Use --dry-run to preview the removal without writing.`,
		Example: `  bn unpin "Reading List"
  bn unpin --dry-run "Reading List"`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnpin(cmd, args, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the removal without writing")
	return cmd
}

func runPin(cmd *cobra.Command, args []string, dryRun bool) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("pin: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	note, err := resolveNoteTarget("pin", v, args[0])
	if err != nil {
		return err
	}
	kind, err := notePinKind(note)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	rel := displayPath(note.Path, v.Root)
	if kind != pins.NotPinned {
		return reportAlreadyPinned(out, rel, dryRun)
	}

	writes, err := planPinWrites(v, &cfg.Frontmatter, rel)
	if err != nil {
		return err
	}
	return applyOrPreviewPin(cmd, args, v.Root, writes, &cfg.Frontmatter, rel, dryRun)
}

// reportAlreadyPinned is the no-op path for a note that already carries a rank.
// --dry-run prints nothing at all, because nothing would change; the pinned
// ordering is edited in the web UI, never by re-pinning.
func reportAlreadyPinned(out io.Writer, rel string, dryRun bool) error {
	if dryRun {
		return nil
	}
	_, err := fmt.Fprintf(out, "already pinned: %s\n", rel)
	return err
}

// planPinWrites plans the append against the current pinned list. Nothing on
// disk changes here, which is what keeps --dry-run and the apply path one branch
// apart, working from the identical plan.
func planPinWrites(v *vault.Vault, fm *config.FrontmatterConfig, rel string) ([]noteops.NoteFrontmatterWrite, error) {
	notes, err := v.Notes()
	if err != nil {
		return nil, noteops.WrapScanError("pin", v.Root, err)
	}
	plan, err := pins.PlanAppend(pins.List(v.Root, notes), rel)
	if err != nil {
		return nil, fmt.Errorf("pin: %w", err)
	}
	return pinAssignmentWrites(plan.Assignments, fm)
}

// applyOrPreviewPin is the one branch between previewing the plan and writing
// it. The write runs under the command's context, so Ctrl-C cancels it.
func applyOrPreviewPin(cmd *cobra.Command, args []string, root string, writes []noteops.NoteFrontmatterWrite, fm *config.FrontmatterConfig, rel string, dryRun bool) error {
	out := cmd.OutOrStdout()
	if dryRun {
		return renderPinPreviews(out, root, writes, fm)
	}
	if err := applyPinWrites(commandContext(cmd), root, writes, fm, commandInvocation(cmd, args)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "pinned: %s\n", rel)
	return err
}

func runUnpin(cmd *cobra.Command, args []string, dryRun bool) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("unpin: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	note, err := resolveNoteTarget("unpin", v, args[0])
	if err != nil {
		return err
	}
	rel := displayPath(note.Path, v.Root)
	out := cmd.OutOrStdout()

	if dryRun {
		preview, err := pinKeyPreview(v.Root, note.Path, func(front *frontmatter.Map) error {
			front.Unset(pins.PropertyName)
			return nil
		})
		if err != nil {
			return err
		}
		return noteops.RenderNotePreview(out, preview)
	}

	result, err := noteops.UnsetFrontmatter(v.Root, rel, []string{pins.PropertyName}, &cfg.Frontmatter,
		noteops.WithFrontmatterCommand(commandInvocation(cmd, args)),
		noteops.WithFrontmatterContext(commandContext(cmd)))
	if err != nil {
		return err
	}
	if !result.Changed {
		_, err := fmt.Fprintf(out, "not pinned: %s\n", rel)
		return err
	}
	_, err = fmt.Fprintf(out, "unpinned: %s\n", rel)
	return err
}

// notePinKind classifies the note's own `pinned` value, so an already-pinned
// note is recognized without matching paths against the pinned list.
func notePinKind(note *vault.Note) (pins.Kind, error) {
	if err := note.Load(); err != nil {
		return pins.NotPinned, err
	}
	front, err := note.Front()
	if err != nil {
		return pins.NotPinned, err
	}
	raw, present := front.Get(pins.PropertyName)
	_, kind := pins.Classify(raw, present)
	return kind, nil
}

// pinAssignmentWrites turns a plan into per-note frontmatter writes. The rank is
// formatted and typed explicitly as a float so the write does not depend on a
// [frontmatter.properties.pinned] declaration existing.
func pinAssignmentWrites(assignments []pins.Assignment, fm *config.FrontmatterConfig) ([]noteops.NoteFrontmatterWrite, error) {
	writes := make([]noteops.NoteFrontmatterWrite, 0, len(assignments))
	for _, a := range assignments {
		mutation, err := noteops.NewFrontmatterMutation(pins.PropertyName, "=",
			strconv.FormatFloat(a.Rank, 'f', -1, 64), "float", fm)
		if err != nil {
			return nil, err
		}
		writes = append(writes, noteops.NoteFrontmatterWrite{
			RelPath:   a.RelPath,
			Mutations: []noteops.FrontmatterMutation{mutation},
		})
	}
	return writes, nil
}

// applyPinWrites journals and commits the plan. A plan that renormalized the
// whole list goes through one transaction so the ordering cannot half-apply.
func applyPinWrites(ctx context.Context, root string, writes []noteops.NoteFrontmatterWrite, fm *config.FrontmatterConfig, command string) error {
	if len(writes) == 0 {
		return nil
	}
	opts := []noteops.FrontmatterOption{noteops.WithFrontmatterCommand(command), noteops.WithFrontmatterContext(ctx)}
	if len(writes) == 1 {
		_, err := noteops.SetFrontmatter(root, writes[0].RelPath, writes[0].Mutations, fm, opts...)
		return err
	}
	_, err := noteops.SetFrontmatterAtomic(root, writes, fm, opts...)
	return err
}

func renderPinPreviews(out io.Writer, root string, writes []noteops.NoteFrontmatterWrite, fm *config.FrontmatterConfig) error {
	for _, w := range writes {
		abs := filepath.Join(root, filepath.FromSlash(w.RelPath))
		preview, err := pinKeyPreview(root, abs, func(front *frontmatter.Map) error {
			_, err := noteops.ApplyFrontmatterMutationsToMap(front, w.Mutations, fm)
			return err
		})
		if err != nil {
			return err
		}
		if err := noteops.RenderNotePreview(out, preview); err != nil {
			return err
		}
	}
	return nil
}

// pinKeyPreview renders what mutate would do to one note's `pinned` key without
// writing anything.
func pinKeyPreview(root, absPath string, mutate func(*frontmatter.Map) error) (noteops.NotePreview, error) {
	note := &vault.Note{Path: absPath}
	if err := note.Load(); err != nil {
		return noteops.NotePreview{}, err
	}
	front, err := note.Front()
	if err != nil {
		return noteops.NotePreview{}, err
	}
	before, beforeOK := front.Get(pins.PropertyName)
	if err := mutate(front); err != nil {
		return noteops.NotePreview{}, err
	}
	after, afterOK := front.Get(pins.PropertyName)

	preview := noteops.NotePreview{RelPath: displayPath(absPath, root)}
	if beforeOK != afterOK || !dryRunValuesEqual(before, after) {
		preview.Changes = append(preview.Changes,
			noteops.FrontmatterChange(pins.PropertyName, beforeOK, before, afterOK, after))
	}
	return preview, nil
}
