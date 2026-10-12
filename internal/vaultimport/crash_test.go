package vaultimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// An import is six durable events with five gaps between them, and the only
// honest way to find out what a gap leaves behind is to stop a real process in
// one. os.Exit inside the crash hook skips every deferred cleanup — the import
// lock's release, the transaction's intent clearing, the fixture's own teardown —
// exactly as a kill does, so the parent inspects the same disk a restart would
// see.
//
// The package the helper applies carries all three kinds of change at once. They
// go through three different primitives and each leaves its own half-finished
// state: a create has nothing to restore *to*, an overwrite has a snapshot behind
// it, and a delete is the one whose rollback puts content back that a commit may
// already have removed.
const (
	importCrashExitCode = 7
	crashCreatedPath    = "Notes/Delta.md"
	crashModifiedPath   = "Alpha.md"
	crashDeletedPath    = "Notes/Gamma.md"
	crashCreatedBody    = "---\nstatus: draft\ntitle: Delta\n---\n\nDelta body, created on the peer.\n"
	crashModifiedBody   = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, rewritten on the peer.\n"
	crashMissing        = "<missing>"
)

// crashPaths are the package's paths, in the order a state map is compared in.
var crashPaths = []string{crashCreatedPath, crashModifiedPath, crashDeletedPath}

// crashReport is what the child writes down before it starts applying, because
// after it dies nobody can ask it anything. It names the vault, the package, and
// the scratch tree the parent has to delete — the fixture's own cleanup is one of
// the deferred things os.Exit skips.
type crashReport struct {
	Root       string `json:"root"`
	Scratch    string `json:"scratch"`
	PackageDir string `json:"package_dir"`
	PackageID  string `json:"package_id"`
}

// TestImportCrashHelper is the child process. It builds a two-replica world,
// delivers one package, and terminates at the named durable state transition.
func TestImportCrashHelper(t *testing.T) {
	if os.Getenv("BN_IMPORT_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestImportRecoversFromEveryDurableTransition")
	}
	point := os.Getenv("BN_IMPORT_CRASH_POINT")

	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write(crashCreatedPath, []byte(crashCreatedBody), vaultsync.Absent())
		b.Write(crashModifiedPath, []byte(crashModifiedBody), r.believes(t, crashModifiedPath))
		b.Delete(crashDeletedPath, r.believes(t, crashDeletedPath))
	})
	// Written before the hook is armed, so it exists whichever point fires.
	report := crashReport{
		Root:       r.root(),
		Scratch:    filepath.Dir(r.pair.Dir),
		PackageDir: dir,
		PackageID:  manifest.PackageID,
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	if err := os.WriteFile(os.Getenv("BN_IMPORT_CRASH_REPORT"), data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	importCrashHook = func(fired string) {
		if fired == point {
			os.Exit(importCrashExitCode)
		}
	}
	// Reaching here means the point never fired, which the parent sees as a wrong
	// exit code. Failing loudly rather than silently proving nothing.
	if _, err := r.apply(t, dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// TestImportRecoveryCrashHelper is the second child process. It resolves a vault
// another one already died in the middle of, and dies partway through resolving
// it — because a recovery pass is itself a sequence of durable steps, and the
// power does not wait for it to finish either.
func TestImportRecoveryCrashHelper(t *testing.T) {
	if os.Getenv("BN_IMPORT_RECOVERY_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestAnInterruptedRollbackFinishesWithoutStranding")
	}
	vaultfixture.Isolate(t)
	point := os.Getenv("BN_IMPORT_CRASH_POINT")
	importCrashHook = func(fired string) {
		if fired == point {
			os.Exit(importCrashExitCode)
		}
	}
	if _, err := Recover(os.Getenv("BN_IMPORT_CRASH_ROOT"), Options{}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
}

// interrupt runs the helper to its death at point and returns what it left.
func interrupt(t *testing.T, point string) crashReport {
	t.Helper()
	reportPath := filepath.Join(t.TempDir(), "report.json")
	output, err := runHelper(t, "TestImportCrashHelper",
		"BN_IMPORT_CRASH_HELPER=1",
		"BN_IMPORT_CRASH_POINT="+point,
		"BN_IMPORT_CRASH_REPORT="+reportPath,
	)
	if err != nil {
		t.Fatalf("helper interrupted at %q: %v\n%s", point, err, output)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("the helper died before it said where it was working: %v\n%s", err, output)
	}
	var report crashReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	t.Cleanup(func() { discardScratch(t, report.Scratch) })
	return report
}

// interruptRecovery kills a recovery pass over the vault at root, at point.
func interruptRecovery(t *testing.T, root, point string) {
	t.Helper()
	output, err := runHelper(t, "TestImportRecoveryCrashHelper",
		"BN_IMPORT_RECOVERY_HELPER=1",
		"BN_IMPORT_CRASH_POINT="+point,
		"BN_IMPORT_CRASH_ROOT="+root,
	)
	if err != nil {
		t.Fatalf("recovery interrupted at %q: %v\n%s", point, err, output)
	}
}

// runHelper runs one of this file's subprocess helpers and insists it died where
// it was told to.
//
// The exit code is the assertion that matters: a helper that ran to completion
// exits 0, so a kill point that quietly stopped being reached fails the test
// instead of leaving it to prove nothing about an uninterrupted vault.
func runHelper(t *testing.T, name string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.timeout=300s")
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != importCrashExitCode {
		return string(output), fmt.Errorf("%s exited with %v, want exit %d", name, err, importCrashExitCode)
	}
	return string(output), nil
}

// discardScratch deletes the tree the dead child left behind, having first
// checked it is one — a RemoveAll driven by a path out of a file gets the same
// containment check every other fixture path in this repository gets.
func discardScratch(t *testing.T, scratch string) {
	t.Helper()
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("resolve temp directory: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		return
	}
	if resolved == temp || !strings.HasPrefix(resolved, temp+string(filepath.Separator)) {
		t.Fatalf("the helper reported a scratch tree outside %s: %s", temp, resolved)
	}
	if err := os.RemoveAll(resolved); err != nil {
		t.Fatalf("remove scratch tree: %v", err)
	}
}

// crashBefore and crashAfter are the only two states the vault is allowed to
// settle into. Anything between them is a package half applied.
func crashBefore() map[string]string {
	seed := vaultfixture.DefaultFiles()
	return map[string]string{
		crashCreatedPath:  crashMissing,
		crashModifiedPath: seed[crashModifiedPath],
		crashDeletedPath:  seed[crashDeletedPath],
	}
}

func crashAfter() map[string]string {
	return map[string]string{
		crashCreatedPath:  crashCreatedBody,
		crashModifiedPath: crashModifiedBody,
		crashDeletedPath:  crashMissing,
	}
}

// crashState reads the package's three paths, using a sentinel for absence.
func crashState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := make(map[string]string, len(crashPaths))
	for _, rel := range crashPaths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case err == nil:
			state[rel] = string(data)
		case os.IsNotExist(err):
			state[rel] = crashMissing
		default:
			t.Fatalf("read %s: %v", rel, err)
		}
	}
	return state
}

