package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// treeDigest fingerprints every entry under dir by path, mode, and content.
//
// It walks `.git` too, deliberately: the schedule this test is about lives under
// `.git/brainnotes-safety/`, and so does the recovery intent. Contents rather
// than timestamps, because a reader that only stat'ed a file is allowed to have
// stat'ed it.
func treeDigest(t *testing.T, dir string) map[string]string {
	t.Helper()
	digest := map[string]string{}
	err := filepath.WalkDir(dir, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, abs)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			digest[rel] = "dir"
			return nil
		}
		if !info.Mode().IsRegular() {
			digest[rel] = fmt.Sprintf("irregular %v", info.Mode())
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		digest[rel] = fmt.Sprintf("%v %s", info.Mode().Perm(), hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", dir, err)
	}
	return digest
}

// requireSameTree fails naming every path that appeared, vanished, or changed.
func requireSameTree(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	paths := map[string]struct{}{}
	for p := range before {
		paths[p] = struct{}{}
	}
	for p := range after {
		paths[p] = struct{}{}
	}
	var sorted []string
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		was, had := before[p]
		is, has := after[p]
		switch {
		case had && !has:
			t.Errorf("%s removed %s", what, p)
		case !had && has:
			t.Errorf("%s created %s", what, p)
		case was != is:
			t.Errorf("%s changed %s: %s -> %s", what, p, was, is)
		}
	}
}

// historyFixture is a vault with two snapshot commits and a watch schedule
// already on disk, which is the state a reader has to be harmless against: an
// autosave floor and a debounce deadline that a *writer* opening this repository
// would legitimately advance.
func historyFixture(t *testing.T) (*testHarness, []string) {
	t.Helper()
	h := newHarness(t)

	h.record("bn new", []string{"Alpha.md"}, func() { h.write("Alpha.md", "alpha\n") })
	h.commitPending()
	h.advance(h.mgr.AutosavePolicy().Interval * 4)
	h.record("bn set", []string{"Alpha.md", "Notes/Gamma.md"}, func() {
		h.write("Alpha.md", "alpha edited\n")
		h.write("Notes/Gamma.md", "gamma\n")
	})
	h.commitPending()

	commits := strings.Fields(rawGit(t, h.root, "rev-list", "--reverse", "HEAD"))
	if len(commits) != 2 {
		t.Fatalf("fixture built %d commits, want 2", len(commits))
	}
	return h, commits
}

// readEverything calls every method a History has, so the no-write assertion is
// made about the whole surface rather than about whichever one a test happened
// to reach for. A method added without a line here is a method this test stops
// covering, and nothing here catches that for you.
func readEverything(t *testing.T, view *History, commits []string) {
	t.Helper()
	ctx := context.Background()

	_ = view.Root()
	_ = view.Branch()
	_ = view.Watch()
	if _, _, err := view.Blocked(); err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	tip, err := view.Tip(ctx)
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}
	if _, err := view.Contains(ctx, commits[0], tip); err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if _, err := view.CommitsAfter(ctx, "", tip); err != nil {
		t.Fatalf("CommitsAfter from the root: %v", err)
	}
	if _, err := view.CommitsAfter(ctx, commits[0], tip); err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	for _, commit := range commits {
		readCommit(t, view, commit)
	}
}

// readCommit calls every method that takes a commit, including the object reads
// its changes lead to.
func readCommit(t *testing.T, view *History, commit string) {
	t.Helper()
	ctx := context.Background()

	if _, err := view.Inspect(ctx, commit); err != nil {
		t.Fatalf("Inspect %s: %v", commit, err)
	}
	changes, err := view.Changes(ctx, commit)
	if err != nil {
		t.Fatalf("Changes %s: %v", commit, err)
	}
	var oids []string
	for _, change := range changes {
		if change.NewOID != "" {
			oids = append(oids, change.NewOID)
		}
	}
	if _, err := view.Blobs(ctx, oids); err != nil {
		t.Fatalf("Blobs: %v", err)
	}
	for _, oid := range oids {
		if _, _, err := view.BlobDigest(ctx, oid); err != nil {
			t.Fatalf("BlobDigest %s: %v", oid, err)
		}
	}
	if _, err := view.Tree(ctx, commit); err != nil {
		t.Fatalf("Tree %s: %v", commit, err)
	}
	if _, err := view.EntryKinds(ctx, commit, []string{"Alpha.md"}); err != nil {
		t.Fatalf("EntryKinds %s: %v", commit, err)
	}
}

