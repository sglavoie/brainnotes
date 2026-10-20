package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"
)

// checkpointOptions is the flag surface of `bn checkpoint`, shared by both
// modes so the two subcommands cannot drift on what a flag means.
type checkpointOptions struct {
	// asJSON switches `status` to the machine-readable form. It is what a
	// deployment script reads, so it is a stable shape rather than the prose.
	asJSON bool
	// reason is recorded in the announcement, so a person who finds a vault
	// refusing writes learns what is holding it.
	reason string
	// drain bounds the wait for writers that were already running. Zero means
	// the checkpoint package's default.
	drain time.Duration
	// lease is the lifetime recorded for observers. Nothing enforces it; it is
	// the number a supervisor kills against.
	lease time.Duration
}

func (opts *checkpointOptions) validate(argv []string) error {
	if len(argv) == 0 {
		return errors.New("checkpoint run: no command given after --")
	}
	if opts.drain < 0 {
		return rangeValueError("checkpoint run", "drain", "0 or more", opts.drain)
	}
	if opts.lease < 0 {
		return rangeValueError("checkpoint run", "lease", "0 or more", opts.lease)
	}
	return nil
}

// NewCheckpointCmd returns the `checkpoint` subcommand. Exported for tests.
//
// This is the vault's quiet-window surface, and it exists for callers that are
// not Go programs. The deployment snapshot that copies this vault runs as a
// separate process in another language, and without an invocable door it would
// have to re-implement the barrier's lock names, record format, and recovery
// rules — three copies of an agreement that only works if there is one.
func NewCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Hold the vault still, or report what is holding it",
		Long: `Checkpoint is the vault's quiet window.

While a checkpoint is held, every write that returned success to its caller is
durable and complete, and no write is part-way through its own steps: notes and
configuration, the undo journal and the content history, staged uploads,
activity state, and synchronization transitions. That is what makes a copy of
the vault a state the vault was actually in, rather than a blend of several.

Writers are not failed, they are paused. A write that starts during a
checkpoint waits for it, and only gives up if the checkpoint outlasts its own
wait budget.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newCheckpointStatusCmd())
	cmd.AddCommand(newCheckpointRunCmd())
	return cmd
}

func newCheckpointStatusCmd() *cobra.Command {
	opts := &checkpointOptions{}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report whether a checkpoint is holding this vault",
		Long: `Status reports what the vault's checkpoint record says, plus the one thing
the record cannot say: whether the process that wrote it is still running.

It takes no lock on the barrier and creates nothing, so it is safe to run
against a vault somebody else is writing, and it never blocks.

An announcement whose client is gone is reported as present but not live. It
blocks nothing — writers ignore it and the next checkpoint clears it — so it is
a diagnostic rather than a state to repair by hand.

Exit status is 0 whether or not a checkpoint is held; this is a report.`,
		Args:          noArgs("checkpoint status"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("checkpoint status: no config in context")
			}
			return runCheckpointStatus(cmd, cfg.Vault, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.asJSON, "json", false, "emit the status as JSON")
	return cmd
}

func newCheckpointRunCmd() *cobra.Command {
	opts := &checkpointOptions{}

	cmd := &cobra.Command{
		Use:   "run -- <command> [args...]",
		Short: "Run a command with this vault held still",
		Long: `Run takes the checkpoint, runs the given command, and releases it afterwards
whether the command succeeded or not.

The command is executed directly — no shell, no expansion — and inherits this
process's standard input, output, and error. Its exit status becomes this
command's exit status, so it composes like any other wrapper.

Everything before the command runs is a refusal, not a partial state: if
another checkpoint already holds this vault, or if writers that were already
running do not finish inside --drain, nothing is held and the command is not
run.

The child inherits the checkpoint lock descriptors. If this wrapper dies first,
the vault remains held until the child exits; the kernel then releases the
inherited locks and later writers can proceed safely.`,
		Example: `  bn checkpoint run -- tar czf /backups/vault.tar.gz .
  bn checkpoint run --reason "nightly deployment snapshot" --drain 2m -- ./snapshot.sh`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("checkpoint run: no config in context")
			}
			if err := opts.validate(args); err != nil {
				return err
			}
			return runCheckpointRun(cmd, cfg.Vault, args, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.reason, "reason", "", "short sentence recorded in the announcement")
	flags.DurationVar(&opts.drain, "drain", 0, "how long to wait for in-flight writers (default 30s)")
	flags.DurationVar(&opts.lease, "lease", 0, "lifetime recorded for observers (default 10m)")
	return cmd
}
