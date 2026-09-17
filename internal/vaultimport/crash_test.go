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
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
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

// A process killed at any of the five gaps must converge on exactly one of two
// states: the import never happened, or it happened completely. The undo
// manifest is what decides which, and it is the only thing that can: it is what
// `bn undo` reads, what authorizes committing a deletion, and what the receipt
// promises the peer. Everything before it rolls back; everything after it rolls
// forward.
//
// Each case asserts the four failures this session exists to prevent. No
// duplicate mutation — the content is one of the two states and the vault holds
// one commit per completed import, never two. No premature acknowledgment — a
// receipt exists only where the change is durable recovery history. No missing
// history — a completed import has its entry and its commit. And convergence,
// checked twice, because a recovery pass runs before every import and a second
// one that changed anything would mean the first did not finish.
func TestImportRecoversFromEveryDurableTransition(t *testing.T) {
	cases := []killPoint{
		// The attempt is durable and the transaction has not been committed, so no
		// content moved, no entry directory exists, and there is nothing to undo.
		{point: crashDeclared, interrupted: crashBefore(), wantOutcome: OutcomeDiscarded, wantState: crashBefore()},
		// The peer's bytes are on the disk and the journal captured what they
		// replaced, with no manifest to say so. Not history yet, so it comes back out.
		{point: crashApplied, interrupted: crashAfter(), wantOutcome: OutcomeRolledBack, wantState: crashBefore(), entryDir: true},
		// Same, with the provenance ledger written. The ledger record outlives the
		// rollback and is harmless: it is keyed on a journal entry that no longer
		// exists and can never be committed, so the only reader — the exporter,
		// asking whether a commit it is about to publish came from a peer — never
		// looks it up. Removing it would mean a second durable write on the recovery
		// path to tidy something unreachable.
		{point: crashProvenance, interrupted: crashAfter(), wantOutcome: OutcomeRolledBack, wantState: crashBefore(), provenance: true, entryDir: true},
		// The manifest and the commit are both durable. The change is this vault's
		// own history now, and the peer has not been told.
		{point: crashFinalized, interrupted: crashAfter(), wantOutcome: OutcomeCompleted, wantState: crashAfter(), applied: true, provenance: true, entryDir: true},
		// The receipt is published too, and only the ledger is missing. The peer may
		// already have acted on that receipt, so recovery adopts it rather than
		// minting a second statement about the same package.
		{point: crashAcknowledged, interrupted: crashAfter(), wantOutcome: OutcomeCompleted, wantState: crashAfter(), applied: true, provenance: true, entryDir: true},
	}

	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			vaultfixture.Isolate(t)
			report := interrupt(t, tc.point)
			entry := requireInterrupted(t, report, tc)
			requireRecovered(t, report, tc, entry)
			requireConverged(t, report)
		})
	}
}

// A rollback is three durable steps of its own, and the middle of it is the one
// place where "nothing happened here" and "everything happened here and was put
// back" look the same from the disk.
//
// They look the same because a rollback's whole job is to make them the same: the
// content is back at what the package expected to find, which is exactly what a
// path that was never written holds. That costs nothing while the vault is
// undisturbed — a second pass re-derives an empty plan and finishes the tidying —
// and it costs everything the moment a person edits one of those notes first,
// because then the path holds neither state, which is the signature of an edit
// that must be refused rather than overwritten. The refusal is right for a
// rollback that has not happened and wrong for one that has, and it blocks every
// later import until somebody intervenes.
//
// So the rollback writes down that it finished, and this is the evidence that the
// record is load-bearing: the same vault, the same edit, resolved once with the
// record cleared and once with it intact.
func TestAnInterruptedRollbackFinishesWithoutStranding(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)
	interruptRecovery(t, report.Root, crashRestored)

	entry := requireRollbackRecorded(t, report.Root)

	// Somebody opens the vault and saves one of the restored notes.
	edited := "a person edited this after the rollback and before it was tidied up\n"
	writeNote(t, report.Root, crashModifiedPath, edited)

	requireStrandedWithoutTheRecord(t, report.Root, edited)
	requireRollbackResumed(t, report, entry, edited)
}

