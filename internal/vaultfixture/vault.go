package vaultfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// DefaultConfig is the brainnotes.toml a fixture vault gets when Options.Config
// is empty. It declares the two properties fixtures most often assert on and
// nothing else: a fixture config that enabled more than a test needs would make
// unrelated behaviour depend on this file.
const DefaultConfig = `[frontmatter.properties.status]
type = "string"

[frontmatter.properties.tags]
type  = "list"
items = "string"
`

// DefaultFiles seed a vault that is a usable note graph rather than an empty
// directory: a linked pair, an unlinked note, and a note no case under test has
// any reason to write. Sweeping Unrelated.md into a commit or a synchronization
// package is the failure these fixtures exist to catch, so every vault carries
// one whether or not the test remembers to ask for it.
func DefaultFiles() map[string]string {
	return map[string]string{
		"Alpha.md":       "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, see [[Beta]].\n",
		"Beta.md":        "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body.\n",
		"Notes/Gamma.md": "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body.\n",
		UnrelatedNote:    "---\nstatus: draft\ntitle: Unrelated\n---\n\nNobody under test writes this.\n",
	}
}

// UnrelatedNote is the sentinel every fixture vault carries. It is named by no
// operation, so a commit, package, or backup that contains it swept in a change
// nobody asked to record.
const UnrelatedNote = "Unrelated.md"

// Options describe one fixture vault. The zero value is valid and produces a
// vault named "vault" holding DefaultConfig and DefaultFiles.
type Options struct {
	// Name is the fixture's label. It becomes the vault's directory name and
	// appears in failure messages, so a two-vault test says which side failed.
	Name string
	// Config is the brainnotes.toml body. Empty means DefaultConfig.
	Config string
	// Files are vault-relative slash paths to file bodies. Nil means
	// DefaultFiles; a non-nil map replaces them entirely, so a test that needs
	// an empty vault passes map[string]string{}.
	Files map[string]string
}

// Vault is one disposable Git-backed vault under the system temporary
// directory. It is deleted when the test ends.
type Vault struct {
	// Name is Options.Name, for failure messages.
	Name string
	// Root is the vault root: the directory holding brainnotes.toml and .git.
	// It is symlink-resolved, so comparing it against a path a server or a
	// command reported compares paths rather than which layer happened to call
	// filepath.EvalSymlinks.
	Root string

	// git is the real executable, resolved once. Tests that shim git onto PATH
	// keep their own assertions answered by the real one.
	git string
	// commits counts the commits this vault has recorded, which is what dates
	// each one. Git advances it, not Commit, so a caller who reaches for
	// v.Git(t, "commit", ...) directly gets the same pinned clock. See
	// commitDate.
	commits int
}

// Pair is two independent disposable vaults seeded from identical content: the
// starting state of two enrolled replicas of one vault. They share no storage —
// separate roots, separate Git repositories — so anything that appears in one
// after an operation got there through the code under test.
//
// Because the seed content and the commit dates are both pinned, the two start
// at the *same* commit hash, which is what two replicas of one vault should
// look like and is not a sign that they share anything. Independence is the
// object database, not the hash: a commit made in one is absent from the
// other's, and TestPairVaultsAreDisposableGitRepositories asserts exactly that.
type Pair struct {
	// Dir is the disposable parent holding both vaults. Staging channels and
	// other per-pair scratch storage belong under it, not beside the vaults.
	Dir string
	// Local is the device the operation under test runs on (the Mac side of
	// this project's deployment).
	Local *Vault
	// Peer is the other enrolled device (the Pi side).
	Peer *Vault
}

// ScrubEnv removes $BNOTES_VAULT for the duration of the test and restores it
// afterwards.
//
// This is the narrow half of Isolate, for a test that resolves no vault at all
// and only needs the ambient one to be unreachable. $BNOTES_VAULT overrides a
// working directory, so a command run from a scratch directory resolves the
// developer's real vault unless the variable is gone.
func ScrubEnv(t *testing.T) {
	t.Helper()
	previous, had := os.LookupEnv(EnvVault)
	if err := os.Unsetenv(EnvVault); err != nil {
		t.Fatalf("scrub $%s: %v", EnvVault, err)
	}
	t.Cleanup(func() {
		if !had {
			return
		}
		if err := os.Setenv(EnvVault, previous); err != nil {
			t.Fatalf("restore $%s: %v", EnvVault, err)
		}
	})
}

// ScrubProcessEnv removes $BNOTES_VAULT from the process with no restore, for
// a package TestMain to call before any test runs. A test that needs the
// variable set points it at its own fixture through Vault.Use.
func ScrubProcessEnv() error {
	if err := os.Unsetenv(EnvVault); err != nil {
		return fmt.Errorf("vaultfixture: cannot scrub $%s: %w", EnvVault, err)
	}
	return nil
}

