package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// doctorOptions is the flag state of one `bn doctor` invocation.
//
// There is no validate(): every flag composes. --review-context implies --daily
// rather than conflicting with it, --folder narrows any of the three renderings,
// and --json is simply ignored once --review-context has chosen JSON already.
type doctorOptions struct {
	asJSON        bool
	daily         bool
	reviewContext bool
	folder        string
}

// runDoctorCommand builds the hygiene queue and renders it. It is read-only
// apart from cockpit.StateStore.FilterQueue, which prunes stored records for
// tasks the queue no longer contains.
//
// Order is behaviour, and each step depends on the one before it: workflow-state
// filtering runs first so a done or snoozed task can never consume a daily slot,
// folder scoping second, and the daily cap last. The counts in the footer stay
// the full totals for the scope, not the capped ones.
func runDoctorCommand(cmd *cobra.Command, opts doctorOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("doctor: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	rawQueue, err := cockpit.BuildQueue(v, cfg)
	if err != nil {
		return err
	}

	store := cockpit.NewStateStore(v.Root)
	queue, err := store.FilterQueue(rawQueue)
	if err != nil {
		return err
	}

	if strings.TrimSpace(opts.folder) != "" {
		queue, err = scopeQueueToFolder(queue, rawQueue, store, opts.folder)
		if err != nil {
			return err
		}
	}

	// The review context is inherently a daily surface, so it caps too.
	if opts.daily || opts.reviewContext {
		queue = cockpit.LimitDaily(queue, cfg.Cockpit.DailyLimit)
	}

	// Companion version skew is a health finding but not a hygiene task, so it
	// goes to stderr and leaves the queue (and its JSON) exactly as it was.
	writeCompanionBuildWarnings(cmd.ErrOrStderr(), v.Root)
	return writeDoctorQueue(cmd, queue, opts)
}

// writeDoctorQueue picks the rendering. The review context is a deterministic,
// redacted, read-only view of the daily queue built for agent consumption; it
// never invokes any AI command and is always emitted as JSON, which is why it
// is decided before --json rather than alongside it.
func writeDoctorQueue(cmd *cobra.Command, queue cockpit.Queue, opts doctorOptions) error {
	out := cmd.OutOrStdout()
	switch {
	case opts.reviewContext:
		return writeDoctorJSON(out, cockpit.BuildReviewContext(queue))
	case opts.asJSON:
		return writeDoctorJSON(out, queue)
	default:
		writeDoctorHuman(out, queue)
		return nil
	}
}
