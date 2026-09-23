package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
		Use:   "remove",
		Short: "Move a selected note to the recoverable Trash folder",
		Long: `Remove opens an fzf picker, then moves the selected note to Trash/ without
rewriting backlinks. The original vault-relative folder is preserved below Trash/.
The operation is journaled, so 'bn undo <id>' restores the note.

Use --yes to skip the confirmation prompt.`,
		Args:          cobra.NoArgs,
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

	selected, err := pickNotes(v, cfg, false)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}

	sourceRel, err := selectedNoteRel(v.Root, selected[0])
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !opts.yes {
		confirmed, err := confirmRemove(cmd.InOrStdin(), out, sourceRel)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err := fmt.Fprintln(out, "canceled")
			return err
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

// confirmRemove accepts only a complete y/yes response. EOF is a cancellation:
// it prevents a non-interactive invocation from accidentally removing a note.
func confirmRemove(in io.Reader, out io.Writer, note string) (bool, error) {
	if _, err := fmt.Fprintf(out, "Move %s to Trash? [y/N] ", note); err != nil {
		return false, err
	}
	if in == nil {
		return false, nil
	}
	response, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("remove: read confirmation: %w", err)
	}
	response = strings.TrimSpace(response)
	return strings.EqualFold(response, "y") || strings.EqualFold(response, "yes"), nil
}
