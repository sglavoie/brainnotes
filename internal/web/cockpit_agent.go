package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// dailyReviewAgentResponse is the POST /api/daily-review/agent payload. When the
// optional agent command is not configured it reports Enabled=false and carries
// no output; otherwise it returns the command's stdout verbatim (markdown or
// plain text). It is never a note mutation: the agent only reads the redacted,
// daily-limited review context that was fed to it on stdin.
type dailyReviewAgentResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Enabled bool   `json:"enabled"`
	Output  string `json:"output,omitempty"`
}

// handleDailyReviewAgent runs the optional, configured AI daily-review command.
// It POSTs the deterministic, redacted daily-review context to the command on
// stdin (argv-only invocation, no shell) and returns the command's stdout. When
// cockpit.agent_command is unset it returns a clean disabled response rather than
// an error, so the client can offer the feature only when it is available.
func (s *Server) handleDailyReviewAgent(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/daily-review/agent" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return
	}

	// argv is split on whitespace only — never handed to a shell — so no quoting,
	// globbing, piping, or metacharacter interpretation is possible. An empty
	// command (the default) means AI integration is disabled.
	argv := strings.Fields(s.cfg.Cockpit.AgentCommand)
	if len(argv) == 0 {
		writeManagementJSON(w, http.StatusOK, dailyReviewAgentResponse{
			OK:      true,
			Enabled: false,
			Message: "AI daily review is not configured.",
		})
		return
	}

	// Assemble the same redacted, workflow-filtered, daily-limited context the
	// read-only /api/daily-review/context endpoint returns, then hand it to the
	// agent on stdin. Only human-safe fields cross this boundary — no raw note
	// bodies, no file hashes, and only server-redacted sensitive locations.
	queue, err := s.cockpitQueueFiltered()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	queue = cockpit.LimitDaily(queue, s.cfg.Cockpit.DailyLimit)
	stdin, err := json.Marshal(cockpit.BuildReviewContext(queue))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "encode review context", err))
		return
	}

	timeout := time.Duration(s.cfg.Cockpit.AgentTimeoutSeconds) * time.Second
	ctx := r.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, runErr := s.runAgent(ctx, argv, stdin)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeManagementError(w, noteops.Errorf(noteops.CodeInternal, "AI daily review command timed out"))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "run AI daily review command", runErr))
		return
	}

	writeManagementJSON(w, http.StatusOK, dailyReviewAgentResponse{
		OK:      true,
		Enabled: true,
		Output:  string(output),
	})
}

// runAgentCommand is the default agent runner: it invokes argv[0] with the
// remaining elements as arguments (no shell), writes the redacted context JSON to
// the command's stdin, and returns its stdout. The context carries the configured
// timeout, so a hung command is killed rather than blocking the request.
func runAgentCommand(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("web: empty agent command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}
