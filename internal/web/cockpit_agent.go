package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
// both the legacy cockpit override and shared agent command are unset it returns
// a clean disabled response rather than an error, so the client can offer the
// feature only when it is available.
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

	// A configured legacy cockpit command takes precedence so existing daily
	// review setups retain their behavior. Otherwise use the shared argv config.
	argv, timeoutSeconds := s.dailyReviewAgentConfig()
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

	timeout := time.Duration(timeoutSeconds) * time.Second
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

func (s *Server) dailyReviewAgentConfig() ([]string, int) {
	if legacy := strings.TrimSpace(s.cfg.Cockpit.AgentCommand); legacy != "" {
		// Legacy configuration is intentionally split exactly as before. It is
		// still passed directly as argv and is never interpreted by a shell.
		return strings.Fields(legacy), s.cfg.Cockpit.AgentTimeoutSeconds
	}
	return append([]string(nil), s.cfg.Agent.Command...), s.cfg.Agent.TimeoutSeconds
}
