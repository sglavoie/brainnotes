package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type removeOptions struct {
	yes bool
}

// NewRemoveCmd returns the `remove` subcommand. Exported for tests.
func NewRemoveCmd() *cobra.Command {
	opts := &removeOptions{}

	cmd := &cobra.Command{
		Use:   "remove [note]",
		Short: "Move a selected note to the recoverable Trash folder",
		Long: `Remove accepts a note title (case-insensitive) or path, or opens an fzf
picker when no note is supplied, then moves the selected note to Trash/ without
rewriting backlinks. The original vault-relative folder is preserved below Trash/.
The operation is journaled, so 'bn undo <id>' restores the note.

Use --yes to skip the confirmation prompt.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func runRemove(cmd *cobra.Command, args []string, opts *removeOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("remove: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	selected, err := removeSelection(v, cfg, args)
	if err != nil {
		return err
	}
	sourceRel, err := selectedNoteRel(v.Root, selected[0])
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !opts.yes {
		confirmed, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), "remove", fmt.Sprintf("Move %s to Trash?", sourceRel))
		if err != nil {
			return err
		}
		if !confirmed {
			return canceled("remove")
		}
	}

	result, err := noteops.TrashNote(v.Root, sourceRel, noteops.WithTrashCommand(commandInvocation(cmd, args)))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "removed: %s -> %s\njournal_id: %s\nrecover with: bn undo %s\n",
		result.Source.RelPath, result.Destination.RelPath, result.Journal.ID, result.Journal.ID)
	return err
}

func removeSelection(v *vault.Vault, cfg *config.Config, args []string) ([]string, error) {
	if len(args) == 0 {
		return pickNotes("remove", v, cfg, false)
	}
	note, err := resolveNoteTarget("remove", v, args[0])
	if err != nil {
		return nil, err
	}
	return []string{note.Path}, nil
}

// selectedNoteRel translates the picker’s absolute note path into the
// vault-relative form required by noteops.TrashNote.
func selectedNoteRel(vaultRoot, selected string) (string, error) {
	rel, err := filepath.Rel(vaultRoot, selected)
	if err != nil {
		return "", fmt.Errorf("remove: resolve selected note: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("remove: selected note is outside the vault")
	}
	return filepath.ToSlash(rel), nil
}
