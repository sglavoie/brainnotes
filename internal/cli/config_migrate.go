package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// configMigrateOperation is the fixed token this mutation snapshots under. Like
// every other operation token it is chosen here and never derived from what the
// migration turned out to move.
const configMigrateOperation = "config-migrate"

func newConfigMigrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move every configuration key into the file that owns it",
		Long: "Move each assignment sitting in the wrong configuration file into the one that owns it: the keys naming " +
			"this machine — editor, listen address, agent command — into " + config.LocalFileName + ", and everything " +
			"describing the vault itself into " + config.FileName + ".\n\n" +
			"Assignments are moved as text, so values, quoting, and the comment on a moved line arrive unchanged, and " +
			"comments above a key stay where they are. What the vault does is identical afterwards: the migration is " +
			"refused rather than written if any key would end up with a different effective value.\n\n" +
			"Running it again does nothing, and the originals are recoverable through `bn undo` — an interrupted run " +
			"leaves either both files migrated or neither.",
		Args: cobra.NoArgs,
		// The vault that most needs this is the one whose configuration the
		// loader refuses: once a vault is enrolled in synchronization, a
		// misplaced key stops it loading at all. Requiring a successful load
		// first would put the only repair behind the fault it repairs.
		Annotations:   map[string]string{skipConfigLoad: "repairs a vault whose configuration does not load"},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigMigrate(cmd, args, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the moves and the resulting diffs without writing either file")
	return cmd
}

func runConfigMigrate(cmd *cobra.Command, args []string, dryRun bool) error {
	const label = "config migrate"
	vaultFlag, err := cmd.Flags().GetString("vault")
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	root, origin, err := config.ResolveVault(config.LoadOptions{VaultFlag: vaultFlag})
	if err != nil {
		return err
	}
	sources, err := readConfigSources(label, root)
	if err != nil {
		return err
	}
	// Returned unwrapped: every refusal the planner produces already names the
	// key or the file it is about, and prefixing it would only spell "config"
	// twice.
	plan, err := config.PlanMigration(root, origin, sources)
	if err != nil {
		return err
	}

	if !plan.Changed() {
		// Said out loud rather than passed over in silence: this command's whole
		// contract is that a second run is safe, and saying nothing reads as
		// having done something.
		_, err := fmt.Fprintf(cmd.OutOrStdout(),
			"Nothing to migrate: every key in %s is already in the file that owns it.\n", root)
		return err
	}
	if dryRun {
		return writeMigrationPreview(cmd.OutOrStdout(), root, plan)
	}
	if err := commitMigration(commandContext(cmd), root, plan, commandInvocation(cmd, args)); err != nil {
		return err
	}
	return writeMigrationSummary(cmd.OutOrStdout(), plan)
}

// migrationFile is one configuration file as a migration found it and as the
// migration would leave it.
type migrationFile struct {
	layer  config.Layer
	path   string
	result config.PatchResult
	// existed records whether the file was on disk when the plan was computed.
	// An overlay this run creates has to be written against its absence.
	existed bool
	source  []byte
}

// expectation is the transaction precondition this file carries: the exact
// bytes the plan was computed from, or its absence when it had none.
func (f migrationFile) expectation() vault.FileExpectation {
	if !f.existed {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(vault.HashBytes(f.source))
}

// migrationFiles pairs each layer of a plan with the file it belongs to, so the
// preview and the transaction walk the two in the same order.
func migrationFiles(root string, plan config.MigrationPlan) []migrationFile {
	return []migrationFile{
		{
			layer:   config.LayerPortable,
			path:    config.LayerPath(root, config.LayerPortable),
			result:  plan.Portable,
			existed: true,
			source:  plan.Sources.Portable,
		},
		{
			layer:   config.LayerLocal,
			path:    config.LayerPath(root, config.LayerLocal),
			result:  plan.Local,
			existed: plan.Sources.LocalPresent(),
			source:  plan.Sources.Local,
		},
	}
}

// commitMigration writes both halves of a migration as one transaction.
//
// Both files are always written, including one a plan leaves byte-identical.
// The usual move takes lines out of one file and puts them into the other, so
// the two writes only mean anything together — half a migration is a vault with
// a key in neither file, or in both. Dropping a shadowed assignment is the one
// case that changes a single file, and it is written the same way rather than
// specially: the destination it did not change is still a file the plan was
// computed from, so pinning it as an unconditional write costs one rename and
// keeps the precondition set the same shape in every case. Either way they move
// under a single transaction and a single journal entry, each pinned to the bytes
// the plan read.
//
// That is what makes an interrupted run survivable: the transaction's fsynced
// intent either rolls forward to the migrated pair or rolls back to the
// originals, and `bn undo` restores the originals afterwards from the manifest
// Finalize wrote.
func commitMigration(ctx context.Context, root string, plan config.MigrationPlan, command string) error {
	const label = "config migrate"
	files := migrationFiles(root, plan)

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	journaler, err := noteops.BeginOperationContext(ctx, root, configMigrateOperation, command, paths)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	defer journaler.Release()
	txn, err := vault.NewFileTxn(root)
	if err != nil {
		return fmt.Errorf("%s: open transaction: %w", label, err)
	}
	for _, file := range files {
		if err := txn.Write(file.path, file.result.Patched, 0, file.expectation()); err != nil {
			return configTxnError(label, file.layer.FileName(), err)
		}
	}
	if beforeConfigCommit != nil {
		beforeConfigCommit()
	}
	if err := txn.Commit(journaler); err != nil {
		// Which of the two files lost the race is the transaction's to say: it
		// reports the conflicting path, and naming one here would be a guess.
		return configTxnError(label, "the configuration", err)
	}
	if _, err := journaler.Finalize(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// writeMigrationPreview prints what a migration would do, in the two registers
// it is judged in: the keys that move, and the bytes of each file that changes.
func writeMigrationPreview(out io.Writer, root string, plan config.MigrationPlan) error {
	if err := writeMigrationSummary(out, plan); err != nil {
		return err
	}
	for _, file := range migrationFiles(root, plan) {
		if !file.result.Changed {
			continue
		}
		if err := writeConfigPatchDiff(out, file.layer.FileName(), file.result.Diff); err != nil {
			return err
		}
	}
	return nil
}

// writeMigrationSummary names every key that moved and where it went.
func writeMigrationSummary(out io.Writer, plan config.MigrationPlan) error {
	for _, move := range plan.Moves {
		if _, err := fmt.Fprintln(out, migrationSummaryLine(move)); err != nil {
			return err
		}
	}
	return nil
}

// migrationSummaryLine describes one move in one line.
//
// A key both files assigned is spelled out rather than reported as a plain move,
// because an assignment disappears in that case and the person reading this is
// the only one who can tell whether the value that was never in force mattered
// to them.
func migrationSummaryLine(move config.MigrationMove) string {
	switch {
	case !move.Shadowed:
		return fmt.Sprintf("%s: %s -> %s", move.Key, move.From.FileName(), move.To.FileName())
	case move.To == config.LayerLocal:
		return fmt.Sprintf("%s: dropped from %s; %s already assigned it and that is the value in force",
			move.Key, move.From.FileName(), move.To.FileName())
	default:
		return fmt.Sprintf("%s: %s -> %s, replacing the assignment it was already shadowing",
			move.Key, move.From.FileName(), move.To.FileName())
	}
}
