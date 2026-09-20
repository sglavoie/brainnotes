package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultrecovery"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsyncd"
)

// The command's own surface: which mode a flag selects, what each one prints,
// and what it exits with. The work under all of them is internal/vaultsyncd's
// and is tested there; what is tested here is the part a LaunchAgent, a health
// script, and a person at a terminal actually see.

// TestCheckReportsAReadableVaultAndExitsZero.
func TestCheckReportsAReadableVaultAndExitsZero(t *testing.T) {
	enrolledPair(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check"}, stdout, stderr); code != exitOK {
		t.Fatalf("-check on a healthy vault exited %d: %s", code, stderr)
	}
	status := decodeStatus(t, stdout)
	if !status.Enrolled || len(status.States) == 0 {
		t.Fatalf("-check reported %+v", status)
	}
}

// TestCheckSeparatesNeedingAPersonFromBeingUnreadable is the distinction the
// exit statuses exist for. A vault waiting on a person is not broken, and a
// vault nobody can read is not healthy, so they cannot share a status.
func TestCheckSeparatesNeedingAPersonFromBeingUnreadable(t *testing.T) {
	pair := enrolledPair(t)
	blockHistory(t, pair.Local.Root)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check"}, stdout, stderr); code != exitNeedsPerson {
		t.Fatalf("-check on a blocked vault exited %d, want %d: %s", code, exitNeedsPerson, stderr)
	}
	// It still printed the status. A health check that says "something is wrong"
	// and nothing else is one a person cannot act on.
	if status := decodeStatus(t, stdout); !status.Local.Blocked {
		t.Fatalf("-check did not report the block: %+v", status.Local)
	}

	// Somewhere that is not a vault at all.
	t.Setenv("BNOTES_VAULT", t.TempDir())
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("-check outside a vault exited %d, want %d", code, exitUnreadable)
	}
	if stderr.Len() == 0 {
		t.Fatal("-check refused silently")
	}
}

// TestCheckOnAnUnenrolledVaultIsHealthy. Almost every vault is this one and none
// of them is a fault, so a health check that flagged them would be a health check
// nobody leaves installed.
func TestCheckOnAnUnenrolledVaultIsHealthy(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "local"})
	v.Use(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check"}, stdout, stderr); code != exitOK {
		t.Fatalf("-check on an unenrolled vault exited %d: %s", code, stderr)
	}
	status := decodeStatus(t, stdout)
	if status.Enrolled || status.Headline() != vaultsyncd.StateUnenrolled {
		t.Fatalf("-check reported %+v", status)
	}
}

// TestOnceRunsOneBoundedPassAndPrintsWhatItDid.
func TestOnceRunsOneBoundedPassAndPrintsWhatItDid(t *testing.T) {
	pair := enrolledPair(t)
	commit(t, pair.Local, "Sync/Local.md", "written on the mac\n")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-once"}, stdout, stderr); code != exitOK {
		t.Fatalf("-once exited %d: %s", code, stderr)
	}
	var pass vaultsyncd.Pass
	if err := json.Unmarshal(stdout.Bytes(), &pass); err != nil {
		t.Fatalf("decode the pass: %v\n%s", err, stdout)
	}
	if !pass.Enrolled || len(pass.Units) == 0 || pass.Failed() {
		t.Fatalf("-once reported %+v", pass)
	}
	if entries, err := os.ReadDir(vaultsync.OutgoingDir(pair.Local.Root)); err != nil || len(entries) == 0 {
		t.Fatalf("-once published nothing: %v %v", entries, err)
	}
}

// TestOnceRefusesWhileACompanionHoldsTheVault. One-shot mode is what an operator
// runs on a machine where the companion is not installed; on one where it is, it
// must refuse rather than race the loop.
func TestOnceRefusesWhileACompanionHoldsTheVault(t *testing.T) {
	pair := enrolledPair(t)
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(pair.Local.Root))
	if err != nil || !acquired {
		t.Fatalf("take the companion's lock: acquired=%v err=%v", acquired, err)
	}
	defer release()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-once"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("-once against a held vault exited %d, want %d", code, exitUnreadable)
	}
	if stdout.Len() != 0 {
		t.Fatalf("-once printed a report for a pass that never ran: %s", stdout)
	}
	if stderr.Len() == 0 {
		t.Fatal("-once refused silently")
	}
}

func TestRestoreReviewModesQuarantineCompareAndAccept(t *testing.T) {
	pair := enrolledPair(t)
	restored := prepareRestoreCLI(t)
	assertCheckNeedsReview(t)
	peer, err := vaultrecovery.BuildInventory(pair.Peer.Root)
	if err != nil {
		t.Fatalf("peer inventory: %v", err)
	}
	plan := reviewCLI(t, writeJSONFixture(t, "peer.json", peer))
	if len(plan.Differences) != 0 || plan.TargetEpoch <= restored.Epoch {
		t.Fatalf("review plan = %+v", plan)
	}
	accepted := acceptReviewCLI(t, writeJSONFixture(t, "review.json", plan))
	if accepted.Restored || accepted.DeviceID != restored.EffectiveDeviceID || accepted.Epoch != plan.TargetEpoch {
		t.Fatalf("accepted inventory = %+v", accepted)
	}
}

