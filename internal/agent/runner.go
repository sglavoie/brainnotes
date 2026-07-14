// Package agent runs explicitly configured local agent commands.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxStderrBytes    = 4096
	maxExecErrorBytes = 512
)

// ErrEmptyCommand is returned when a caller attempts to run an empty argv.
var ErrEmptyCommand = errors.New("agent: empty command")

// Runner is the injectable boundary used by CLI and web callers. Implementations
// receive an argv slice, never a shell command string.
type Runner func(ctx context.Context, argv []string, stdin []byte) ([]byte, error)

// Run invokes argv[0] directly with the remaining argv elements, writes stdin to
// the child, and returns stdout verbatim. Deadlines and cancellation are inherited
// from ctx. Failed commands report only a sanitized, bounded stderr excerpt.
func Run(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("agent: context is required")
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return nil, ErrEmptyCommand
	}
	for _, arg := range argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, errors.New("agent: command contains a NUL byte")
		}
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("agent: command canceled: %w", ctxErr)
		}
		message := "agent: command failed: " + sanitizeDiagnostic([]byte(err.Error()), maxExecErrorBytes, false)
		if detail := sanitizeDiagnostic(stderr.Bytes(), maxStderrBytes, stderr.truncated); detail != "" {
			message += ": " + detail
		}
		return nil, &runError{message: message, cause: err}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type runError struct {
	message string
	cause   error
}

func (e *runError) Error() string { return e.message }
func (e *runError) Unwrap() error { return e.cause }

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }

func sanitizeDiagnostic(raw []byte, limit int, truncated bool) string {
	if len(raw) > limit {
		raw = raw[:limit]
		truncated = true
	}
	text := strings.ToValidUTF8(string(raw), "�")
	var out strings.Builder
	lastSpace := true
	for _, r := range text {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !lastSpace {
				out.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		if out.Len()+utf8.RuneLen(r) > limit {
			truncated = true
			break
		}
		out.WriteRune(r)
		lastSpace = false
	}
	result := strings.TrimSpace(out.String())
	if truncated {
		if result == "" {
			return "…"
		}
		return result + " …"
	}
	return result
}
