package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestRunCopiesStdinToStdoutVerbatim(t *testing.T) {
	t.Setenv("BN_AGENT_HELPER", "1")
	payload := []byte("exact payload\nwith a final line\n")
	out, err := Run(context.Background(), helperCommand("echo"), payload)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("stdout = %q, want %q", out, payload)
	}
}

func TestRunRejectsEmptyCommand(t *testing.T) {
	for _, argv := range [][]string{nil, {}, {"  "}} {
		if _, err := Run(context.Background(), argv, nil); !errors.Is(err, ErrEmptyCommand) {
			t.Fatalf("Run(%q) error = %v, want ErrEmptyCommand", argv, err)
		}
	}
}

func TestRunReportsMissingExecutable(t *testing.T) {
	_, err := Run(context.Background(), []string{"/definitely/missing/bn-agent"}, nil)
	if err == nil {
		t.Fatal("Run: got nil error for missing executable")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("Run error = %v, want wrapped os.PathError", err)
	}
}

func TestRunReportsNonZeroExitWithBoundedSanitizedStderr(t *testing.T) {
	t.Setenv("BN_AGENT_HELPER", "1")
	_, err := Run(context.Background(), helperCommand("fail"), nil)
	if err == nil {
		t.Fatal("Run: got nil error for non-zero exit")
	}
	message := err.Error()
	if !strings.Contains(message, "unsafe [31m detail") {
		t.Fatalf("error missing sanitized stderr: %q", message)
	}
	if !strings.HasSuffix(message, "…") {
		t.Fatalf("error does not mark truncated stderr: %q", message)
	}
	if len(message) > maxStderrBytes+maxExecErrorBytes+128 {
		t.Fatalf("error length = %d, want bounded diagnostic", len(message))
	}
	for _, r := range message {
		if unicode.IsControl(r) {
			t.Fatalf("error contains control rune U+%04X: %q", r, message)
		}
	}
}

func TestRunHonorsTimeout(t *testing.T) {
	t.Setenv("BN_AGENT_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, helperCommand("sleep"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timed-out command stopped after %s", elapsed)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	t.Setenv("BN_AGENT_HELPER", "1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, helperCommand("echo"), []byte("ignored"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want Canceled", err)
	}
}

func helperCommand(mode string) []string {
	return []string{os.Args[0], "-test.run=TestAgentHelperProcess", "--", mode}
}

func TestAgentHelperProcess(t *testing.T) {
	if os.Getenv("BN_AGENT_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	case "fail":
		_, _ = fmt.Fprint(os.Stderr, "unsafe\n\x1b[31m detail "+strings.Repeat("x", maxStderrBytes+256))
		os.Exit(7)
	case "sleep":
		time.Sleep(5 * time.Second)
	default:
		os.Exit(2)
	}
}