// requireRollbackRecorded checks what the second dead process left: the content
// restored, and every artifact that says the rollback is unfinished still
// standing. It returns the journal entry the attempt still names.
func requireRollbackRecorded(t *testing.T, root string) string {
	t.Helper()
	requireState(t, root, crashBefore(), "after an interrupted rollback")
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending == nil {
		t.Fatal("an interrupted rollback cleared the attempt it had not finished")
	}
	if state.Pending.Restored == nil {
		t.Fatal("a rollback that put three paths back left no record of having done so")
	}
	if got := len(state.Pending.Restored.Paths); got != len(crashPaths) {
		t.Fatalf("the rollback recorded %d restored paths, want %d", got, len(crashPaths))
	}
	if !entryDirExists(t, root, state.Pending.JournalEntry) {
		t.Fatal("an interrupted rollback removed the entry it restored from")
	}
	return state.Pending.JournalEntry
}

// requireRollbackResumed runs the pass that finds the rollback's own record and
// checks that it converges: rolled back, the newer edit untouched, and every
// artifact of the attempt gone.
func requireRollbackResumed(t *testing.T, report crashReport, entry, edited string) {
	t.Helper()
	recovery, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("resuming a finished rollback over a newer edit: %v", err)
	}
	if recovery.Outcome != OutcomeRolledBack {
		t.Fatalf("the resumed rollback reported %q, want %q", recovery.Outcome, OutcomeRolledBack)
	}
	requireState(t, report.Root, map[string]string{crashModifiedPath: edited}, "after the rollback was resumed")
	if entryDirExists(t, report.Root, entry) {
		t.Fatal("the resumed rollback left the entry directory behind")
	}
	// Not requireBooks: the edit is deliberately left in the working tree, so this
	// is the one recovered vault in this file that is legitimately dirty.
	requireHistory(t, report, Record{}, false)
	requireImports(t, report.Root, false)
	settled, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if settled.Pending != nil {
		t.Fatalf("the resumed rollback left attempt %q pending", settled.Pending.PackageID())
	}
	if _, remembered := settled.Applied[report.PackageID]; remembered {
		t.Fatal("a rolled-back package was remembered as applied")
	}
}

// requireStrandedWithoutTheRecord clears the finished rollback's record and shows
// what the next pass does without it: refuses, permanently, over a rollback that
// is already done. The record is put back afterwards so the caller can watch the
// same pass succeed with it.
func requireStrandedWithoutTheRecord(t *testing.T, root, edited string) {
	t.Helper()
	local, err := vaultsync.Load(root)
	if err != nil {
		t.Fatalf("vaultsync.Load: %v", err)
	}
	state, err := loadState(root, local)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	restored := state.Pending.Restored
	state.Pending.Restored = nil
	if err := saveState(root, state); err != nil {
		t.Fatalf("clear the rollback record: %v", err)
	}

	_, err = Recover(root, Options{})
	if !errors.Is(err, ErrStrandedImport) {
		t.Fatalf("a pass with nothing but the disk to go on resolved the edit as %v, want ErrStrandedImport", err)
	}
	requireState(t, root, map[string]string{crashModifiedPath: edited}, "after a stranded pass")

	state.Pending.Restored = restored
	if err := saveState(root, state); err != nil {
		t.Fatalf("restore the rollback record: %v", err)
	}
}

