package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewDoctorCmd builds the read-only cockpit/health queue command. It surfaces
// the same deterministic hygiene queue the web cockpit serves, honoring the
// per-task workflow state (done/snoozed) stored under .bnotes/cockpit.json.
func NewDoctorCmd() *cobra.Command {
	var (
		asJSON        bool
		daily         bool
		reviewContext bool
		folder        string
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show the vault hygiene queue (Daily Fix Cockpit)",
		Long: `Doctor reports the current hygiene queue for the vault: frontmatter parse
errors, schema violations, broken link targets, duplicate titles, orphans,
dead ends, and long-tail property values. It is read-only and never modifies
files.

Tasks marked done or snoozed via the cockpit (web UI or state file) are hidden;
the footer reports how many were suppressed. Use --json for a stable,
machine-readable view of every task and its available actions. Use --daily to
cap the surfaced tasks to the daily cockpit view (cockpit.daily_limit); the
footer still reports the full active/hidden totals. Use --review-context to emit
a deterministic, redacted JSON snapshot of the daily queue for agent review; it
implies --daily and never invokes any AI command.

Exit code is always 0 for a successful scan; an empty queue means the vault is
clean.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if strings.TrimSpace(folder) != "" {
				queue, err = scopeQueueToFolder(queue, rawQueue, store, folder)
				if err != nil {
					return err
				}
			}

			// Daily view caps the surfaced tasks last, after workflow-state
			// filtering and any folder scoping, so done/snoozed tasks never
			// consume a daily slot. Counts stay intact as the full totals. The
			// review context is inherently a daily surface, so it caps too.
			if daily || reviewContext {
				queue = cockpit.LimitDaily(queue, cfg.Cockpit.DailyLimit)
			}

			// The review context is a deterministic, redacted, read-only view of
			// the daily queue built for agent consumption; it never invokes any
			// AI command. It is always emitted as JSON.
			if reviewContext {
				return writeDoctorJSON(cmd.OutOrStdout(), cockpit.BuildReviewContext(queue))
			}

			if asJSON {
				return writeDoctorJSON(cmd.OutOrStdout(), queue)
			}
			writeDoctorHuman(cmd.OutOrStdout(), queue)
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the queue as JSON")
	cmd.Flags().BoolVar(&daily, "daily", false, "limit to the daily cockpit view (at most cockpit.daily_limit tasks)")
	cmd.Flags().BoolVar(&reviewContext, "review-context", false, "emit the deterministic, redacted daily-review context as JSON (implies --daily; no AI command is run)")
	cmd.Flags().StringVar(&folder, "folder", "", "only tasks touching a note under this vault-relative folder")

	return cmd
}

// scopeQueueToFolder keeps only tasks that touch at least one affected note
// under folder (the folder itself or a descendant). It is the one find-style
// filter that maps cleanly onto the grouped queue: tasks retain note-path
// context, so folder membership is decidable without reloading the vault. Other
// find dimensions (tag/status/age/…) operate on individual notes and do not map
// onto tasks that group many notes, so they are intentionally not offered here.
// Tasks are kept whole (affected lists are not pruned) so signatures and counts
// stay consistent with the queue the cockpit generated.
//
// active is the already-filtered queue (done/snoozed tasks removed); raw is the
// full BuildQueue result including suppressed tasks. All four counts are scoped
// to the folder: the active list comes from active.Tasks, while DoneHidden,
// SnoozedHidden, and TotalGenerated are recomputed by classifying the raw tasks
// under the folder against persisted state via store.CountScoped. The raw queue
// must not be re-run through FilterQueue — that would delete stored state for
// out-of-folder tasks (StateStore prunes records absent from the queue it is
// handed), so the single full-vault FilterQueue in RunE remains the only persist.
func scopeQueueToFolder(active, raw cockpit.Queue, store *cockpit.StateStore, folder string) (cockpit.Queue, error) {
	prefix := filepath.ToSlash(strings.Trim(strings.TrimSpace(folder), "/"))

	kept := make([]cockpit.Task, 0, len(active.Tasks))
	for _, task := range active.Tasks {
		if taskUnderFolder(task, prefix) {
			kept = append(kept, task)
		}
	}

	scopedRaw := make([]cockpit.Task, 0, len(raw.Tasks))
	for _, task := range raw.Tasks {
		if taskUnderFolder(task, prefix) {
			scopedRaw = append(scopedRaw, task)
		}
	}
	counts, err := store.CountScoped(scopedRaw)
	if err != nil {
		return cockpit.Queue{}, err
	}

	active.Tasks = kept
	active.Counts = counts
	return active, nil
}

// taskUnderFolder reports whether any affected note of the task lives under the
// slash-normalized folder prefix. An empty prefix matches every task.
func taskUnderFolder(task cockpit.Task, prefix string) bool {
	if prefix == "" {
		return true
	}
	for _, note := range task.Affected {
		rel := filepath.ToSlash(note.RelPath)
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

// writeDoctorJSON emits a stable, indented JSON view. It serves both the full
// queue (--json) and the redacted review context (--review-context), so it takes
// any serializable payload rather than only cockpit.Queue.
func writeDoctorJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func writeDoctorHuman(w io.Writer, queue cockpit.Queue) {
	if len(queue.Tasks) == 0 {
		fmt.Fprintln(w, "No hygiene tasks. Vault is clean.")
		writeDoctorFooter(w, queue.Counts)
		return
	}

	for i, task := range queue.Tasks {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s: %s\n", task.Kind, task.Title)
		fmt.Fprintf(w, "  %s\n", task.Summary)
		for _, note := range task.Affected {
			if note.Location != "" {
				fmt.Fprintf(w, "    %s (%s)\n", note.RelPath, note.Location)
			} else {
				fmt.Fprintf(w, "    %s\n", note.RelPath)
			}
		}
		if labels := actionLabels(task.Actions); labels != "" {
			fmt.Fprintf(w, "  actions: %s\n", labels)
		}
	}

	writeDoctorFooter(w, queue.Counts)
}

func actionLabels(actions []cockpit.Action) string {
	if len(actions) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(actions))
	for _, a := range actions {
		kinds = append(kinds, string(a.Kind))
	}
	return strings.Join(kinds, ", ")
}

func writeDoctorFooter(w io.Writer, counts cockpit.Counts) {
	noun := "tasks"
	if counts.Active == 1 {
		noun = "task"
	}
	var hidden []string
	if counts.DoneHidden > 0 {
		hidden = append(hidden, fmt.Sprintf("%d done", counts.DoneHidden))
	}
	if counts.SnoozedHidden > 0 {
		hidden = append(hidden, fmt.Sprintf("%d snoozed", counts.SnoozedHidden))
	}
	if len(hidden) == 0 {
		fmt.Fprintf(w, "\n%d active %s.\n", counts.Active, noun)
		return
	}
	fmt.Fprintf(w, "\n%d active %s (%s hidden).\n", counts.Active, noun, strings.Join(hidden, ", "))
}
