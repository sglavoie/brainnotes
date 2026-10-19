//go:build unix

package cli

import (
	"io"
	"syscall"
	"testing"
	"time"
)

// These tests signal the test process itself. Each one sends exactly the
// signals that notifyInterrupt is still listening for, because a signal that
// arrives after it hands delivery back to the default handler ends the run.

func TestNotifyInterruptCancelsOnSIGINT(t *testing.T) {
	ctx, stop := notifyInterrupt(io.Discard)
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT did not cancel the command context")
	}
}

func TestNotifyInterruptLeavesSIGINTToInteractiveChild(t *testing.T) {
	ctx, stop := notifyInterrupt(io.Discard)
	defer stop()

	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withInteractiveChild(func() error {
			<-release
			return nil
		})
	}()
	waitFor(t, func() bool { return interactiveChildren.Load() > 0 })

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("SIGINT meant for fzf cancelled bn's context")
	case <-time.After(200 * time.Millisecond):
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not cancel while a child held the terminal")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(time.Millisecond)
	}
}
