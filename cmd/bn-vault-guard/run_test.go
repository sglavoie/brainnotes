package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// A file that does not decode at startup is fatal. There is no last valid policy
// to retain yet, and a guard that invented one would be enforcing a floor the
// vault never asked for.
func TestRunRefusesToStartOnAnInvalidConfig(t *testing.T) {
	root := newVault(t, "5m")
	initGit(t, root)
	writeConfig(t, root, "[autosave]\nmin_interval = \"nope\"\n")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "bn-vault-guard:") {
		t.Fatalf("no diagnostic on stderr: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("a refused startup logged events: %q", stdout.String())
	}
}

// A vault with no Git history is a supported vault with nothing to watch, and it
// exits 0 so the LaunchAgent does not restart it forever.
func TestRunExitsCleanlyWhenTheVaultHasNoHistory(t *testing.T) {
	newVault(t, "5m")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "no content history") {
		t.Fatalf("stderr does not explain the exit: %q", stderr.String())
	}
}

// cancelOnFirstWrite cancels as soon as the guard logs anything, which is the
// signal-driven shutdown without the signal and without a sleep: the first
// record is written from the loop's own goroutine, so the cancellation lands
// while the loop is running rather than before it starts.
type cancelOnFirstWrite struct {
	buf    *bytes.Buffer
	cancel context.CancelFunc
	fired  bool
}

func (w *cancelOnFirstWrite) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if !w.fired {
		w.fired = true
		w.cancel()
	}
	return n, err
}

// A guard runs its configuration through startup, enters the loop, and leaves it
// on cancellation — the whole path the LaunchAgent exercises, ending in the
// clean exit that stops it being restarted.
func TestRunStartsUnderTheConfiguredPolicyAndStopsOnCancellation(t *testing.T) {
	root := newVault(t, "off")
	initGit(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr bytes.Buffer
	out := &cancelOnFirstWrite{buf: &stdout, cancel: cancel}
	if code := run(ctx, out, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %q)", code, stderr.String())
	}

	events := decodeLog(t, stdout.String())
	if len(events) == 0 {
		t.Fatal("the guard logged nothing at all")
	}
	if events[0]["event"] != string(vaultgit.WatchEventStarted) {
		t.Fatalf("the first event is %v, want %q", events[0]["event"], vaultgit.WatchEventStarted)
	}
	if last := events[len(events)-1]; last["event"] != string(vaultgit.WatchEventStopped) {
		t.Fatalf("the last event is %v, want %q", last["event"], vaultgit.WatchEventStopped)
	}
}

// The single-instance lock is per vault: a second guard reports and exits rather
// than doubling every scan and racing the first for the safety lock.
func TestRunRefusesASecondGuardOnTheSameVault(t *testing.T) {
	root := newVault(t, "5m")
	initGit(t, root)

	release, acquired, err := vault.TryLockPath(filepath.Join(root, guardLockRelPath))
	if err != nil {
		t.Fatalf("TryLockPath: %v", err)
	}
	if !acquired {
		t.Fatal("the lock was already held in a fresh vault")
	}
	defer release()

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "another guard") {
		t.Fatalf("stderr does not explain the refusal: %q", stderr.String())
	}
}

// --- Restarting across a switch to off ------------------------------------------

// The unit tests in internal/vaultgit drive the watcher through an injected
// policy source, which can hand it any policy at any moment. That is the wrong
// shape for the case these two cover, because the case is about what startup
// itself decides: the guard reads brainnotes.toml *before* it opens the engine,
// so a file that already says `off` makes the engine start disabled, and from
// there no reload can tell an interrupted transition from a finished one. These
// run the real startup path against a real file to prove which of the two the
// vault gets.
