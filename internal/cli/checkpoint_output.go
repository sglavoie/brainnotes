package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
)

// renderCheckpointStatusJSON emits checkpoint.State verbatim.
//
// The struct is the contract, not this function: a deployment script reads
// `blocking` and nothing else has to be parsed out of prose.
func renderCheckpointStatusJSON(w io.Writer, state checkpoint.State) error {
	if err := writeJSON(w, state); err != nil {
		return fmt.Errorf("checkpoint status: encode JSON: %w", err)
	}
	return nil
}

// renderCheckpointStatusText writes the one-line human form.
//
// The three states are deliberately three different sentences rather than one
// sentence with a field that changes. "Held" and "announced by a process that
// is gone" have opposite consequences for whoever is reading — one means wait,
// the other means nothing is wrong — and a reader should not have to notice a
// boolean to tell them apart.
func renderCheckpointStatusText(w io.Writer, state checkpoint.State) error {
	switch {
	case !state.Announced:
		_, err := fmt.Fprintln(w, "checkpoint: none")
		return err
	case state.Live:
		_, err := fmt.Fprintf(w, "checkpoint: held (%s)\n", checkpointStatusFields(state))
		return err
	default:
		_, err := fmt.Fprintf(w,
			"checkpoint: an announcement is present but its client is gone, so it is blocking nothing (%s)\n",
			checkpointStatusFields(state))
		return err
	}
}

// checkpointStatusFields renders the record as key=value pairs, leaving out
// what the record does not say rather than printing an empty field.
func checkpointStatusFields(state checkpoint.State) string {
	fields := []string{"id=" + state.ID}
	if state.Phase != "" {
		fields = append(fields, "phase="+string(state.Phase))
	}
	if state.Reason != "" {
		fields = append(fields, fmt.Sprintf("reason=%q", state.Reason))
	}
	if state.PID > 0 {
		fields = append(fields, fmt.Sprintf("pid=%d", state.PID))
	}
	if state.Host != "" {
		fields = append(fields, "host="+state.Host)
	}
	if !state.AnnouncedAt.IsZero() {
		fields = append(fields, "announced="+state.AnnouncedAt.UTC().Format(time.RFC3339))
	}
	if !state.Deadline.IsZero() {
		fields = append(fields, "deadline="+state.Deadline.UTC().Format(time.RFC3339))
	}
	return strings.Join(fields, " ")
}
