//go:build unix

package agent

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunTimeoutKillsLauncherProcessGroup(t *testing.T) {
	t.Setenv("BN_AGENT_HELPER", "1")
	pidPath := childPIDPath(t)
	t.Setenv("BN_AGENT_CHILD_PID_FILE", pidPath)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := Run(ctx, helperCommand("spawn-child"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process-group timeout returned after %s", elapsed)
	}

	rawPID, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatalf("read grandchild pid: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if parseErr != nil {
		t.Fatalf("parse grandchild pid %q: %v", rawPID, parseErr)
	}

	deadline := time.Now().Add(time.Second)
	for {
		probeErr := syscall.Kill(pid, 0)
		if errors.Is(probeErr, syscall.ESRCH) {
			break
		}
		if probeErr != nil {
			t.Fatalf("probe grandchild pid %d: %v", pid, probeErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d survived process-group cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