// TestHistoryReadsWithoutDisturbingTheVault is the requirement that export
// observation must not alter scheduled external-edit timing.
//
// The debounce deadline and the autosave floor live in `watch.json` under the
// state directory, and a Manager opened for writing legitimately rewrites them.
// A reader that did the same would move the next autosave every time somebody
// asked what there was to export — an observation with the power to disturb what
// it observes. The assertion is made over the whole vault, working tree and
// repository alike, so it also covers the index, the refs, and the recovery
// intent rather than only the one file the bug would most likely land in.
func TestHistoryReadsWithoutDisturbingTheVault(t *testing.T) {
	h, commits := historyFixture(t)

	stateDir := h.mgr.StateDir()
	before := treeDigest(t, h.root)
	beforeState := treeDigest(t, stateDir)

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	// Opening alone must already be harmless: a reader that only created its
	// state directory on open would still have written to the vault.
	requireSameTree(t, beforeState, treeDigest(t, stateDir), "OpenHistory")

	readEverything(t, view, commits)

	requireSameTree(t, beforeState, treeDigest(t, stateDir), "reading the history")
	requireSameTree(t, before, treeDigest(t, h.root), "reading the history")
}

// TestHistoryTakesNoLock proves the reader is harmless in the other direction
// too: a mutation is still free to proceed while a History is open, so a
// long-running export cannot stall a person's save.
func TestHistoryTakesNoLock(t *testing.T) {
	h, commits := historyFixture(t)

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	readEverything(t, view, commits)

	h.advance(h.mgr.AutosavePolicy().Interval * 4)
	h.record("bn set", []string{"Beta.md"}, func() { h.write("Beta.md", "beta\n") })
	h.commitPending()

	tip, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip after the write: %v", err)
	}
	if tip != h.head() {
		t.Fatalf("the view resolved %q, want the new tip %q", tip, h.head())
	}
	// The view was opened before that commit existed and reports it without being
	// reopened, because it holds no snapshot of anything.
	after, err := view.CommitsAfter(context.Background(), commits[len(commits)-1], tip)
	if err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	if len(after) != 1 || after[0] != tip {
		t.Fatalf("commits after the fixture: got %v, want [%s]", after, tip)
	}
}

// TestHistoryIgnoresUncommittedEdits is the property the exporter is built on:
// every method reads committed objects, so a half-typed note is invisible by
// construction rather than by a filter somebody has to remember to apply.
func TestHistoryIgnoresUncommittedEdits(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip := commits[len(commits)-1]

	h.write("Alpha.md", "half a sentence typed into an open editor")
	h.write("Draft.md", "brand new and unsaved\n")

	got, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}
	if got != tip {
		t.Fatalf("an uncommitted edit moved the tip to %q, want %q", got, tip)
	}
	files, err := view.Tree(context.Background(), tip)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	for _, file := range files {
		if file.Path == "Draft.md" {
			t.Fatal("an uncommitted file is in the committed tree")
		}
		if file.Path != "Alpha.md" {
			continue
		}
		blobs, err := view.Blobs(context.Background(), []string{file.OID})
		if err != nil {
			t.Fatalf("Blobs: %v", err)
		}
		if got := string(blobs[file.OID]); got != "alpha edited\n" {
			t.Fatalf("Alpha.md reads %q, want the committed bytes", got)
		}
	}
}

