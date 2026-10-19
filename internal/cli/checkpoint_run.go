package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
)

// checkpointRunner executes the child process. Tests replace it to avoid
// depending on what happens to be installed on the machine running them.
var checkpointRunner = runCheckpointChild

// runCheckpointStatus reports the vault's checkpoint record.
//
// It asks once and renders what it got. There is deliberately no retry and no
// wait: a status that waited would be a status that could not answer "a
// checkpoint is running right now", which is the question.
func runCheckpointStatus(cmd *cobra.Command, vaultRoot string, opts *checkpointOptions) error {
	state, err := checkpoint.Status(vaultRoot)
	if err != nil {
		return fmt.Errorf("checkpoint status: %w", err)
	}
	if opts.asJSON {
		return renderCheckpointStatusJSON(cmd.OutOrStdout(), state)
	}
	return renderCheckpointStatusText(cmd.OutOrStdout(), state)
}

// runCheckpointRun holds the vault for exactly as long as the child runs.
//
// The release is unconditional — a child that failed leaves the vault as
// quiet as one that succeeded, and a checkpoint kept over a failure would be a
// vault nobody can write to because a snapshot script had a bug. Its own
// failure is reported only when the child succeeded: a record that could not be
// cleared is a liveness problem for the next writer, and reporting it in place
// of the child's exit status would hide the thing the operator asked about.
func runCheckpointRun(cmd *cobra.Command, vaultRoot string, argv []string, opts *checkpointOptions) error {
	held, err := checkpoint.Hold(vaultRoot, checkpoint.Options{
		Reason: opts.holdReason(argv),
		Drain:  opts.drain,
		Lease:  opts.lease,
	})
	if err != nil {
		return fmt.Errorf("checkpoint run: %w", err)
	}

	runErr := checkpointRunner(cmd, argv, held.InheritanceFiles())
	if releaseErr := held.Release(); releaseErr != nil && runErr == nil {
		return fmt.Errorf("checkpoint run: release the checkpoint: %w", releaseErr)
	}
	return runErr
}

// holdReason is what a person reading `bn checkpoint status` sees. An operator
// who gave no reason still gets the program that is holding their vault, which
// is the fact they actually need.
func (opts *checkpointOptions) holdReason(argv []string) string {
	if opts.reason != "" {
		return opts.reason
	}
	if len(argv) == 0 {
		return "bn checkpoint run"
	}
	return "bn checkpoint run " + argv[0]
}

// runCheckpointChild executes argv with no shell and no expansion.
//
// An *exec.ExitError is returned unwrapped, because cmd/bn turns exactly that
// into this process's exit status — wrapping it would still propagate the code
// and would prefix the child's failure with a sentence about bn.
// checkpointChildWaitDelay bounds how long a forwarded SIGTERM may take before
// the child is killed.
const checkpointChildWaitDelay = 10 * time.Second

func runCheckpointChild(cmd *cobra.Command, argv []string, inherited []*os.File) error {
	child := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	child.Stdin = cmd.InOrStdin()
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()
	child.ExtraFiles = inherited
	// The terminal already hands Ctrl-C to the child, which owns it; a SIGTERM
	// sent to bn alone is forwarded rather than escalated straight to a kill.
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = checkpointChildWaitDelay

	var exitErr *exec.ExitError
	switch err := withInteractiveChild(child.Run); {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		return err
	default:
		return fmt.Errorf("checkpoint run: %s: %w", argv[0], err)
	}
}
