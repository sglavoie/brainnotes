package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A checkpoint and a writer are both short sequences of durable transitions, and
// the only honest way to find out what an interruption leaves behind is to stop
// a real process at one of them. The helpers below are those processes: they
// call os.Exit, which skips every deferred release exactly as a kill does, so
// the parent inspects the same disk a restart would see — including the one
// thing no in-process test can reproduce, the kernel dropping an flock because
// the process holding it is gone.
const (
	clientCrashExit = 11
	writerCrashExit = 12

	envClientCrash = "BN_CKPT_CLIENT_CRASH"
	envWriter      = "BN_CKPT_WRITER"
	envRoot        = "BN_CKPT_ROOT"
	envPoint       = "BN_CKPT_POINT"
	envReady       = "BN_CKPT_READY"
	envRelease     = "BN_CKPT_RELEASE"
	envMode        = "BN_CKPT_MODE"
	envExecParent  = "BN_CKPT_EXEC_PARENT"
	envExecChild   = "BN_CKPT_EXEC_CHILD"

	writerDies     = "die"
	writerFinishes = "finish"
)

// TestCheckpointExecParentHelper models `bn checkpoint run`: it holds the
// checkpoint, passes both lock descriptors to a child, then dies without
// releasing. The child is deliberately independent of its parent.
func TestCheckpointExecParentHelper(t *testing.T) {
	if os.Getenv(envExecParent) != "1" {
		t.Skip("subprocess helper")
	}
	held, err := Hold(os.Getenv(envRoot), Options{Reason: "exec inheritance", Drain: 10 * time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	child := helper(t, "TestCheckpointExecChildHelper", envExecChild+"=1", envRelease+"="+os.Getenv(envRelease))
	child.ExtraFiles = held.InheritanceFiles()
	if err := child.Start(); err != nil {
		t.Fatalf("start inherited child: %v", err)
	}
	if err := os.WriteFile(os.Getenv(envReady), []byte(fmt.Sprintf("%d", child.Process.Pid)), 0o644); err != nil {
		t.Fatalf("write child pid: %v", err)
	}
	os.Exit(clientCrashExit)
}

func TestCheckpointExecChildHelper(t *testing.T) {
	if os.Getenv(envExecChild) != "1" {
		t.Skip("subprocess helper")
	}
	if !waitForFile(os.Getenv(envRelease), helperBudget) {
		t.Fatalf("the parent never released inherited child")
	}
}

// helperPoll paces a helper's wait for the parent's signal file. The parent and
// the child share no channel, so a file is the whole protocol.
const helperPoll = 5 * time.Millisecond

// helperBudget bounds every wait a helper does, so a parent that failed before
// signalling leaves a child that exits rather than one that runs until the test
// binary's own timeout.
const helperBudget = 60 * time.Second

// TestCheckpointClientCrashHelper is the child process for the checkpoint half.
// It takes a checkpoint and terminates at the named durable transition.
func TestCheckpointClientCrashHelper(t *testing.T) {
	if os.Getenv(envClientCrash) != "1" {
		t.Skip("subprocess helper; driven by TestACheckpointClientDeathLeavesTheVaultWritable")
	}
	point := os.Getenv(envPoint)
	checkpointCrashHook = func(fired string) {
		if fired == point {
			os.Exit(clientCrashExit)
		}
	}
	held, err := Hold(os.Getenv(envRoot), Options{Reason: "a client about to die", Drain: 10 * time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestCheckpointWriterHelper is the child process for the writer half. It joins
// the barrier, says so, and then either dies holding the join or waits for the
// parent's signal and releases it properly.
func TestCheckpointWriterHelper(t *testing.T) {
	if os.Getenv(envWriter) != "1" {
		t.Skip("subprocess helper; driven by the barrier's drain tests")
	}
	release, err := JoinWithin(os.Getenv(envRoot), helperBudget)
	if err != nil {
		t.Fatalf("JoinWithin: %v", err)
	}
	if err := os.WriteFile(os.Getenv(envReady), nil, 0o644); err != nil {
		t.Fatalf("announce readiness: %v", err)
	}
	if os.Getenv(envMode) == writerDies {
		os.Exit(writerCrashExit)
	}
	if !waitForFile(os.Getenv(envRelease), helperBudget) {
		t.Fatalf("the parent never signalled %q", os.Getenv(envRelease))
	}
	release()
}

// waitForFile polls until path exists or the budget runs out.
func waitForFile(path string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return true
		}
		time.Sleep(helperPoll)
	}
	return false
}

// helper builds a child invocation of one of the two helpers above.
func helper(t *testing.T, name string, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// runToDeath runs a helper expected to terminate at a crash point and asserts it
// died the way the test arranged rather than failing for some other reason.
func runToDeath(t *testing.T, cmd *exec.Cmd, want int) {
	t.Helper()
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != want {
		t.Fatalf("helper = %v, want exit %d\n%s", err, want, output)
	}
}

// A checkpoint client that dies is the failure the whole liveness half of this
// package exists for: the record it leaves says the vault is being copied and
// nothing on disk can contradict it. The claim lock can, because the kernel
// drops it, and that is what every writer asks.
//
// Every transition must leave a vault writers can use. A checkpoint's whole
// failure mode is refusing writes it will never stop refusing.
func TestACheckpointClientDeathLeavesTheVaultWritable(t *testing.T) {
	cases := []struct {
		point      string
		announced  bool
		whatItLeft string
	}{
		{crashAnnounced, true, "an announcement and no exclusive hold"},
		{crashDrained, true, "an announcement saying draining while the barrier was held"},
		{crashHeld, true, "a complete announcement and an exclusive hold"},
		{crashReleased, false, "nothing but the claim lock"},
	}

	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			root := newBarrierVault(t)
			runToDeath(t, helper(t, "TestCheckpointClientCrashHelper",
				envClientCrash+"=1", envRoot+"="+root, envPoint+"="+tc.point), clientCrashExit)

			if got := recordExists(t, root); got != tc.announced {
				t.Fatalf("record on disk after dying at %q = %v, want %v (%s)",
					tc.point, got, tc.announced, tc.whatItLeft)
			}
			state := mustStatus(t, root)
			if state.Live || state.Blocking {
				t.Fatalf("status after the client died = %+v, want nothing blocking", state)
			}

			// A writer proceeds, and proceeds without waiting: a dead client's
			// leavings must not cost every later command the writer budget.
			start := time.Now()
			mustJoin(t, root)()
			if waited := time.Since(start); waited > shortWait {
				t.Fatalf("a writer waited %s on a dead client's leavings", waited)
			}

			// And the next checkpoint works, which is the property a vault that
			// is backed up nightly actually depends on.
			held := mustHold(t, root, "the checkpoint after the death")
			if held.ID() == "" {
				t.Fatal("the recovering checkpoint has no identity")
			}
			if err := held.Release(); err != nil {
				t.Fatalf("Release: %v", err)
			}
			if recordExists(t, root) {
				t.Fatal("the recovering checkpoint left its own announcement behind")
			}
		})
	}
}

func TestCheckpointClientDeathKeepsManagedVaultStatusClean(t *testing.T) {
	root := newBarrierVault(t)
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	runToDeath(t, helper(t, "TestCheckpointClientCrashHelper",
		envClientCrash+"=1", envRoot+"="+root, envPoint+"="+crashAnnounced), clientCrashExit)
	output, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("killed checkpoint dirtied managed vault: %q", output)
	}
}

