package cli

import (
	"errors"
	"fmt"
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
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("backup: no config in context")
			}

			result, err := backup.Create(cfg.Vault, backup.CreateOptions{
				Now: func() time.Time { return nowForBackup() },
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
	var wipe bool

	cmd := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore a vault snapshot archive",
		Long: `Restore extracts one backup archive into the vault.

By default, restore runs in overlay mode: files from the archive overwrite
existing files at the same paths, unrelated files stay in place, and the
command fails before writing if the archive is corrupt/unsafe or if an entry
hits a destination type conflict (for example, a file where a directory is
required).

With --wipe, restore deletes everything in the vault except the root backups/
directory and its contents, then applies the archive. That includes .git/ and
any other non-archived extras.

Relative archive paths are resolved from the vault root; absolute paths are
also accepted. This command is separate from the undo journal, so inspect with
'git diff' after restoring if you need to verify or recover changes.`,
		Example: `  bn restore backups/backup-20260528T232425.123456789Z.tar.gz
  bn restore /absolute/path/to/backup.tar.gz
  bn restore backups/backup-20260528T232425.123456789Z.tar.gz --wipe`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("restore: no config in context")
			}

			result, err := noteops.RestoreBackup(cfg.Vault, args[0], backup.RestoreOptions{Wipe: wipe})
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
		},
	}

	cmd.Flags().BoolVar(&wipe, "wipe", false, "delete existing vault contents except backups/ before restoring")
	return cmd
}
