package vaultgit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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

// TestMain points HOME and the XDG directories at one scratch tree for the
// whole package.
//
// Local plumbing already ignores the user's global config, but a push
// deliberately reads it — that is where an SSH command or a URL rewrite lives.
// Without this, a developer's own `~/.gitconfig` could change what these tests
// observe. It also protects the tests from the very setting that motivated this
// package: a global core.excludesFile matching `BRAIN[A-Z]*`.
//
// It is set once here rather than per test with t.Setenv because t.Setenv
// forbids t.Parallel, and every test builds its own temporary repository, so
// nothing about the isolated home differs between tests. No test writes to it.
//
// Every test runs in parallel except TestStrayGitEnvironmentIsIgnored, which
// hijacks the process environment with t.Setenv.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "vaultgit-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "vaultgit tests: create isolated HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	for key, value := range map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL":   filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "vaultgit tests: set %s: %v\n", key, err)
			return 1
		}
	}
	return m.Run()
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