func requireState(t *testing.T, root string, want map[string]string, context string) {
	t.Helper()
	got := crashState(t, root)
	for rel, body := range want {
		if got[rel] != body {
			t.Fatalf("%s: %s holds %q, want %q", context, rel, got[rel], body)
		}
	}
}

// The parent inspects a vault it did not build, so it reaches for git directly
// rather than through vaultfixture.Vault, whose handle died with the child.
func crashGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func crashCommitCount(t *testing.T, root string) int {
	t.Helper()
	count, err := strconv.Atoi(strings.TrimSpace(crashGit(t, root, "rev-list", "--count", "HEAD")))
	if err != nil {
		t.Fatalf("parse commit count: %v", err)
	}
	return count
}

// crashDirty is dirtyInScope for a vault the parent has only a path to. Same
// question, same policy: `.bnotes/sync/` and `.bnotes/locks/` are untracked by
// design and are not evidence of anything.
func crashDirty(t *testing.T, root string) []string {
	t.Helper()
	kept := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(crashGit(t, root, "status", "--porcelain")), "\n") {
		if len(line) < 4 {
			continue
		}
		if vaultgit.InScope(strings.Trim(line[3:], `"`)) {
			kept = append(kept, line)
		}
	}
	sort.Strings(kept)
	return kept
}

// crashEntries lists the undo entries a vault the parent did not build holds.
func crashEntries(t *testing.T, root string) []journal.Entry {
	t.Helper()
	entries, _, err := journal.ListEntries(noteops.JournalRoot(root))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	return entries
}

// entryDirExists asks the disk rather than the journal, which is the whole point
// of it: an entry directory with no manifest is not a recovery entry and
// journal.ListEntries will not report it, so a rollback that restored the notes
// and left the snapshots behind looks identical to a clean one from above.
func entryDirExists(t *testing.T, root, entryID string) bool {
	t.Helper()
	if entryID == "" {
		t.Fatal("the attempt record names no journal entry")
	}
	_, err := os.Stat(filepath.Join(noteops.JournalRoot(root), entryID))
	switch {
	case err == nil:
		return true
	case os.IsNotExist(err):
		return false
	default:
		t.Fatalf("stat entry directory: %v", err)
		return false
	}
}

// publishedReceipt returns the receipt in the vault's outgoing channel, or nil.
// A receipt is the one thing in this protocol a peer acts on by discarding its
// own copy of a change, so "is one there" is the premature-acknowledgment
// question stated directly.
func publishedReceipt(t *testing.T, root, packageID string) *vaultsync.Receipt {
	t.Helper()
	receipt, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(root), packageID)
	if errors.Is(err, vaultsync.ErrIncompletePackage) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadReceipt: %v", err)
	}
	return receipt
}
