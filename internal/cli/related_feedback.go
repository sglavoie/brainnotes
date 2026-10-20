package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type relatedFeedbackOptions struct {
	stateText string
	untilText string
}

// NewRelatedFeedbackCmd returns the explicit recall-feedback subcommand.
func NewRelatedFeedbackCmd() *cobra.Command {
	options := relatedFeedbackOptions{}
	cmd := &cobra.Command{
		Use:           "feedback <source> <candidate>",
		Short:         "Record feedback for a related-note pair",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := recallNow()
			feedback, until, err := validateRelatedFeedbackOptions(options, now)
			if err != nil {
				return err
			}
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("related feedback: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}
			sourcePath, err := relatedFeedbackNotePath(v, args[0])
			if err != nil {
				return fmt.Errorf("related feedback: resolve source: %w", err)
			}
			candidatePath, err := relatedFeedbackNotePath(v, args[1])
			if err != nil {
				return fmt.Errorf("related feedback: resolve candidate: %w", err)
			}
			store := recall.NewStateStore(v.Root, recall.WithClock(func() time.Time { return now }))
			if err := store.SetFeedback(sourcePath, candidatePath, feedback, until); err != nil {
				return fmt.Errorf("related feedback: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&options.stateText, "state", "", "feedback state: useful, dismissed, later, or clear")
	cmd.Flags().StringVar(&options.untilText, "until", "", "future local date for --state later (YYYY-MM-DD)")
	return cmd
}

func validateRelatedFeedbackOptions(
	options relatedFeedbackOptions,
	now time.Time,
) (recall.FeedbackState, time.Time, error) {
	feedback := recall.FeedbackState(strings.TrimSpace(options.stateText))
	if feedback == "" {
		return "", time.Time{}, errors.New("related feedback: --state is required (useful, dismissed, later, or clear)")
	}
	if !feedback.Valid() {
		return "", time.Time{}, enumValueError("related feedback", "state", options.stateText,
			"useful", "dismissed", "later", "clear")
	}
	untilText := strings.TrimSpace(options.untilText)
	if feedback != recall.FeedbackLater {
		if untilText != "" {
			return "", time.Time{}, fmt.Errorf("related feedback: --until is only valid with --state later")
		}
		return feedback, time.Time{}, nil
	}
	if untilText == "" {
		return "", time.Time{}, errors.New("related feedback: --until is required with --state later")
	}
	until, err := time.ParseInLocation("2006-01-02", untilText, now.Location())
	if err != nil {
		// Go's layout-parse error ("cannot parse ... as \"2006\"") says nothing
		// a user can act on, so it is not wrapped.
		return "", time.Time{}, fmt.Errorf("related feedback: --until must be a date like 2026-01-31 (got %q)", untilText)
	}
	year, month, day := now.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	if !until.After(today) {
		return "", time.Time{}, fmt.Errorf("related feedback: --until must be a future local calendar date")
	}
	return feedback, until, nil
}

func relatedFeedbackNotePath(v *vault.Vault, argument string) (string, error) {
	note, err := resolveNoteTarget("related feedback", v, argument)
	if err != nil {
		return "", err
	}
	document, err := recall.ExtractDocument(v.Root, note, recall.ExtractOptions{})
	if err != nil {
		return "", err
	}
	return document.Path, nil
}