// Isolate scrubs $BNOTES_VAULT and points HOME, the XDG directories, and Git's
// global configuration at a scratch tree, so neither an ambient vault nor a
// developer's own global Git configuration can change what a fixture observes.
// core.excludesFile is the concrete case: a user's global ignore pattern
// silently changes which files a snapshot captures.
//
// It uses t.Setenv, so a test that calls it — directly or through New or
// NewPair — cannot also call t.Parallel.
func Isolate(t *testing.T) {
	t.Helper()
	ScrubEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// New builds one disposable Git-backed vault with an initial commit and a clean
// working tree.
func New(t *testing.T, opts Options) *Vault {
	t.Helper()
	Isolate(t)
	return newVault(t, t.TempDir(), opts)
}

// NewPair builds two independent disposable Git-backed vaults, both seeded from
// files (nil means DefaultFiles) and both committed, under one disposable
// parent directory.
func NewPair(t *testing.T, files map[string]string) *Pair {
	t.Helper()
	Isolate(t)
	dir := resolveDir(t, t.TempDir())
	return &Pair{
		Dir:   dir,
		Local: newVault(t, dir, Options{Name: "local", Files: files}),
		Peer:  newVault(t, dir, Options{Name: "peer", Files: files}),
	}
}

// newVault does the work of New without re-isolating the environment, so a Pair
// builds both of its vaults under one HOME.
func newVault(t *testing.T, parent string, opts Options) *Vault {
	t.Helper()
	v := &Vault{Name: orDefault(opts.Name, "vault"), git: requireGit(t)}
	v.Root = filepath.Join(resolveDir(t, parent), v.Name)
	// Refused before the directory exists, not after it does. A refusal that
	// has already created its target has left behind exactly the artifact it
	// exists to prevent -- the ordering internal/noteops.BeginOperation encodes
	// for real mutations, held here for the same reason. The parent was
	// symlink-resolved above, so this judges the path the filesystem will use.
	if err := IsolationError(v.Root, v.Root); err != nil {
		t.Fatalf("refusing to build a vault fixture: %v", err)
	}

	//bn:direct-write IsolationError above refused unless v.Root is throwaway storage under the system temporary directory and is not a real vault
	if err := os.MkdirAll(v.Root, 0o755); err != nil {
		t.Fatalf("%s: mkdir vault root: %v", v.Name, err)
	}

	v.Write(t, config.FileName, orDefault(opts.Config, DefaultConfig))
	files := opts.Files
	if files == nil {
		files = DefaultFiles()
	}
	for rel, body := range files {
		v.Write(t, rel, body)
	}

	v.Git(t, "-c", "init.defaultBranch=main", "init", "-q")
	v.Git(t, "config", "user.name", "Fixture User")
	v.Git(t, "config", "user.email", "fixture@example.invalid")
	v.Commit(t, "fixture")
	return v
}

// Use points $BNOTES_VAULT at this vault and refuses unless the process now
// resolves it, which is the assertion a plain `cd` into a scratch directory
// cannot make.
func (v *Vault) Use(t *testing.T) {
	t.Helper()
	t.Setenv(EnvVault, v.Root)
	if err := IsolationError(AmbientVaultRoot(), v.Root); err != nil {
		t.Fatalf("refusing to run a mutating verification: %v", err)
	}
}

// Write creates or replaces a vault-relative slash path, making parents as
// needed. A path that would leave the vault root is refused: the root was
// proved isolated once, and a `..` in a caller's relative path is how a write
// lands somewhere that proof never covered.
func (v *Vault) Write(t *testing.T, rel, body string) {
	t.Helper()
	if err := v.containmentError(rel); err != nil {
		t.Fatalf("%s: refusing to write: %v", v.Name, err)
	}
	abs := filepath.Join(v.Root, filepath.FromSlash(rel))

	//bn:direct-write the check above keeps the write inside a root IsolationError already proved is disposable temporary storage
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("%s: mkdir %q: %v", v.Name, filepath.Dir(abs), err)
	}
	//bn:direct-write same root containment check as the MkdirAll above
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("%s: write %q: %v", v.Name, rel, err)
	}
}

// containmentError reports why rel does not name a file inside this vault, or
// nil when it does. It is a separate function so the refusal can be asserted
// directly: Write reports it through t.Fatalf, which a test cannot catch.
func (v *Vault) containmentError(rel string) error {
	abs := filepath.Join(v.Root, filepath.FromSlash(rel))
	if abs == v.Root {
		return fmt.Errorf("%q names the vault root itself rather than a file inside it", rel)
	}
	if !pathWithin(abs, v.Root) {
		return fmt.Errorf("%q resolves to %q, outside the vault root %q", rel, abs, v.Root)
	}
	return nil
}