// TestOpenHistoryRefusesWhatOpenRefuses pins the two views to one set of
// structural refusals. They share openWorkingTree and resolveGitDir precisely so
// they cannot disagree about what a vault is; a reader that accepted a
// repository the writer rejects would report a history nothing else can commit
// to.
func TestOpenHistoryRefusesWhatOpenRefuses(t *testing.T) {
	requireGit(t)

	t.Run("no repository", func(t *testing.T) {
		isolateHome(t)
		_, err := OpenHistory(context.Background(), t.TempDir(), HistoryOptions{})
		if !IsUnmanaged(err) {
			t.Fatalf("OpenHistory on a non-Git vault = %v, want ErrUnmanaged", err)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		isolateHome(t)
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
			t.Fatalf("write .git file: %v", err)
		}
		if _, err := OpenHistory(context.Background(), root, HistoryOptions{}); KindOf(err) != KindRepositoryState {
			t.Fatalf("OpenHistory with a .git file = %v, want a repository-state failure", err)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		h, commits := historyFixture(t)
		rawGit(t, h.root, "checkout", "-q", "--detach", commits[0])
		_, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
		if KindOf(err) != KindRepositoryState {
			t.Fatalf("OpenHistory on a detached HEAD = %v, want a repository-state failure", err)
		}
		if !strings.Contains(err.Error(), "detached") {
			t.Fatalf("the refusal must say why: %v", err)
		}
	})
}

// TestTipReportsAnUnbornBranchAsItsOwnThing separates "this vault has no history
// yet" from "this repository is broken". The first is an ordinary state for a
// vault enrolled before its first commit and means there is nothing to export;
// the second must never be read that way.
func TestTipReportsAnUnbornBranchAsItsOwnThing(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	rawGit(t, root, "-c", "init.defaultBranch=main", "init", "-q")

	view, err := OpenHistory(context.Background(), root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	if _, err := view.Tip(context.Background()); !errors.Is(err, ErrUnbornBranch) {
		t.Fatalf("Tip on an unborn branch = %v, want ErrUnbornBranch", err)
	}
}

// TestCommitsAfterIsOldestFirst pins the replay order. `rev-list` is newest-first
// and `-n` selects *before* `--reverse` reverses, so a bounded query would return
// the newest few in oldest-first order — which reads as correct and would replay
// a vault's history backwards from the middle.
func TestCommitsAfterIsOldestFirst(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}

	all, err := view.CommitsAfter(context.Background(), "", tip)
	if err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	if strings.Join(all, " ") != strings.Join(commits, " ") {
		t.Fatalf("commits: got %v, want %v", all, commits)
	}
	// And a base that is already the tip has nothing after it.
	rest, err := view.CommitsAfter(context.Background(), tip, tip)
	if err != nil {
		t.Fatalf("CommitsAfter from the tip: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("commits after the tip: %v", rest)
	}
}

// TestHistoryRefusesObjectNamesItDidNotProduce is checkObjectNames at the
// boundary. Every name these methods take came from git, so the only way one of
// these strings arrives is a corrupt cursor read back off a disk — and it must
// fail as that, rather than reaching rev-list as a range of its own devising.
func TestHistoryRefusesObjectNamesItDidNotProduce(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip := commits[len(commits)-1]

	for _, bad := range []string{"--all", "HEAD", "", commits[0][:7], strings.ToUpper(commits[0])} {
		requireRefusedObjectName(t, view, tip, bad)
	}
	// An empty base is the one exception, and it means "from the root commit".
	if _, err := view.CommitsAfter(context.Background(), "", tip); err != nil {
		t.Fatalf("CommitsAfter with no base: %v", err)
	}
}

// objectNameProbe is one method call made with a name checkObjectNames must
// refuse, paired with the name to report it by.
type objectNameProbe struct {
	method string
	call   func() error
}

// requireRefusedObjectName fails for every method that accepted name, so a
// method added without a refusal is a method this test reports.
func requireRefusedObjectName(t *testing.T, view *History, tip, name string) {
	t.Helper()
	ctx := context.Background()

	// A table rather than a run of ifs, so adding the next method's probe is one
	// line and the complexity ratchet stays out of the way of doing that.
	probes := []objectNameProbe{
		{"CommitsAfter tip", func() error { _, err := view.CommitsAfter(ctx, "", name); return err }},
		{"Inspect", func() error { _, err := view.Inspect(ctx, name); return err }},
		{"Changes", func() error { _, err := view.Changes(ctx, name); return err }},
		{"Tree", func() error { _, err := view.Tree(ctx, name); return err }},
		{"Blobs", func() error { _, err := view.Blobs(ctx, []string{name}); return err }},
		{"BlobDigest", func() error { _, _, err := view.BlobDigest(ctx, name); return err }},
		{"Contains", func() error { _, err := view.Contains(ctx, name, tip); return err }},
		{"EntryKinds", func() error { _, err := view.EntryKinds(ctx, name, []string{"Alpha.md"}); return err }},
	}
	// An empty base is CommitsAfter's one legitimate value — it means "from the
	// root commit" — and its caller asserts that separately.
	if name != "" {
		probes = append(probes, objectNameProbe{
			"CommitsAfter base",
			func() error { _, err := view.CommitsAfter(ctx, name, tip); return err },
		})
	}

	for _, probe := range probes {
		if err := probe.call(); err == nil {
			t.Errorf("%s accepted %q", probe.method, name)
		}
	}
}
