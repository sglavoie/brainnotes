package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// nowForBackup lets tests pin backup archive names.
var nowForBackup = time.Now

// NewBackupCmd returns the `backup` subcommand. Exported for tests.
func NewBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a vault snapshot archive",
		Long: `Backup creates a timestamped tar.gz snapshot at
<vault>/backups/backup-<UTC timestamp>.tar.gz.

The archive excludes the vault's existing backups/ directory and .git/.
Success output prints the archive path plus file and byte counts. If symlinks
are encountered, they are skipped and reported instead of aborting the
snapshot.

This is a vault-level safety snapshot, separate from the undo journal used by
'bn history' and 'bn undo'.`,
		Args:          noArgs("backup"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("backup: no config in context")
			}

			result, err := backup.Create(cfg.Vault, backup.CreateOptions{
				Now:     func() time.Time { return nowForBackup() },
				Context: commandContext(cmd),
			})
			if err != nil {
				return err
			}

			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"backup: created %s (files=%d bytes=%d",
				result.ArchivePath,
				result.Files,
				result.Bytes,
			); err != nil {
				return err
			}
			if result.SkippedSymlinks > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), " skipped_symlinks=%d", result.SkippedSymlinks); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), ")")
			return err
		},
	}

	return cmd
}

// NewRestoreCmd returns the `restore` subcommand. Exported for tests.
func NewRestoreCmd() *cobra.Command {
	var opts restoreOptions

	cmd := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore a vault snapshot archive",
		Long: `Restore extracts one backup archive into the vault.

By default, restore runs in overlay mode: files from the archive overwrite
existing files at the same paths, unrelated files stay in place, and the
command fails before writing if the archive is corrupt/unsafe or if an entry
hits a destination type conflict (for example, a file where a directory is
required).

With --wipe, restore deletes everything in the vault, then applies the archive.
The two directories no archive ever contains are kept, because nothing could
put them back: the root backups/ directory holding your other archives, and
.git/ holding the vault's content history, including every version the wipe
discards. Any other non-archived extra is removed. Because a wipe cannot be
undone with 'bn undo', it asks for confirmation first; --yes skips the prompt
and is required when stdin is not a terminal.

--dry-run validates the archive and lists what the restore would create,
overwrite, and (with --wipe) remove, without changing the vault.

Relative archive paths are resolved from the vault root; absolute paths are
also accepted. This command is separate from the undo journal, so inspect with
'git diff' after restoring if you need to verify or recover changes.`,
		Example: `  bn restore backups/backup-20260528T232425.123456789Z.tar.gz
  bn restore /absolute/path/to/backup.tar.gz
  bn restore backups/backup-20260528T232425.123456789Z.tar.gz --wipe --dry-run
  bn restore backups/backup-20260528T232425.123456789Z.tar.gz --wipe`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(cmd, args[0], opts)
		},
	}

	cmd.Flags().BoolVar(&opts.wipe, "wipe", false, "delete existing vault contents except backups/ and .git/ before restoring")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "list what the restore would create, overwrite, and remove without changing the vault")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the --wipe confirmation prompt (required without a terminal)")
	return cmd
}

type restoreOptions struct {
	wipe   bool
	dryRun bool
	yes    bool
}

func runRestore(cmd *cobra.Command, archive string, opts restoreOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("restore: no config in context")
	}
	restoreOpts := backup.RestoreOptions{Wipe: opts.wipe}
	if opts.dryRun || (opts.wipe && !opts.yes) {
		preview, err := backup.PreviewRestore(cfg.Vault, archive, restoreOpts)
		if err != nil {
			return err
		}
		if opts.dryRun {
			return writeRestorePreview(cmd.OutOrStdout(), preview)
		}
		if err := confirmWipe(cmd, preview); err != nil {
			return err
		}
	}

	result, err := noteops.RestoreBackupContext(commandContext(cmd), cfg.Vault, archive, restoreOpts)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"restore: restored %s (mode=%s files=%d)\n",
		result.ArchivePath,
		result.Mode,
		result.Files,
	)
	return err
}

// confirmWipe asks before a wipe deletes what the archive does not hold. A
// script cannot answer a prompt, so without a terminal the refusal says which
// flag to pass instead of reading an EOF as "no" (confirmTerminal).
func confirmWipe(cmd *cobra.Command, preview *backup.RestorePreview) error {
	question := fmt.Sprintf("Delete %d path(s) not in the archive, then restore %d file(s) from %s?",
		len(preview.Remove), len(preview.Create)+len(preview.Overwrite)+preview.Unchanged, preview.ArchivePath)
	refusal := fmt.Sprintf("--wipe would delete %d path(s) not in the archive; pass --yes to confirm, or --dry-run to list them",
		len(preview.Remove))
	return confirmTerminal(cmd, "restore", question, refusal)
}

func writeRestorePreview(w io.Writer, p *backup.RestorePreview) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "restore: would restore %s (mode=%s)\n", p.ArchivePath, p.Mode)
	for _, group := range []struct {
		verb  string
		paths []string
	}{{"create", p.Create}, {"overwrite", p.Overwrite}, {"remove", p.Remove}} {
		for _, path := range group.paths {
			fmt.Fprintf(bw, "%-9s  %s\n", group.verb, path)
		}
	}
	fmt.Fprintf(bw, "summary: create=%d overwrite=%d unchanged=%d remove=%d\n",
		len(p.Create), len(p.Overwrite), p.Unchanged, len(p.Remove))
	return bw.Flush()
}