// Read returns the body of a vault-relative slash path.
func (v *Vault) Read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: read %q: %v", v.Name, rel, err)
	}
	return string(data)
}

// Path returns the absolute path of a vault-relative slash path, whether or not
// it exists.
func (v *Vault) Path(rel string) string {
	return filepath.Join(v.Root, filepath.FromSlash(rel))
}

// Git runs the real git in this vault and returns its trimmed stdout, failing
// the test on a nonzero exit.
func (v *Vault) Git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(v.git, args...)
	cmd.Dir = v.Root
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_DATE="+commitDate(v.commits), "GIT_COMMITTER_DATE="+commitDate(v.commits))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: git %s: %v\n%s", v.Name, strings.Join(args, " "), err, out)
	}
	if isCommitInvocation(args) {
		v.commits++
	}
	return strings.TrimSpace(string(out))
}

// isCommitInvocation reports whether these argv words run `git commit`.
//
// The counter lives here rather than in Commit because Git is exported and a
// test with an unusual need (an amend, a specific author, an empty commit) will
// reach for it directly. If only Commit advanced the clock, that test's commit
// and the next one would carry the same timestamp -- still deterministic, but no
// longer ordered, and "the later commit is dated later" is a thing fixtures
// asserting on history reasonably expect.
func isCommitInvocation(args []string) bool {
	for i := 0; i < len(args); i++ {
		// Separate-word forms of git's value-taking global options: the word
		// after one of these is that option's value, not the subcommand. The
		// `--opt=value` forms need no entry here, because they start with `-`
		// and the check below skips them. `--exec-path` is deliberately absent:
		// its value is optional, so bare `git --exec-path` takes no word.
		switch args[i] {
		case "-c", "-C", "--git-dir", "--work-tree", "--namespace",
			"--super-prefix", "--attr-source", "--config-env":
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i] == "commit"
	}
	return false
}

// Commit stages everything and records one commit, dated by commitDate rather
// than by the wall clock.
func (v *Vault) Commit(t *testing.T, message string) string {
	t.Helper()
	v.Git(t, "add", "-A")
	v.Git(t, "commit", "-q", "-m", message)
	return v.Head(t)
}

// commitDate is the date the n-th commit in a fixture vault carries: Epoch, one
// second per commit.
//
// A commit hash covers its timestamp, so a fixture committing at the wall clock
// produces a different history on every run. That is not only an aesthetic
// problem. Two vaults seeded from identical content and committed a
// millisecond apart hash the same or differently depending on whether the pair
// happened to straddle a second boundary, which is a test that passes all day
// and fails once an hour. Pinning the clock makes a pair's starting history
// *deterministically* identical, which is also what two replicas of one vault
// are supposed to look like.
func commitDate(n int) string {
	return Epoch.Add(time.Duration(n) * time.Second).Format(time.RFC3339)
}

// Head returns the current commit hash.
func (v *Vault) Head(t *testing.T) string {
	t.Helper()
	return v.Git(t, "rev-parse", "HEAD")
}

// Dirty returns the porcelain status lines, empty when the working tree is
// clean. It is how a test asserts an operation left nothing behind.
func (v *Vault) Dirty(t *testing.T) []string {
	t.Helper()
	out := v.Git(t, "status", "--porcelain=v1")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// LoadConfig loads this vault's brainnotes.toml the way `bn --vault` does, with
// the environment explicitly ignored so the fixture's configuration is what is
// loaded even if a caller left $BNOTES_VAULT pointing elsewhere.
func (v *Vault) LoadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{
		VaultFlag: v.Root,
		Getenv:    func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("%s: load fixture config: %v", v.Name, err)
	}
	return cfg
}

// gitEnv is the fixed environment every fixture git call runs under. It is
// built rather than inherited so a developer's locale, timezone, or identity
// cannot change what a commit or a status line says.
func gitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GIT_CONFIG_GLOBAL=" + os.Getenv("GIT_CONFIG_GLOBAL"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_AUTHOR_NAME=Fixture User",
		"GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture User",
		"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	exe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available on PATH")
	}
	return exe
}

// resolveDir resolves a directory through symlinks. macOS puts every temp
// directory under /var -> /private/var, and the layers under test reach the
// same vault by different routes, so resolving once here means a later path
// comparison is comparing paths.
func resolveDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %q: %v", dir, err)
	}
	return resolved
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
