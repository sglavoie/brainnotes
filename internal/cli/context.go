package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	minContextRelated = 0
	maxContextRelated = 10
	minContextBytes   = 1024
	maxContextBytes   = 1024 * 1024

	contextFormatMarkdown = "markdown"
	contextFormatJSON     = "json"
)

type contextOptions struct {
	related     int
	maxBytes    int
	formatText  string
	purposeText string
	runAgent    bool
}

type validatedContextOptions struct {
	related  int
	maxBytes int
	format   string
	purpose  recall.ContextPurpose
}

// NewContextCmd returns the deterministic context-bundle export command.
func NewContextCmd() *cobra.Command {
	return newContextCmd(agent.Run)
}

func newContextCmd(agentRunner agent.Runner) *cobra.Command {
	options := contextOptions{
		related:     recall.DefaultBundleRelatedLimit,
		maxBytes:    recall.DefaultBundleMaxBytes,
		formatText:  contextFormatMarkdown,
		purposeText: recall.PurposeSynthesis,
	}

	cmd := &cobra.Command{
		Use:           "context <note>",
		Short:         "Export a bounded research context packet",
		Long:          "Export a deterministic, read-only research packet containing a complete source note and bounded excerpts from related notes.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			commandNow := relatedNow()
			stateStore := recall.NewStateStore(v.Root, recall.WithClock(func() time.Time { return commandNow }))
			if err := stateStore.Validate(); err != nil {
				return err
			}
			payload, err := buildContextPayload(v, args[0], validated, recall.RankOptions{
				Now:    func() time.Time { return commandNow },
				Recall: stateStore,
			})
			if err != nil {
				return err
			}
			if !options.runAgent {
				return writeContextPayload(cmd.OutOrStdout(), payload)
			}

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
				return fmt.Errorf("context: run agent: %w", err)
			}
			return writeContextPayload(cmd.OutOrStdout(), output)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&options.related, "related", options.related, "maximum related notes to include (0-10)")
	flags.IntVar(&options.maxBytes, "max-bytes", options.maxBytes, "maximum serialized packet size in bytes (1024-1048576)")
	flags.StringVar(&options.formatText, "format", options.formatText, "output format: markdown or json")
	flags.StringVar(&options.purposeText, "purpose", options.purposeText, "research purpose: synthesis, contradictions, or questions")
	flags.BoolVar(&options.runAgent, "agent", false, "send the Markdown packet to the configured local agent")

	return cmd
}

func validateContextOptions(options contextOptions) (validatedContextOptions, error) {
	if options.runAgent && options.formatText == contextFormatJSON {
		return validatedContextOptions{}, errors.New("context: --agent requires --format markdown")
	}
	if options.related < minContextRelated || options.related > maxContextRelated {
		return validatedContextOptions{}, fmt.Errorf(
			"context: --related must be between %d and %d (got %d)",
			minContextRelated,
			maxContextRelated,
			options.related,
		)
	}
	if options.maxBytes < minContextBytes || options.maxBytes > maxContextBytes {
		return validatedContextOptions{}, fmt.Errorf(
			"context: --max-bytes must be between %d and %d (got %d)",
			minContextBytes,
			maxContextBytes,
			options.maxBytes,
		)
	}
	if options.formatText != contextFormatMarkdown && options.formatText != contextFormatJSON {
		return validatedContextOptions{}, fmt.Errorf(
			"context: --format must be markdown or json (got %q)",
			options.formatText,
		)
	}
	purpose, ok := recall.StandardPurpose(options.purposeText)
	if !ok {
		return validatedContextOptions{}, fmt.Errorf(
			"context: --purpose must be synthesis, contradictions, or questions (got %q)",
			options.purposeText,
		)
	}
	return validatedContextOptions{
		related:  options.related,
		maxBytes: options.maxBytes,
		format:   options.formatText,
		purpose:  purpose,
	}, nil
}

func buildContextPayload(
	v *vault.Vault,
	sourceArg string,
	options validatedContextOptions,
	rankOptions recall.RankOptions,
) ([]byte, error) {
	queryResult, err := buildRelatedQuery(v, sourceArg, recall.ModeBalanced, rankOptions)
	if err != nil {
		return nil, err
	}
	bundleOptions := recall.DefaultContextBundleOptions()
	bundleOptions.RelatedLimit = options.related
	bundleOptions.RelatedLimitSet = true
	bundleOptions.MaxBytes = options.maxBytes
	bundleOptions.Purpose = options.purpose
	bundle, err := recall.BuildContextBundle(
		queryResult.source,
		queryResult.documents,
		queryResult.results,
		bundleOptions,
	)
	if err != nil {
		return nil, err
	}
	if options.format == contextFormatJSON {
		return recall.RenderContextJSON(bundle)
	}
	return recall.RenderContextMarkdown(bundle)
}

func writeContextPayload(writer io.Writer, payload []byte) error {
	written, err := writer.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}