// TestARollbackRecordNamingAnUntouchedPathIsRefused is the other side of the
// record's cost.
//
// A record that is merely present is believed: the next pass skips the
// restoration, deletes the journal entry holding the only copy of the
// pre-change bytes, and settles the attempt. So a record this package's own
// rollback could not have written — naming a path the operation never changed —
// has to be refused rather than acted on, or a damaged state file would destroy
// the evidence for an application that is still on the disk.
func TestARollbackRecordNamingAnUntouchedPathIsRefused(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	local, err := vaultsync.Load(report.Root)
	if err != nil {
		t.Fatalf("vaultsync.Load: %v", err)
	}
	state, err := loadState(report.Root, local)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	entry := state.Pending.JournalEntry
	state.Pending.Restored = &Rollback{Paths: []string{"Nobody Touched This.md"}}

	// The writer refuses it too, which is why the file has to be forged by hand
	// to get a reader anywhere near it.
	if err := saveState(report.Root, state); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("writing a forged rollback record reported %v, want ErrCorruptState", err)
	}
	forgeState(t, report.Root, state)

	if _, err := Recover(report.Root, Options{}); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("a forged rollback record resolved as %v, want ErrCorruptState", err)
	}
	// Everything the forged record would have licensed destroying is still here.
	requireState(t, report.Root, crashAfter(), "after a forged rollback record was refused")
	if !entryDirExists(t, report.Root, entry) {
		t.Fatal("a refused rollback record still cost the journal entry its snapshots")
	}
}

// forgeState writes the record straight to the state file, past the validation
// saveState runs — the way a hand-edited or half-written file reaches a reader.
func forgeState(t *testing.T, root string, record *State) {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode forged import state: %v", err)
	}
	if err := os.WriteFile(StatePath(root), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write forged import state: %v", err)
	}
}

// writeNote edits a note in a vault the parent did not build, the way a person
// with an editor would.
func writeNote(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatalf("edit %s: %v", rel, err)
	}
}

// killPoint is one durable transition and the two states either side of it.
type killPoint struct {
	point string
	// interrupted is what the dead process left on the disk, asserted before
	// anything recovers it. Without it a rollback case would pass just as well
	// against a crash that never applied anything, and the test would be
	// evidence about nothing.
	interrupted map[string]string
	wantOutcome Outcome
	wantState   map[string]string
	// applied says the import became this vault's own history: a recovery
	// entry, a commit, a ledger record, and a receipt for the peer.
	applied bool
	// provenance says the anti-echo ledger already knew about the entry when
	// the process died. It survives a rollback deliberately — see the comment
	// on the rolled-back cases in the table.
	provenance bool
	// entryDir says the dead process left a journal entry directory behind.
	// Asserted before recovery, because a rollback that removed a directory
	// which was never there proves nothing, and listing entries cannot tell
	// the difference: journal.ListEntries skips a directory with no manifest,
	// so an un-finalized entry is invisible to a count of recovery history.
	entryDir bool
}

// requireInterrupted asserts what the dead process left and returns the entry
// the attempt names, which is the only way the parent can know it.
func requireInterrupted(t *testing.T, report crashReport, tc killPoint) string {
	t.Helper()
	// The attempt is what makes the interruption explicable at all.
	interrupted, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if interrupted.Pending.PackageID() != report.PackageID {
		t.Fatalf("the interrupted vault holds attempt %q, want %s",
			interrupted.Pending.PackageID(), report.PackageID)
	}
	requireState(t, report.Root, tc.interrupted, "as the dead process left it")
	entry := interrupted.Pending.JournalEntry
	if got := entryDirExists(t, report.Root, entry); got != tc.entryDir {
		t.Fatalf("the dead process left an entry directory = %v, want %v", got, tc.entryDir)
	}
	return entry
}

// requireRecovered runs the recovery pass and asserts every record it touched.
func requireRecovered(t *testing.T, report crashReport, tc killPoint, entry string) {
	t.Helper()
	recovery, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Outcome != tc.wantOutcome {
		t.Fatalf("recovery outcome = %q, want %q", recovery.Outcome, tc.wantOutcome)
	}
	requireState(t, report.Root, tc.wantState, "after recovery")
	// A rollback owes the disk more than a restored note: the entry directory and
	// its snapshots are the undo record for a change that no longer exists, and
	// leaving them is how `bn undo` ends up offering to revert something nobody
	// did.
	if got := entryDirExists(t, report.Root, entry); got != tc.applied {
		t.Fatalf("entry directory %s exists = %v after recovery, want %v", entry, got, tc.applied)
	}
	requireBooks(t, report, tc.applied, tc.provenance)
}

