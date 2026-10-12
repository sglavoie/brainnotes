package vaultgit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func (h *testHarness) open(opts Options) *Manager {
	h.t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return h.clock }
	}
	if opts.Autosave == (config.AutosavePolicy{}) {
		opts.Autosave = h.autosave
	}
	m, err := Open(context.Background(), h.root, opts)
	if err != nil {
		h.t.Fatalf("Open: %v", err)
	}
	return m
}

func (h *testHarness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

// slowClock makes every clock read on the current Manager land step after the
// previous one, and returns a function that hands the clock back to the test.
//
// It provokes the one condition a fixed fake clock cannot express: a snapshot
// that takes real time. Intent creation and promotion, commit creation, and the
// observed ref landing read the clock at distinct points. Holding those instants
// equal is what lets a shortened floor or a backdated explicit commit go
// unnoticed.
func (h *testHarness) slowClock(step time.Duration) (restore func()) {
	mgr := h.mgr
	previous := mgr.now
	mgr.now = func() time.Time {
		h.clock = h.clock.Add(step)
		return h.clock
	}
	return func() { mgr.now = previous }
}

// write creates or replaces a note.
func (h *testHarness) write(rel, body string) {
	h.t.Helper()
	abs := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		h.t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		h.t.Fatalf("write %q: %v", rel, err)
	}
}

func (h *testHarness) remove(rel string) {
	h.t.Helper()
	if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(rel))); err != nil {
		h.t.Fatalf("remove %q: %v", rel, err)
	}
}

func (h *testHarness) journalRoot() string {
	return filepath.Join(h.root, filepath.FromSlash(config.DefaultJournalDir))
}

// record runs mutate between a journal capture and a finalize, producing the
// same finalized entry a real `bn` command would leave behind. It is the only
// thing that can authorize a deletion, so tests build one exactly the way the
// CLI does rather than hand-writing a manifest.
func (h *testHarness) record(command string, paths []string, mutate func()) *journal.Entry {
	h.t.Helper()
	rec, err := journal.NewRecorder(h.root, h.journalRoot(), command)
	if err != nil {
		h.t.Fatalf("NewRecorder: %v", err)
	}
	for _, p := range paths {
		if err := rec.Capture(filepath.Join(h.root, filepath.FromSlash(p))); err != nil {
			h.t.Fatalf("Capture %q: %v", p, err)
		}
	}
	mutate()
	entry, err := rec.Finalize()
	if err != nil {
		h.t.Fatalf("Finalize: %v", err)
	}
	return entry
}

func (h *testHarness) commitPending() CommitResult {
	h.t.Helper()
	result, err := h.mgr.CommitPending(context.Background())
	if err != nil {
		h.t.Fatalf("CommitPending: %v", err)
	}
	return result
}

// failWatchWriteFrom makes the nth durable write of watch.json on mgr fail, and
// every later one, standing in for a full disk or a process that died in that
// instant.
//
// Counting is how a test reaches the one write that matters. A single
// CommitPending writes watch.json exactly twice — once before the attempt and
// once with the observed ref-landing bound — and the failure this is about is
// the second of those, which cannot be provoked from outside the process without
// also breaking the first.
func failWatchWriteFrom(mgr *Manager, n int) {
	writes := 0
	mgr.state.failWrite = func(name string) error {
		if name != watchStateFileName {
			return nil
		}
		writes++
		if writes < n {
			return nil
		}
		return errors.New("injected: the debounce clock could not be written")
	}
}

// head reports the current commit, or an empty string on an unborn branch.
func (h *testHarness) head() string {
	h.t.Helper()
	cmd := exec.Command(requireGit(h.t), "rev-parse", "--verify", "--quiet", "HEAD")
	cmd.Dir = h.root
	cmd.Env = userEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// tracked lists every path the tip holds, so a test can assert exactly what is
// and is not in the history. It reads NUL-delimited output because the vault
// deliberately holds names git would otherwise C-quote.
func (h *testHarness) tracked() []string {
	h.t.Helper()
	if h.head() == "" {
		return nil
	}
	out := rawGit(h.t, h.root, "ls-tree", "-r", "-z", "--name-only", "--full-tree", "HEAD")
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}

func (h *testHarness) trackedSet() map[string]bool {
	set := map[string]bool{}
	for _, p := range h.tracked() {
		set[p] = true
	}
	return set
}

func (h *testHarness) subjects() []string {
	h.t.Helper()
	if h.head() == "" {
		return nil
	}
	out := rawGit(h.t, h.root, "log", "--format=%s")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// watchJSON decodes watch.json into the shape a consumer outside this package
// sees: top-level keys with JSON values, and an absent key genuinely missing
// rather than zero. The health script reads it exactly this way.
func (h *testHarness) watchJSON() map[string]any {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.mgr.StateDir(), watchStateFileName))
	if err != nil {
		h.t.Fatalf("read watch.json: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		h.t.Fatalf("decode watch.json: %v", err)
	}
	return fields
}

// requireWatchPolicy asserts the policy token the vault has *published*, which
// is a different question from the one requirePolicy asks: that one is what this
// process is enforcing, and this one is what a process starting fresh would find
// on disk and have to reason from.
func requireWatchPolicy(t *testing.T, h *testHarness, want string) {
	t.Helper()
	if got := h.mgr.WatchStatus().AutosaveMinInterval; got != want {
		t.Fatalf("watch.json publishes policy %q, want %q", got, want)
	}
}

// corruptWatchPolicy replaces the published policy token with bytes this build
// cannot resolve, leaving the rest of the record intact and readable. It stands
// in for a hand edit or a half-written file — the record still decodes, so the
// only thing in question is what an unreadable token means.
func (h *testHarness) corruptWatchPolicy(token string) {
	h.t.Helper()
	path := filepath.Join(h.mgr.StateDir(), watchStateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("read watch.json: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		h.t.Fatalf("decode watch.json: %v", err)
	}
	fields["autosave_min_interval"] = token
	encoded, err := json.Marshal(fields)
	if err != nil {
		h.t.Fatalf("encode watch.json: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		h.t.Fatalf("write watch.json: %v", err)
	}
}

// commitTimes reports every commit's committer date, newest first. The engine
// stamps both dates from its injected clock, so on this harness these are
// exactly the instants the schedule chose.
func (h *testHarness) commitTimes() []time.Time {
	h.t.Helper()
	if h.head() == "" {
		return nil
	}
	out := rawGit(h.t, h.root, "log", "--format=%cI")
	if out == "" {
		return nil
	}
	times := make([]time.Time, 0, 8)
	for _, line := range strings.Split(out, "\n") {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(line))
		if err != nil {
			h.t.Fatalf("parse commit date %q: %v", line, err)
		}
		times = append(times, at.UTC())
	}
	return times
}

func (h *testHarness) blockRecord() (BlockRecord, bool) {
	h.t.Helper()
	return h.mgr.Blocked()
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// gitShim writes an executable shell script that stands in for git. It is how
// the tests reach failure modes that cannot be provoked from outside: a branch
// that moves in the instant between planning and landing a commit, and an
// object write that fails.
func gitShim(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("a POSIX shell is required for this test")
	}
	real := requireGit(t)
	path := filepath.Join(t.TempDir(), "git-shim")
	script := "#!/bin/sh\nREAL_GIT=" + shellQuote(real) + "\n" + body + "\nexec \"$REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