func TestExecChildKeepsCheckpointAfterWrapperDeath(t *testing.T) {
	root := newBarrierVault(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	releaseChild := filepath.Join(dir, "release")
	runToDeath(t, helper(t, "TestCheckpointExecParentHelper",
		envExecParent+"=1", envRoot+"="+root, envReady+"="+ready, envRelease+"="+releaseChild), clientCrashExit)

	release, err := JoinWithin(root, shortWait)
	if !errors.Is(err, ErrBarrierHeld) {
		t.Fatalf("JoinWithin while inherited child lives = %v, want ErrBarrierHeld", err)
	}
	release()
	if err := os.WriteFile(releaseChild, nil, 0o644); err != nil {
		t.Fatalf("release inherited child: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		release, err = JoinWithin(root, shortWait)
		if err == nil {
			release()
			break
		}
		if !errors.Is(err, ErrBarrierHeld) || !time.Now().Before(deadline) {
			t.Fatalf("JoinWithin after inherited child exit = %v", err)
		}
	}
}

// startWriter launches a writer helper and waits until it holds the join, so the
// test that follows is racing a writer that is genuinely inside the barrier
// rather than one still starting up.
func startWriter(t *testing.T, root, mode string) (cmd *exec.Cmd, releaseFile string, done chan error) {
	t.Helper()
	ready := t.TempDir()
	readyFile := ready + "/ready"
	releaseFile = ready + "/release"

	cmd = helper(t, "TestCheckpointWriterHelper",
		envWriter+"=1", envRoot+"="+root, envReady+"="+readyFile,
		envRelease+"="+releaseFile, envMode+"="+mode)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the writer: %v", err)
	}
	done = make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if !waitForFile(readyFile, 30*time.Second) {
		_ = cmd.Process.Kill()
		t.Fatal("the writer never reported that it had joined the barrier")
	}
	return cmd, releaseFile, done
}

// signal tells a waiting writer to finish.
func signal(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("signal the writer: %v", err)
	}
}