// requireConverged asserts that a second recovery pass finds nothing and changes
// nothing, which is what makes recovery safe to run ahead of every import.
func requireConverged(t *testing.T, report crashReport) {
	t.Helper()
	state := crashState(t, report.Root)
	commits := crashCommitCount(t, report.Root)
	again, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if again.Outcome != OutcomeNone {
		t.Fatalf("a second recovery pass found %q to do", again.Outcome)
	}
	requireState(t, report.Root, state, "after a second recovery pass")
	if now := crashCommitCount(t, report.Root); now != commits {
		t.Fatalf("a second recovery pass added %d commits", now-commits)
	}
}

// requireBooks asserts that the vault's four records of an import — content
// history, recovery history, provenance ledger, and the receipt owed to the peer
// — all say the same thing about it.
func requireBooks(t *testing.T, report crashReport, applied, provenance bool) {
	t.Helper()
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending != nil {
		t.Fatalf("recovery left attempt %q pending", state.Pending.PackageID())
	}

	record, remembered := state.Applied[report.PackageID]
	if remembered != applied {
		t.Fatalf("the import ledger remembers %s = %v, want %v", report.PackageID, remembered, applied)
	}
	if dirty := crashDirty(t, report.Root); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after recovery: %v", dirty)
	}
	requireHistory(t, report, record, applied)
	requireImports(t, report.Root, provenance)
}

// requireHistory asserts that the three records an applied import leaves behind
// — the commit, the recovery entry, and the published receipt — exist together
// and name each other, or that none of them exist at all.
//
// They are worth checking as one claim because their value is in the agreement.
// A receipt naming an entry the journal does not hold tells the peer an import
// is this vault's history when it is not, and no single record can be caught
// being wrong about that on its own.
func requireHistory(t *testing.T, report crashReport, record Record, applied bool) {
	t.Helper()
	// One seed commit, plus one for an import that became history. Two would mean
	// the recovery pass committed the same change again.
	wantCommits := 1
	wantEntries := 0
	if applied {
		wantCommits, wantEntries = 2, 1
	}
	if got := crashCommitCount(t, report.Root); got != wantCommits {
		t.Fatalf("the vault holds %d commits, want %d", got, wantCommits)
	}
	entries := crashEntries(t, report.Root)
	if len(entries) != wantEntries {
		t.Fatalf("the vault holds %d recovery entries, want %d", len(entries), wantEntries)
	}

	receipt := publishedReceipt(t, report.Root, report.PackageID)
	if applied != (receipt != nil) {
		t.Fatalf("a receipt for %s is published = %v, want %v", report.PackageID, receipt != nil, applied)
	}
	if !applied {
		return
	}
	if record.Receipt == nil {
		t.Fatalf("the import ledger records no receipt for an acknowledged package")
	}
	if receipt.RecoveryEntry != record.JournalEntry || receipt.RecoveryEntry != entries[0].ID {
		t.Fatalf("the receipt names entry %s, the ledger %s, the journal %s",
			receipt.RecoveryEntry, record.JournalEntry, entries[0].ID)
	}
}

// requireImports asserts the anti-echo ledger holds exactly the expected number
// of provenance records, which is what stops the peer's own change being
// published back to it.
func requireImports(t *testing.T, root string, provenance bool) {
	t.Helper()
	state, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatalf("vaultexport.LoadState: %v", err)
	}
	want := 0
	if provenance {
		want = 1
	}
	if len(state.Imported) != want {
		t.Fatalf("the provenance ledger holds %d records, want %d: %v", len(state.Imported), want, state.Imported)
	}
}

