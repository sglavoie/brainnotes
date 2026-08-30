package vaultgit

import (
	"bytes"
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
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// testHarness is one temporary vault with a real Git repository and a Manager
// pointed at it. The clock is explicit so commit messages and retry schedules
// are reproducible.
type testHarness struct {
	t     *testing.T
	root  string
	clock time.Time
	mgr   *Manager
	// autosave is the floor every Manager this harness opens is given unless the
	// caller names one. It is the smallest legal interval rather than the shipped
	// default, so that a test about readiness is only about readiness; a test
	// about the floor sets it explicitly with setAutosave.
	autosave config.AutosavePolicy
}

// autosavePolicy resolves a spelling the way `brainnotes.toml` would, so a test
// names its floor in the same vocabulary a user does.
func autosavePolicy(t *testing.T, spelling string) config.AutosavePolicy {
	t.Helper()
	policy, err := config.ParseAutosaveMinInterval(spelling)
	if err != nil {
		t.Fatalf("ParseAutosaveMinInterval(%q): %v", spelling, err)
	}
	return policy
}

// requireGit skips a test on a machine without git rather than failing it. The
// package's unit tests cover the pure logic; these cover the plumbing, and
// plumbing tests need the plumbing.
func requireGit(t *testing.T) string {
	t.Helper()
	exe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available on PATH")
	}
	return exe
}

// isolateHome points HOME and the XDG directories at a scratch tree.
//
// Local plumbing already ignores the user's global config, but a push
// deliberately reads it — that is where an SSH command or a URL rewrite lives.
// Without this, a developer's own `~/.gitconfig` could change what these tests
// observe. It also protects the tests from the very setting that motivated this
// package: a global core.excludesFile matching `BRAIN[A-Z]*`.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// userEnv is the environment the test's own git invocations run with. It is
// built from scratch rather than inherited: one test deliberately puts a
// hostile GIT_DIR in the process environment to prove the engine ignores it,
// and the assertions that follow must not be hijacked by the same variable.
func userEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	}
	if sock, ok := os.LookupEnv("SSH_AUTH_SOCK"); ok {
		env = append(env, "SSH_AUTH_SOCK="+sock)
	}
	return env
}

// rawGit runs git directly, standing in for the user doing something by hand.
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(requireGit(t), args...)
	cmd.Dir = dir
	cmd.Env = userEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errBuf.String())
	}
	return strings.TrimSpace(out.String())
}

// newHarness creates an initialized but empty vault repository.
func newHarness(t *testing.T) *testHarness {
	t.Helper()
	requireGit(t)
	isolateHome(t)

	root := t.TempDir()
	rawGit(t, root, "-c", "init.defaultBranch=main", "init", "-q")
	rawGit(t, root, "config", "user.name", "Test User")
	rawGit(t, root, "config", "user.email", "test@example.invalid")

	h := &testHarness{
		t:        t,
		root:     root,
		clock:    time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC),
		autosave: autosavePolicy(t, config.CanonicalAutosaveDuration(config.MinAutosaveInterval)),
	}
	h.mgr = h.open(Options{})
	return h
}

// setAutosave adopts a new floor and reopens the harness Manager under it, which
// is what a guard restarting after a config edit does.
func (h *testHarness) setAutosave(spelling string) {
	h.t.Helper()
	h.autosave = autosavePolicy(h.t, spelling)
	h.mgr = h.open(Options{})
}

// policyFile is a stand-in for `brainnotes.toml` that holds one line: the
// autosave spelling. It is a real file inside the vault, so editing it makes the
// working tree dirty exactly the way a hand edit of the real configuration does,
// and the source below reads and fingerprints real bytes.
//
// It is not TOML on purpose. The production source in cmd/bn-vault-guard is what
// owns the file format, and reproducing that here would test the wrong seam:
// what this package's tests are about is what the watcher does with a policy and
// a hash, not how those two were derived.
const policyFile = "autosave-policy"

// writePolicyFile sets the on-disk policy the harness source reports.
func (h *testHarness) writePolicyFile(contents string) {
	h.t.Helper()
	h.write(policyFile, contents)
}

// policySource reads policyFile the way the guard reads brainnotes.toml: hash
// first, then resolve, with the hash surviving a resolution failure so an
// invalid version is identified by its bytes.
func (h *testHarness) policySource() PolicySource {
	path := filepath.Join(h.root, policyFile)
	return func() (PolicySnapshot, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return PolicySnapshot{}, err
		}
		snapshot := PolicySnapshot{Hash: vault.HashBytes(data)}
		policy, err := config.ParseAutosaveMinInterval(strings.TrimSpace(string(data)))
		if err != nil {
			return snapshot, err
		}
		snapshot.Policy = policy
		return snapshot, nil
	}
}

// stubPolicySource is the injectable source: a policy the test sets directly,
// with no file behind it. It is the right tool for a transition whose point is
// the transition rather than the edit that caused it, and for the cases a real
// file cannot produce on demand — an unreadable source, or a source that fails
// the same way twice.
type stubPolicySource struct {
	snapshot PolicySnapshot
	err      error
	calls    int
}

func (s *stubPolicySource) source() PolicySource {
	return func() (PolicySnapshot, error) {
		s.calls++
		return s.snapshot, s.err
	}
}

// set publishes a valid policy under a fresh fingerprint.
func (s *stubPolicySource) set(t *testing.T, spelling string) {
	t.Helper()
	s.snapshot = PolicySnapshot{Hash: "hash-" + spelling, Policy: autosavePolicy(t, spelling)}
	s.err = nil
}

// setInvalid publishes bytes that do not resolve. Two calls with the same hash
// are the same broken version; two with different hashes are two of them.
func (s *stubPolicySource) setInvalid(hash, message string) {
	s.snapshot = PolicySnapshot{Hash: hash}
	s.err = errors.New(message)
}

// setUnreadable publishes a source that could not read its bytes at all, so it
// has no fingerprint to deduplicate on.
func (s *stubPolicySource) setUnreadable(message string) {
	s.snapshot = PolicySnapshot{}
	s.err = errors.New(message)
}

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
