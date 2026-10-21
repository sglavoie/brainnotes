package vaultfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

// TestPairVaultsAreDisposableGitRepositories is the demonstration that the pair
// really is two Git-backed vaults and not two directories: each has its own
// history, each starts clean, and both live in throwaway storage.
func TestPairVaultsAreDisposableGitRepositories(t *testing.T) {
	pair := NewPair(t, nil)

	for _, v := range []*Vault{pair.Local, pair.Peer} {
		assertDisposableGitVault(t, v)
	}
	if pair.Local.Root == pair.Peer.Root {
		t.Fatal("the pair shares one root, so nothing it observes distinguishes the two devices")
	}
	if got, want := pair.Peer.Read(t, "Alpha.md"), pair.Local.Read(t, "Alpha.md"); got != want {
		t.Fatalf("the pair was not seeded from identical content:\nlocal: %q\npeer:  %q", want, got)
	}
	// Identical content and pinned commit dates, so the two replicas start at
	// the same hash. That is the point of pinning the clock and says nothing
	// about whether they share a repository.
	if pair.Local.Head(t) != pair.Peer.Head(t) {
		t.Fatalf("the pair does not start from one deterministic history: local %s, peer %s",
			pair.Local.Head(t), pair.Peer.Head(t))
	}
	assertSeparateObjectDatabases(t, pair)
}

// assertDisposableGitVault checks the four properties that make one fixture a
// vault rather than a directory: throwaway storage, a repository, a vault root
// marker, a clean start, and the sentinel note.
func assertDisposableGitVault(t *testing.T, v *Vault) {
	t.Helper()
	if err := IsolationError(v.Root, v.Root); err != nil {
		t.Fatalf("%s is not an isolated vault: %v", v.Name, err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, ".git")); err != nil {
		t.Fatalf("%s has no Git repository: %v", v.Name, err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, config.FileName)); err != nil {
		t.Fatalf("%s is not a vault root: %v", v.Name, err)
	}
	if dirty := v.Dirty(t); len(dirty) != 0 {
		t.Fatalf("%s starts dirty: %v", v.Name, dirty)
	}
	if v.Read(t, UnrelatedNote) == "" {
		t.Fatalf("%s is missing the %s sentinel", v.Name, UnrelatedNote)
	}
}

// assertSeparateObjectDatabases is the independence claim stated where it
// actually lives. The two vaults start at the same commit hash by design, so
// the hash proves nothing; what proves it is that a commit made in one is an
// object the other cannot resolve. If it could, the pair is one repository
// reached by two names and every later synchronization assertion is vacuous.
func assertSeparateObjectDatabases(t *testing.T, pair *Pair) {
	t.Helper()
	pair.Local.Write(t, "LocalOnly.md", "---\ntitle: LocalOnly\n---\n\nOnly here.\n")
	created := pair.Local.Commit(t, "local only")

	cmd := exec.Command(pair.Peer.git, "cat-file", "-e", created+"^{commit}")
	cmd.Dir = pair.Peer.Root
	cmd.Env = gitEnv()
	if err := cmd.Run(); err == nil {
		t.Fatalf("the peer vault resolves %s, a commit made only in the local vault; they share an object database", created)
	}
	if peerHead := pair.Peer.Head(t); peerHead == created {
		t.Fatalf("the peer vault's HEAD is %s, the commit made only in the local vault", peerHead)
	}
}

// TestPairVaultsDoNotShareStorage is the isolation claim that matters for the
// synchronization sessions: content only crosses between the two vaults through
// the code under test.
func TestPairVaultsDoNotShareStorage(t *testing.T) {
	pair := NewPair(t, nil)

	pair.Local.Write(t, "Alpha.md", "---\ntitle: Alpha\n---\n\nEdited only on the local device.\n")
	pair.Local.Commit(t, "local edit")

	if body := pair.Peer.Read(t, "Alpha.md"); strings.Contains(body, "Edited only on the local device") {
		t.Fatal("an edit committed on the local vault appeared in the peer vault; the two share storage")
	}
	if dirty := pair.Peer.Dirty(t); len(dirty) != 0 {
		t.Fatalf("the peer vault went dirty while only the local vault was written: %v", dirty)
	}
}

// TestVaultUseMakesTheFixtureWhatTheProcessResolves proves the half a plain
// `cd` cannot: after Use, the vault this process resolves from the environment
// alone is the fixture, and the guard admits it.
func TestVaultUseMakesTheFixtureWhatTheProcessResolves(t *testing.T) {
	v := New(t, Options{Name: "used"})
	v.Use(t)

	resolved := AmbientVaultRoot()
	if resolved != v.Root {
		t.Fatalf("AmbientVaultRoot() = %q, want the fixture %q", resolved, v.Root)
	}
	if err := IsolationError(resolved, v.Root); err != nil {
		t.Fatalf("the fixture the process resolves is refused by its own guard: %v", err)
	}

	cfg := v.LoadConfig(t)
	if cfg.Vault != v.Root {
		t.Fatalf("LoadConfig resolved %q, want the fixture %q", cfg.Vault, v.Root)
	}
}

