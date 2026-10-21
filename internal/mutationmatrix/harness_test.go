package mutationmatrix

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

// journalPrefix is where the undo manifests live. A snapshot is allowed to
// carry them for any journaled operation: they are what authorizes committing
// the deletions in the same commit.
const journalPrefix = config.DefaultJournalDir + "/"

// unrelatedNote is edited in the working tree but named by no case, so a commit
// that contains it swept in a change nobody asked to record.
const unrelatedNote = "Beta.md"

// baseFiles seed every fixture. They are shared so a case describes only the
// paths it touches, and so the unrelated-edit assertion always has a note that
// no operation under test has any reason to write.
var baseFiles = map[string]string{
	"Inbox.md":       "---\ntitle: Inbox\n---\n\n## Captures\n",
	"Alpha.md":       "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, see [[Missing Note]].\n",
	unrelatedNote:    "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body.\n",
	"Notes/Gamma.md": "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body.\n",
}

const fixtureConfig = `[frontmatter.properties.status]
type = "string"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[capture]
inbox_note = "Inbox.md"
heading = "## Captures"
`

// harness is one temporary vault that is a real Git repository, reachable by
// every layer under test: a *config.Config for the web server, a *vault.Vault
// for the note operations, and a marked vault root the Cobra tree resolves
// through --vault.
type harness struct {
	t    *testing.T
	root string
	cfg  *config.Config
	v    *vault.Vault
	// git is the real executable, resolved before PATH is rewritten, so the
	// test's own assertions are never answered by the failure shim.
	git string
	// failCommit is the sentinel whose existence makes the shimmed git refuse to
	// write a commit object. Creating it is how a snapshot failure is provoked
	// through the real engine rather than around it.
	failCommit string
}

func requireGit(t *testing.T) string {
	t.Helper()
	exe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available on PATH")
	}
	return exe
}

// isolateHome points HOME and the XDG directories at a scratch tree so a
// developer's own global Git configuration cannot change what these tests
// observe — including the core.excludesFile pattern that motivated the engine.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitEnv() []string {
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
	return env
}

// newHarness builds a vault with the base fixture plus extra, commits all of it,
// and leaves the working tree clean.
func newHarness(t *testing.T, extra map[string]string) *harness {
	t.Helper()
	realGit := requireGit(t)
	isolateHome(t)

	// The vault root is resolved through symlinks up front. macOS puts every
	// temp directory under /var, which is one, and the layers under test reach
	// the same vault by different routes — some resolving, some not. Resolving
	// once here means a path comparison in a test is comparing paths, not
	// comparing which layer happened to call EvalSymlinks.
	raw := t.TempDir()
	root, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}

	h := &harness{t: t, root: root, git: realGit}
	for rel, body := range baseFiles {
		h.writeFile(rel, body)
	}
	for rel, body := range extra {
		h.writeFile(rel, body)
	}
	h.writeFile(config.FileName, fixtureConfig)

	h.rawGit("-c", "init.defaultBranch=main", "init", "-q")
	h.rawGit("config", "user.name", "Test User")
	h.rawGit("config", "user.email", "test@example.invalid")
	h.rawGit("add", "-A")
	h.rawGit("commit", "-q", "-m", "fixture")

	h.installGitShim()
	// The engine is cached per vault root for the life of the process. Dropping
	// it keeps one case's blocked or shimmed engine from being handed to the
	// next, which would make failures depend on test order.
	vaultgit.ForgetShared(root)
	t.Cleanup(func() { vaultgit.ForgetShared(root) })

	// The config is loaded from the fixture's own brainnotes.toml rather than
	// built in Go, so every layer in the table sees the same configuration the
	// Cobra tree will resolve for itself from --vault.
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("load fixture config: %v", err)
	}
	disabled := false
	cfg.Serve = config.ServeConfig{Addr: "127.0.0.1:0", OpenBrowser: &disabled}
	h.cfg = cfg
	h.v, err = vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return h
}

// installGitShim puts a git on PATH that refuses to write a commit object while
// a sentinel file exists.
//
// It is on PATH rather than passed as an option because the whole point is to
// reach the engine the way production does: every layer under test resolves its
// engine through vaultgit.Shared with zero options, so PATH is the only seam a
// test has, and using it proves the production resolution path is what runs.
func (h *harness) installGitShim() {
	h.t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		h.t.Skip("a POSIX shell is required for this test")
	}
	dir := h.t.TempDir()
	h.failCommit = filepath.Join(dir, "fail-commit")
	// Every argument is scanned rather than just the first: the engine invokes
	// git with leading global flags, so matching on $1 would silently never fire.
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"commit-tree\" ] && [ -e " + shellQuote(h.failCommit) + " ]; then\n" +
		"    echo 'simulated object write failure' >&2\n" +
		"    exit 128\n" +
		"  fi\n" +
		"done\n" +
		"exec " + shellQuote(h.git) + " \"$@\"\n"
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		h.t.Fatalf("write git shim: %v", err)
	}
	h.t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// breakSnapshots arms the shim. The next snapshot fails to write its commit,
