package vaultrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultimport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

const applyCrashExitCode = 7

// TestReviewApplyCrashHelper is the child process: it accepts the plan the
// parent wrote and dies at the named point. The parent's environment is
// inherited, so the child runs under the same isolated HOME and Git
// configuration and never needs a fixture of its own.
func TestReviewApplyCrashHelper(t *testing.T) {
	if os.Getenv("BN_REVIEW_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestReviewAcceptanceResumesAfterEveryInterruption")
	}
	plan, err := ReadPlan(os.Getenv("BN_REVIEW_CRASH_PLAN"))
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	point := os.Getenv("BN_REVIEW_CRASH_POINT")
	applyCrashHook = func(fired string) {
		if fired == point {
			os.Exit(applyCrashExitCode)
		}
	}
	// Reaching the end means the point never fired, which the parent sees as the
	// wrong exit code.
	if err := ApplyPlan(os.Getenv("BN_REVIEW_CRASH_ROOT"), plan, nil); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
}

// TestReviewAcceptanceResumesAfterEveryInterruption kills a real acceptance
// between each pair of durable steps and requires the retry to finish it, and a
// second retry to change nothing at all.
//
// Two of the cases also change the disk while the acceptance is down, the way
// the world does: the transport recreates the incoming channel the archive just
// moved away, which is what turned an interrupted archive into "both active and
// archived causal state exist" and an acceptance no retry could finish.
func TestReviewAcceptanceResumesAfterEveryInterruption(t *testing.T) {
	cases := []struct {
		point     string
		meanwhile func(t *testing.T, root string)
	}{
		{point: crashArchivedPrefix + "outgoing"},
		{point: crashArchivedPrefix + "incoming", meanwhile: redeliverIncoming},
		{point: crashArchivedPrefix + "import.json", meanwhile: redeliverIncoming},
		{point: crashEnrolled},
		{point: crashRebaselined},
		{point: crashCompleted},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.point, "/", "-"), func(t *testing.T) {
			r := newReviewPair(t)
			root := r.pair.Local.Root
			local := r.prepare(t, root)
			plan, _ := reviewPlans(t, r, local, mustInventory(t, r.pair.Peer.Root))
			interruptApply(t, root, plan, tc.point)
			if tc.meanwhile != nil {
				tc.meanwhile(t, root)
			}

			if err := ApplyPlan(root, plan, r.clock.Tick); err != nil {
				t.Fatalf("resume after %s: %v", tc.point, err)
			}
			requireAccepted(t, root, plan, local)
			settled := syncStateDigest(t, root)
			if err := ApplyPlan(root, plan, r.clock.Tick); err != nil {
				t.Fatalf("second resume after %s: %v", tc.point, err)
			}
			if again := syncStateDigest(t, root); again != settled {
				t.Fatalf("a second resume after %s changed the synchronization state", tc.point)
			}
		})
	}
}

// interruptApply writes the plan where the child can read it, runs the child to
// its death at point, and insists it died there.
func interruptApply(t *testing.T, root string, plan *Plan, point string) {
	t.Helper()
	planPath := filepath.Join(t.TempDir(), "plan.json")
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("encode plan: %v", err)
	}
	if err := os.WriteFile(planPath, data, 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReviewApplyCrashHelper$", "-test.timeout=300s")
	cmd.Env = append(os.Environ(),
		"BN_REVIEW_CRASH_HELPER=1",
		"BN_REVIEW_CRASH_POINT="+point,
		"BN_REVIEW_CRASH_PLAN="+planPath,
		"BN_REVIEW_CRASH_ROOT="+root,
	)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != applyCrashExitCode {
		t.Fatalf("helper interrupted at %q exited with %v, want exit %d\n%s", point, err, applyCrashExitCode, output)
	}
}

// redeliverIncoming is the transport putting a package back into an incoming
// channel the archive had already moved away.
func redeliverIncoming(t *testing.T, root string) {
	t.Helper()
	incoming := vaultsync.IncomingDir(root)
	if _, err := os.Stat(incoming); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the incoming channel was not archived before the interruption: %v", err)
	}
	delivered := filepath.Join(incoming, "redelivered", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(delivered), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delivered, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// requireAccepted checks every durable outcome the acceptance owes.
func requireAccepted(t *testing.T, root string, plan *Plan, before *Inventory) {
	t.Helper()
	if review, err := vaultsync.LoadRestoreReview(root); err != nil || review != nil {
		t.Fatalf("the review marker survived acceptance: %+v, %v", review, err)
	}
	enrollment := mustEnrollment(t, root)
	if enrollment.DeviceID != before.EffectiveDeviceID || enrollment.Epoch != plan.TargetEpoch {
		t.Fatalf("enrollment = %s at %d, want %s at %d", enrollment.DeviceID, enrollment.Epoch, before.EffectiveDeviceID, plan.TargetEpoch)
	}
	if _, err := os.Stat(completionPath(root, plan.SHA256)); err != nil {
		t.Fatalf("no completion record: %v", err)
	}
	requireFreshLedgers(t, root, plan)
}

// requireFreshLedgers checks both causal ledgers were archived and restarted.
func requireFreshLedgers(t *testing.T, root string, plan *Plan) {
	t.Helper()
	exportState, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatalf("load producer ledger: %v", err)
	}
	if exportState.Sequence != 0 || exportState.Commit != "" || len(exportState.Imported) != 0 {
		t.Fatalf("the restored identity did not start a fresh stream: %+v", exportState)
	}
	importState, err := vaultimport.LoadState(root)
	if err != nil {
		t.Fatalf("load consumer ledger: %v", err)
	}
	if len(importState.Applied) != 0 || importState.Pending != nil {
		t.Fatalf("the consumer ledger carried state across review: %+v", importState)
	}
	archived := filepath.Join(archiveDir(root, plan.SHA256), "export.json")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("the reviewed producer ledger was not archived: %v", err)
	}
}

// syncStateDigest hashes every regular file under the synchronization state
// directory except the two guards, whose files exist only to be locked.
func syncStateDigest(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(vaultsync.StateDir()))
	var lines []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if vaultsync.IsStateLock(filepath.ToSlash(rel)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		lines = append(lines, fmt.Sprintf("%s %s", filepath.ToSlash(rel), hex.EncodeToString(sum[:])))
		return nil
	})
	if err != nil {
		t.Fatalf("digest synchronization state: %v", err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