// A writer that dies mid-write holds the shared barrier at the instant it dies.
// Nothing in this package releases it and nothing has to: the kernel drops an
// flock when its holder goes, however it went, which is why the barrier is a
// flock and not a file somebody writes "held" into.
func TestAWriterDeathDoesNotStrandTheBarrier(t *testing.T) {
	root := newBarrierVault(t)
	cmd, _, done := startWriter(t, root, writerDies)

	var exit *exec.ExitError
	if err := <-done; !errors.As(err, &exit) || exit.ExitCode() != writerCrashExit {
		t.Fatalf("writer = %v, want exit %d", err, writerCrashExit)
	}
	_ = cmd

	held := mustHold(t, root, "after the writer died holding its join")
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// A write already running when a checkpoint announces is waited for, not cut
// off. That is the whole difference between a barrier and a lock somebody takes:
// the writer finishes and reports success, and the copy taken afterwards holds
// its work.
func TestHoldWaitsForAnInFlightWriterRatherThanCuttingItOff(t *testing.T) {
	root := newBarrierVault(t)
	_, releaseFile, done := startWriter(t, root, writerFinishes)

	type outcome struct {
		held *Held
		err  error
	}
	results := make(chan outcome, 1)
	go func() {
		held, err := Hold(root, Options{Reason: "waiting for a writer", Drain: 30 * time.Second})
		results <- outcome{held, err}
	}()

	select {
	case got := <-results:
		_ = got.held.Release()
		t.Fatalf("Hold returned while a writer still held the barrier: (%v, %v)", got.held, got.err)
	case <-time.After(shortWait):
	}

	// The vault says what is happening while it is happening, which is the only
	// thing a person looking at a stalled command has to go on.
	if state := mustStatus(t, root); state.Phase != PhaseDraining || !state.Blocking {
		t.Fatalf("status while draining = %+v, want phase %q and blocking", state, PhaseDraining)
	}

	signal(t, releaseFile)
	if err := <-done; err != nil {
		t.Fatalf("the writer did not finish cleanly: %v", err)
	}

	got := <-results
	if got.err != nil {
		t.Fatalf("Hold after the writer finished = %v", got.err)
	}
	if state := mustStatus(t, root); state.Phase != PhaseHeld {
		t.Fatalf("status after draining = %+v, want phase %q", state, PhaseHeld)
	}
	if err := got.held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// A drain budget that runs out is the vault being busier than the caller was
// willing to wait for. Nothing is captured, nothing is disturbed, and — the part
// that matters — the writer it was waiting for is not stranded: it finishes and
// reports success, and new writers are admitted immediately.
func TestADrainThatTimesOutStrandsNobody(t *testing.T) {
	root := newBarrierVault(t)
	_, releaseFile, done := startWriter(t, root, writerFinishes)

	held, err := Hold(root, Options{Reason: "impatient", Drain: shortWait})
	if !errors.Is(err, ErrDrainTimeout) {
		_ = held.Release()
		t.Fatalf("Hold against a long writer = (%v, %v), want ErrDrainTimeout", held, err)
	}
	if recordExists(t, root) {
		t.Fatal("a timed-out Hold left its announcement behind")
	}

	// New writers are admitted again straight away. A refused checkpoint that
	// left the vault quieter than it found it would be a worse outcome than the
	// copy it failed to take.
	mustJoin(t, root)()

	signal(t, releaseFile)
	if err := <-done; err != nil {
		t.Fatalf("the writer the checkpoint gave up on did not finish cleanly: %v", err)
	}

	// And the vault is checkpointable once the writer is gone, so a caller that
	// simply tries again succeeds.
	second := mustHold(t, root, "the retry")
	if err := second.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
