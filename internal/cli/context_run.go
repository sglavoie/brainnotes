package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// runContextCommand builds the bounded research packet for one note.
//
// The whole command is read-only: it ranks through the same recall corpus
// `bn related` uses, but it never records an opened pair, so nothing under
// <vault>/.bnotes is written on any path through here — including the --agent
// path. That is the read side of the boundary in
// docs/internals/recall-context.md.
//
// Order is behaviour and is preserved from the original closure: the flag
// matrix is refused before the config is read, and the missing-agent.command
// refusal comes after the config load but before the vault is opened, so
// `bn context x --agent` with no agent configured reports the configuration
// error rather than a vault error.
//
// agentRunner is a parameter rather than a package variable because
// NewContextCmd is the only production caller and passes agent.Run; tests
// construct the command through newContextCmd with their own runner and never
// spawn a process.
func runContextCommand(cmd *cobra.Command, args []string, options contextOptions, agentRunner agent.Runner) error {
	validated, err := validateContextOptions(options)
	if err != nil {
		return err
	}

	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("context: no config in context")
	}
	if options.runAgent && len(cfg.Agent.Command) == 0 {
		return errors.New("context: --agent requires agent.command configuration")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	setup, err := newRecallSetup(v.Root, recallNow())
	if err != nil {
		return err
	}
	payload, err := buildContextPayload(cmd.ErrOrStderr(), v, args[0], validated, setup.rankOptions())
	if err != nil {
		return err
	}

	if !options.runAgent {
		return writeContextPayload(cmd.OutOrStdout(), payload)
	}
	output, err := runContextAgent(cmd, cfg, agentRunner, payload)
	if err != nil {
		return err
	}
	return writeContextPayload(cmd.OutOrStdout(), output)
}

// runContextAgent hands the rendered Markdown packet to the configured local
// agent and returns what it wrote back. The command argv is copied before it is
// passed on so the runner cannot alias the loaded config, and the timeout is
// applied only when agent.timeout_seconds is positive — zero means the caller's
// context is the only deadline.
func runContextAgent(
	cmd *cobra.Command,
	cfg *config.Config,
	agentRunner agent.Runner,
	payload []byte,
) ([]byte, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout := time.Duration(cfg.Agent.TimeoutSeconds) * time.Second; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	output, err := agentRunner(ctx, append([]string(nil), cfg.Agent.Command...), payload)
	if err != nil {
		return nil, fmt.Errorf("context: run agent: %w", err)
	}
	return output, nil
}