func prepareRestoreCLI(t *testing.T) vaultrecovery.Inventory {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-restore-prepare"}, stdout, stderr); code != exitOK {
		t.Fatalf("-restore-prepare exited %d: %s", code, stderr)
	}
	var restored vaultrecovery.Inventory
	if err := json.Unmarshal(stdout.Bytes(), &restored); err != nil {
		t.Fatalf("decode restored inventory: %v", err)
	}
	if !restored.Restored || restored.DeviceID == restored.EffectiveDeviceID {
		t.Fatalf("restored inventory = %+v", restored)
	}
	return restored
}

func TestRestorePrepareRefusesWhileCompanionHoldsTheVault(t *testing.T) {
	pair := enrolledPair(t)
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(pair.Local.Root))
	if err != nil || !acquired {
		t.Fatalf("take companion lock: acquired=%v err=%v", acquired, err)
	}
	defer release()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	args := []string{"-restore-prepare"}
	if code := run(context.Background(), args, stdout, stderr); code != exitNeedsPerson {
		t.Fatalf("-restore-prepare against a held vault exited %d: %s", code, stderr)
	}
	if review, err := vaultsync.LoadRestoreReview(pair.Local.Root); err != nil || review != nil {
		t.Fatalf("refused preparation wrote marker %+v: %v", review, err)
	}
}

func assertCheckNeedsReview(t *testing.T) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check"}, stdout, stderr); code != exitNeedsPerson {
		t.Fatalf("-check during review exited %d, want %d: %s", code, exitNeedsPerson, stderr)
	}
}

func writeJSONFixture(t *testing.T, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func reviewCLI(t *testing.T, peerPath string) vaultrecovery.Plan {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-review", peerPath}, stdout, stderr); code != exitOK {
		t.Fatalf("-review exited %d: %s", code, stderr)
	}
	var plan vaultrecovery.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return plan
}

func acceptReviewCLI(t *testing.T, planPath string) vaultrecovery.Inventory {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-accept-review", planPath}, stdout, stderr); code != exitOK {
		t.Fatalf("-accept-review exited %d: %s", code, stderr)
	}
	var accepted vaultrecovery.Inventory
	if err := json.Unmarshal(stdout.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted inventory: %v", err)
	}
	return accepted
}

// TestTheModesAreNotAskedForTogether: they answer different questions, and
// guessing which one was meant is worse than saying so.
func TestTheModesAreNotAskedForTogether(t *testing.T) {
	vaultfixture.ScrubEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-check", "-once"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("both modes together exited %d, want %d", code, exitUnreadable)
	}
	if stderr.Len() == 0 {
		t.Fatal("the refusal explained nothing")
	}
}

func TestRestorePrepareRefusesCallerSuppliedIdentity(t *testing.T) {
	vaultfixture.ScrubEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-restore-prepare", "caller-token"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("positional restore identity exited %d: %s", code, stderr)
	}
}

// TestAskingForTheUsageIsAnswered. `-h` is a question this command can answer,
// and the flag set has already answered it by the time the parse reports it —
// so it is not the "no answer could be produced" status, which would put a 2 in
// a health script's log for somebody who asked how to run this.
func TestAskingForTheUsageIsAnswered(t *testing.T) {
	vaultfixture.ScrubEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-h"}, stdout, stderr); code != exitOK {
		t.Fatalf("-h exited %d, want %d: %s", code, exitOK, stderr)
	}
	if stderr.Len() == 0 {
		t.Fatal("-h printed no usage")
	}
}

// TestAnUnknownFlagIsRefusedBeforeAnythingIsResolved.
func TestAnUnknownFlagIsRefusedBeforeAnythingIsResolved(t *testing.T) {
	vaultfixture.ScrubEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-forever"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("an unknown flag exited %d, want %d", code, exitUnreadable)
	}
}

// enrolledPair builds two disposable replicas, enrolls them with each other
// through the flow a person runs, and points the process at the local one.
func enrolledPair(t *testing.T) *vaultfixture.Pair {
	t.Helper()
	pair := vaultfixture.NewPair(t, nil)
	clock := vaultfixture.NewClock(t)

	local, err := vaultsync.Establish(pair.Local.Root, vaultsync.Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	clock.Tick()
	peer, err := vaultsync.Join(pair.Peer.Root, local.Invite(), vaultsync.Options{Label: "pi", Now: clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if _, err := vaultsync.RecordPeer(pair.Local.Root, vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label}); err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	pair.Local.Use(t)
	return pair
}

// commit writes a note and commits it the way the content history would have.
func commit(t *testing.T, v *vaultfixture.Vault, rel, body string) {
	t.Helper()
	v.Write(t, rel, body)
	v.Git(t, "add", "-A", "--", ".", ":(exclude).bnotes/sync")
	v.Git(t, "commit", "-q", "-m", "edit "+rel)
}

// blockHistory latches internal/vaultgit's fail-closed marker the way a vault
// reaches it in life: a tracked note vanishes with nothing authorizing it.
func blockHistory(t *testing.T, root string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "Alpha.md")); err != nil {
		t.Fatalf("remove a tracked note: %v", err)
	}
	manager, err := vaultgit.Open(context.Background(), root, vaultgit.Options{})
	if err != nil {
		t.Fatalf("open the content history: %v", err)
	}
	if _, err := manager.CommitPending(context.Background()); err == nil {
		t.Fatal("an unexplained deletion was committed")
	}
}

func decodeStatus(t *testing.T, out *bytes.Buffer) *vaultsyncd.Status {
	t.Helper()
	status := &vaultsyncd.Status{}
	if err := json.Unmarshal(out.Bytes(), status); err != nil {
		t.Fatalf("decode the status: %v\n%s", err, out)
	}
	return status
}
