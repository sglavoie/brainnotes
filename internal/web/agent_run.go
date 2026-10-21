package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/agent"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// readAgentPrompt validates the optional free-text prompt the insight routes
// accept. Both bounds are about what is handed to a subprocess on stdin: invalid
// UTF-8 is refused because the context packet is markdown and a rune count is
// refused because an unbounded prompt is an unbounded argument to somebody
// else's model. It has already answered when it reports false.
func readAgentPrompt(w http.ResponseWriter, r *http.Request) (string, bool) {
	prompt := strings.TrimSpace(r.Form.Get("prompt"))
	if !utf8.ValidString(prompt) {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "AI prompt must be valid UTF-8"))
		return "", false
	}
	if utf8.RuneCountInString(prompt) > agentPromptMaxRunes {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "AI prompt must be %d characters or fewer", agentPromptMaxRunes))
		return "", false
	}
	return prompt, true
}

// agentRunLabels name the two failures an agent-backed route can report. They
// are supplied per route rather than derived, because the wording is what a
// person reads when a command fails and each route describes a different action.
type agentRunLabels struct {
	timeout string
	failure string
}

// runAgentForRequest is the one place an HTTP request runs the configured agent
// command. It applies the route's timeout, goes through the capability-checked
// door in launchAgent, and turns a failure into the management error the route
// reports; it has already answered when it reports false.
//
// The four agent routes used to carry a copy of this each. They are identical
// but for the two labels, and a copy is a place where one route forgets to
// sanitize the diagnostic and puts a command line in a response body.
func (s *Server) runAgentForRequest(
	w http.ResponseWriter,
	r *http.Request,
	argv []string,
	stdin []byte,
	timeoutSeconds int,
	labels agentRunLabels,
) ([]byte, bool) {
	ctx := r.Context()
	if timeout := time.Duration(timeoutSeconds) * time.Second; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := s.launchAgent(ctx, argv, stdin)
	if err == nil {
		return output, true
	}
	// The deadline is reported from the context rather than from the error,
	// because a command killed by the timeout usually surfaces as a signal and
	// "signal: killed" is not what went wrong from the caller's side.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeManagementError(w, noteops.Errorf(noteops.CodeInternal, "%s", labels.timeout))
		return nil, false
	}
	message := labels.failure
	if diagnostic := agent.SanitizedDiagnostic(err); diagnostic != "" {
		message += ": " + diagnostic
	}
	writeManagementError(w, noteops.WrapError(noteops.CodeInternal, message, err))
	return nil, false
}
