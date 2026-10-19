package cli

import (
	"errors"
	"fmt"
	"io"

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
	// json is `--json`, the spelling every other command uses, accepted as
	// an alias for `--format json`.
	json bool
	// formatSet records an explicit --format, so `--json --format markdown`
	// is refused rather than resolved by flag order.
	formatSet bool
}

type validatedContextOptions struct {
	related  int
	maxBytes int
	format   string
	purpose  recall.ContextPurpose
}

// contextFormat resolves --json and --format into one format and refuses the
// combinations that cannot be honoured.
func contextFormat(options contextOptions) (string, error) {
	format := options.formatText
	if options.json {
		if options.formatSet && format != contextFormatJSON {
			return "", fmt.Errorf("context: --json cannot be combined with --format %s", format)
		}
		format = contextFormatJSON
	}
	if format != contextFormatMarkdown && format != contextFormatJSON {
		return "", fmt.Errorf("context: --format must be markdown or json (got %q)", format)
	}
	if options.runAgent && format == contextFormatJSON {
		return "", errors.New("context: --agent requires --format markdown")
	}
	return format, nil
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
			options.formatSet = cmd.Flags().Changed("format")
			return runContextCommand(cmd, args, options, agentRunner)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&options.related, "related", options.related, "maximum related notes to include (0-10)")
	flags.IntVar(&options.maxBytes, "max-bytes", options.maxBytes, "maximum serialized packet size in bytes (1024-1048576)")
	flags.StringVar(&options.formatText, "format", options.formatText, "output format: markdown or json")
	flags.BoolVar(&options.json, "json", false, "shorthand for --format json")
	flags.StringVar(&options.purposeText, "purpose", options.purposeText, "research purpose: synthesis, contradictions, or questions")
	flags.BoolVar(&options.runAgent, "agent", false, "send the Markdown packet to the configured local agent")

	return cmd
}

func validateContextOptions(options contextOptions) (validatedContextOptions, error) {
	format, err := contextFormat(options)
	if err != nil {
		return validatedContextOptions{}, err
	}
	options.formatText = format
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