// A redelivery of a package a recovered vault already applied must change
// nothing and must republish the receipt it already minted, because the reason a
// peer re-sends is almost always that the reply went missing.
func TestRedeliveryAfterRecoveryChangesNothing(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashAcknowledged)

	if _, err := Recover(report.Root, Options{}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	first := publishedReceipt(t, report.Root, report.PackageID)
	if first == nil {
		t.Fatal("recovery adopted no receipt for an import that had published one")
	}
	state := crashState(t, report.Root)
	commits := crashCommitCount(t, report.Root)

	result, err := Apply(report.Root, report.PackageDir, Options{})
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if !result.Duplicate {
		t.Fatal("a redelivery was not reported as one")
	}
	if result.Receipt == nil || result.Receipt.AppliedAt != first.AppliedAt {
		t.Fatalf("the redelivery answered with a different receipt than the one already published")
	}
	requireState(t, report.Root, state, "after a redelivery")
	if now := crashCommitCount(t, report.Root); now != commits {
		t.Fatalf("a redelivery added %d commits", now-commits)
	}
	if entries := crashEntries(t, report.Root); len(entries) != 1 {
		t.Fatalf("a redelivery left %d recovery entries, want 1", len(entries))
	}
}

// An import interrupted before it became history is rolled back — but never over
// somebody else's newer edit. The crash, a person opening the note and saving it,
// and the next import are three events with no ordering between them, and the
// edit is the only copy of itself.
//
// Every artifact is kept on purpose: the attempt says the import is unresolved,
// the journal entry holds what the paths held before it, and the working tree
// holds what the person wrote. A person with all three can choose; a process that
// guessed would have destroyed one.
func TestRecoveryWillNotRollBackOverANewerEdit(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	const foreign = "---\ntitle: Alpha\n---\n\nSomebody saved this after the crash.\n"
	if err := os.WriteFile(filepath.Join(report.Root, crashModifiedPath), []byte(foreign), 0o644); err != nil {
		t.Fatalf("foreign edit: %v", err)
	}

	_, err := Recover(report.Root, Options{})
	requireIs(t, err, ErrStrandedImport, "recovering over a newer edit")
	if !strings.Contains(err.Error(), crashModifiedPath) {
		t.Fatalf("the refusal does not name the contested path: %v", err)
	}

	if got := crashState(t, report.Root)[crashModifiedPath]; got != foreign {
		t.Fatalf("recovery overwrote a newer edit: %q", got)
	}
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending.PackageID() != report.PackageID {
		t.Fatal("a stranded import cleared its own attempt")
	}
	finalized, err := journal.Finalized(noteops.JournalRoot(report.Root), state.Pending.JournalEntry)
	if err != nil {
		t.Fatalf("Finalized: %v", err)
	}
	if finalized {
		t.Fatal("a stranded import finalized its entry")
	}
	if _, err := journal.ReadSnapshot(noteops.JournalRoot(report.Root),
		state.Pending.JournalEntry, crashModifiedPath); err != nil {
		t.Fatalf("the stranded entry no longer holds what the path used to say: %v", err)
	}

	// And it stays refused, rather than resolving itself on a later pass: the
	// contested content is a decision, and nothing here is entitled to make it.
	_, again := Recover(report.Root, Options{})
	requireIs(t, again, ErrStrandedImport, "a second pass over a stranded import")

	// Which also means no further import can run, so the evidence cannot be
	// buried by the next package the transport delivers.
	_, applyErr := Apply(report.Root, report.PackageDir, Options{})
	requireIs(t, applyErr, ErrStrandedImport, "applying over a stranded import")
}

// A vault whose import died before it started must be usable again immediately,
// with the same package: nothing was applied, so nothing stands in the way.
func TestApplyAfterAnInterruptionResolvesItAndProceeds(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	result, err := Apply(report.Root, report.PackageDir, Options{})
	if err != nil {
		t.Fatalf("Apply after an interruption: %v", err)
	}
	if result.Duplicate {
		t.Fatal("a rolled-back import was reported as already applied")
	}
	requireState(t, report.Root, crashAfter(), "after re-applying a rolled-back import")
	if got := crashCommitCount(t, report.Root); got != 2 {
		t.Fatalf("the vault holds %d commits, want 2", got)
	}
	if entries := crashEntries(t, report.Root); len(entries) != 1 {
		t.Fatalf("the vault holds %d recovery entries, want 1", len(entries))
	}
	if publishedReceipt(t, report.Root, report.PackageID) == nil {
		t.Fatal("re-applying published no receipt")
	}
	if dirty := crashDirty(t, report.Root); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after re-applying: %v", dirty)
	}
}