// TestIsolateScrubsTheAmbientVault is the regression shape stated as a fixture
// property: a fixture built while $BNOTES_VAULT names somewhere else must not
// leave that value resolvable.
func TestIsolateScrubsTheAmbientVault(t *testing.T) {
	ambient := filepath.Join(t.TempDir(), "BrainNotesVault")
	if err := os.MkdirAll(ambient, 0o755); err != nil {
		t.Fatalf("mkdir ambient vault: %v", err)
	}
	t.Setenv(EnvVault, ambient)

	t.Run("fixture", func(t *testing.T) {
		v := New(t, Options{Name: "isolated"})
		if got, ok := os.LookupEnv(EnvVault); ok {
			t.Fatalf("$%s = %q after building a fixture, want it scrubbed", EnvVault, got)
		}
		if got := AmbientVaultRoot(); got == ambient {
			t.Fatalf("AmbientVaultRoot() = %q, the ambient vault the fixture was supposed to make unreachable", got)
		}
		if err := IsolationError(v.Root, v.Root); err != nil {
			t.Fatalf("the fixture is not isolated: %v", err)
		}
	})
}

// TestWriteRefusesAPathThatLeavesTheVault covers the gap between "this root was
// proved isolated" and "this write landed in it". The root is judged once, at
// construction; a `..` in a caller's relative path is how a later write escapes
// a proof that was perfectly correct about the root.
func TestWriteRefusesAPathThatLeavesTheVault(t *testing.T) {
	v := New(t, Options{Name: "contained"})

	for _, rel := range []string{"../escaped.md", "Notes/../../escaped.md", "..", "."} {
		if err := v.containmentError(rel); err == nil {
			t.Fatalf("Write(%q) would be allowed, but it does not name a file inside %q", rel, v.Root)
		}
	}
	// And is not refusing everything: the shapes fixtures actually use pass.
	for _, rel := range []string{"Alpha.md", "Notes/Gamma.md", "a/b/c/Deep.md"} {
		if err := v.containmentError(rel); err != nil {
			t.Fatalf("Write(%q) was refused: %v", rel, err)
		}
	}
}

// TestTheFixtureRootIsJudgedBeforeItExists pins the ordering newVault relies
// on. The guard runs before os.MkdirAll, so it has to reach a verdict on a path
// with nothing at it -- a guard that could only judge existing directories
// would force the fixture to create its target before deciding it was allowed
// to, leaving behind the artifact the refusal exists to prevent.
func TestTheFixtureRootIsJudgedBeforeItExists(t *testing.T) {
	sandbox := t.TempDir()
	future := filepath.Join(sandbox, "not-created-yet")
	if _, err := os.Stat(future); !os.IsNotExist(err) {
		t.Fatalf("stat %q = %v, want it not to exist", future, err)
	}
	if err := IsolationError(future, future); err != nil {
		t.Fatalf("IsolationError refused a sandbox path that simply does not exist yet: %v", err)
	}

	outside := filepath.Join(string(filepath.Separator), "Users", "someone", "Notes", "not-created-yet")
	if err := IsolationError(outside, outside); err == nil {
		t.Fatalf("IsolationError admitted %q; a non-existent path outside throwaway storage must still be refused", outside)
	}
}

// TestTheCommitClockAdvancesForEveryCommit covers the path around Commit. Git
// is exported, so a fixture with an unusual need commits through it directly; if
// only Commit moved the clock, that commit and the next would share a timestamp
// and a history assertion ordering commits by date would see a tie.
func TestTheCommitClockAdvancesForEveryCommit(t *testing.T) {
	v := New(t, Options{Name: "clocked"})

	v.Write(t, "Direct.md", "---\ntitle: Direct\n---\n\nBody.\n")
	v.Git(t, "add", "-A")
	v.Git(t, "commit", "-q", "-m", "committed without Commit")

	v.Write(t, "ThroughCommit.md", "---\ntitle: ThroughCommit\n---\n\nBody.\n")
	v.Commit(t, "committed through Commit")

	dates := strings.Split(v.Git(t, "log", "--format=%cI", "--reverse"), "\n")
	if len(dates) != 3 {
		t.Fatalf("git log reported %d commits, want the seed plus two: %v", len(dates), dates)
	}
	want := []string{
		Epoch.Format(time.RFC3339),
		Epoch.Add(time.Second).Format(time.RFC3339),
		Epoch.Add(2 * time.Second).Format(time.RFC3339),
	}
	for i, got := range dates {
		if parsed, err := time.Parse(time.RFC3339, got); err != nil {
			t.Fatalf("commit %d has an unparseable date %q: %v", i, got, err)
		} else if !parsed.Equal(mustParse(t, want[i])) {
			t.Fatalf("commit %d is dated %s, want %s; the pinned clock did not advance", i, got, want[i])
		}
	}
}

func mustParse(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

// TestExplicitFilesReplaceTheDefaults keeps Options.Files honest: a non-nil map
// is the whole vault, so a test asserting on an empty vault really gets one.
func TestExplicitFilesReplaceTheDefaults(t *testing.T) {
	v := New(t, Options{Name: "explicit", Files: map[string]string{"Only.md": "---\ntitle: Only\n---\n\nBody.\n"}})

	if _, err := os.Stat(v.Path(UnrelatedNote)); !os.IsNotExist(err) {
		t.Fatalf("stat %s = %v, want the default files to have been replaced", UnrelatedNote, err)
	}
	if !strings.Contains(v.Read(t, "Only.md"), "Body.") {
		t.Fatal("the explicitly named file is missing")
	}
	if dirty := v.Dirty(t); len(dirty) != 0 {
		t.Fatalf("the vault starts dirty: %v", dirty)
	}
}