// which is a local failure and therefore latches blocked state.
func (h *harness) breakSnapshots() {
	h.t.Helper()
	if err := os.WriteFile(h.failCommit, nil, 0o644); err != nil {
		h.t.Fatalf("arm the failing git shim: %v", err)
	}
}

// repairSnapshots disarms the shim, so what refuses the next mutation is the
// latched block rather than a still-broken git.
func (h *harness) repairSnapshots() {
	h.t.Helper()
	if err := os.Remove(h.failCommit); err != nil && !os.IsNotExist(err) {
		h.t.Fatalf("disarm the failing git shim: %v", err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (h *harness) abs(rel string) string {
	return filepath.Join(h.root, filepath.FromSlash(rel))
}

func (h *harness) writeFile(rel, body string) {
	h.t.Helper()
	abs := h.abs(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		h.t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		h.t.Fatalf("write %q: %v", rel, err)
	}
}

func (h *harness) readFile(rel string) string {
	h.t.Helper()
	data, err := os.ReadFile(h.abs(rel))
	if err != nil {
		h.t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}

func (h *harness) exists(rel string) bool {
	_, err := os.Lstat(h.abs(rel))
	return err == nil
}

// rawGit runs the real git directly, standing in for a person inspecting the
// repository by hand.
func (h *harness) rawGit(args ...string) string {
	h.t.Helper()
	out, err := h.tryGit(args...)
	if err != nil {
		h.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

// tryGit returns git's stdout verbatim. Trimming happens in rawGit rather than
// here, because a committed note's trailing newline is content and comparing it
// against the working tree has to see it.
func (h *harness) tryGit(args ...string) (string, error) {
	h.t.Helper()
	cmd := exec.Command(h.git, args...)
	cmd.Dir = h.root
	cmd.Env = gitEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", errorWithStderr(err, errBuf.String())
	}
	return out.String(), nil
}

type gitError struct {
	err    error
	stderr string
}

func (e *gitError) Error() string { return e.err.Error() + ": " + strings.TrimSpace(e.stderr) }

func errorWithStderr(err error, stderr string) error { return &gitError{err: err, stderr: stderr} }

// head reports the current commit, or an empty string on an unborn branch.
func (h *harness) head() string {
	h.t.Helper()
	out, err := h.tryGit("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// commitCount is how many commits the branch holds, which is what "exactly one
// commit" is measured against.
func (h *harness) commitCount() int {
	h.t.Helper()
	if h.head() == "" {
		return 0
	}
	out := h.rawGit("rev-list", "--count", "HEAD")
	n := 0
	for _, c := range out {
		if c < '0' || c > '9' {
			h.t.Fatalf("rev-list --count returned %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// changedPaths lists the vault-relative paths one commit touched, in either
// direction. It reads NUL-delimited output because a vault legitimately holds
// names git would otherwise C-quote.
func (h *harness) changedPaths(rev string) []string {
	h.t.Helper()
	out := h.rawGit("diff-tree", "--no-commit-id", "-r", "-z", "--name-only", rev)
	if out == "" {
		return nil
	}
	paths := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	sort.Strings(paths)
	return paths
}

// show reads a path's committed content, reporting absence rather than failing.
func (h *harness) show(rev, rel string) (string, bool) {
	h.t.Helper()
	out, err := h.tryGit("show", rev+":"+rel)
	if err != nil {
		return "", false
	}
	return out, true
}

// differsFromHead reports whether the working tree and the tip disagree about
// one path, in existence or in content. It is how a test asserts that a change
// really is on disk after its snapshot failed, without knowing what the change
// was.
func (h *harness) differsFromHead(rel string) bool {
	h.t.Helper()
	committed, tracked := h.show("HEAD", rel)
	if !h.exists(rel) {
		return tracked
	}
	if !tracked {
		return true
	}
	return h.readFile(rel) != committed
}

// tree lists every path in the vault except the repository itself, so an
// operation the location policy refused can be shown to have created nothing at
// all — not a folder, not a lock, not a journal entry.
func (h *harness) tree() []string {
	h.t.Helper()
	var paths []string
	err := filepath.WalkDir(h.root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(h.root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			return filepath.SkipDir
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		h.t.Fatalf("walk vault: %v", err)
	}
	sort.Strings(paths)
	return paths
}

func (h *harness) subject(rev string) string {
	h.t.Helper()
	return h.rawGit("log", "-1", "--format=%s", rev)
}

// nonJournalPaths drops the undo manifests from a change set, leaving the paths
// a case is expected to name.
func nonJournalPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, journalPrefix) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func hasJournalPaths(paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, journalPrefix) {
			return true
		}
	}
	return false
}
