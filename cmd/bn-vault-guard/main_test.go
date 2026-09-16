package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// The guard's startup and its half of live reload.
//
// internal/vaultgit is tested against an injected policy source because it is
// deliberately ignorant of the file format. These are the tests of the thing
// that supplies it: a real brainnotes.toml on disk, read and decoded the way the
// running process reads and decodes it.

// newVault writes a vault whose brainnotes.toml holds the given autosave
// spelling, and points $BNOTES_VAULT at it.
func newVault(t *testing.T, autosave string) string {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("BNOTES_VAULT", root)
	writeConfig(t, root, "[autosave]\nmin_interval = \""+autosave+"\"\n")
	return root
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.FileName, err)
	}
}

func loadVault(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// initGit turns the vault into one the engine will watch, or skips.
func initGit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.invalid"},
	} {
		runGit(t, root, args...)
	}
}

// runGit runs git the way a person would, and skips the test on a machine that
// has none rather than failing it.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	exe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available on PATH")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// tracked lists every path the vault's history tip holds.
func tracked(t *testing.T, root string) []string {
	t.Helper()
	out := runGit(t, root, "ls-tree", "-r", "--name-only", "--full-tree", "HEAD")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func trackedContains(t *testing.T, root, want string) bool {
	t.Helper()
	for _, path := range tracked(t, root) {
		if path == want {
			return true
		}
	}
	return false
}

// --- policySource -------------------------------------------------------------

// The ordinary case: valid bytes in, a fingerprint and a resolved policy out.
func TestPolicySourceReportsTheResolvedPolicy(t *testing.T) {
	root := newVault(t, "10m")
	source := policySource(loadVault(t))

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if snapshot.Policy.Canonical != "10m" {
		t.Fatalf("resolved %q, want %q", snapshot.Policy.Canonical, "10m")
	}
	if snapshot.Policy.Disabled {
		t.Fatal("a duration resolved as disabled")
	}
	if snapshot.Hash == "" {
		t.Fatal("a readable file produced no fingerprint")
	}

	// The fingerprint is of the file, so an edit that changes the policy changes
	// it — that is what makes the watcher act at all.
	writeConfig(t, root, "[autosave]\nmin_interval = \"off\"\n")
	next, err := source()
	if err != nil {
		t.Fatalf("source after edit: %v", err)
	}
	if !next.Policy.Disabled || next.Policy.Canonical != config.AutosaveOff {
		t.Fatalf("resolved %+v, want off", next.Policy)
	}
	if next.Hash == snapshot.Hash {
		t.Fatal("two different files share a fingerprint")
	}

	// And an edit that does not touch the meaning still changes it, which the
	// watcher absorbs by comparing policies rather than trusting the hash alone.
	writeConfig(t, root, "# a comment\n[autosave]\nmin_interval = \"off\"\n")
	third, err := source()
	if err != nil {
		t.Fatalf("source after comment: %v", err)
	}
	if third.Hash == next.Hash {
		t.Fatal("a changed file kept its fingerprint")
	}
	if third.Policy != next.Policy {
		t.Fatalf("a comment changed the policy: %+v", third.Policy)
	}
}

// An unparseable value is refused, and the snapshot still carries the hash so
// the watcher can tell one broken version from the next.
func TestPolicySourceFingerprintsAnInvalidVersion(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	writeConfig(t, root, "[autosave]\nmin_interval = \"5 minutes\"\n")
	first, err := source()
	if err == nil {
		t.Fatal("an unparseable interval was accepted")
	}
	if first.Hash == "" {
		t.Fatal("an invalid version has no fingerprint, so it would be reported on every poll")
	}
	if first.Policy != (config.AutosavePolicy{}) {
		t.Fatalf("a refused version still reported a policy: %+v", first.Policy)
	}

	// Polling the same broken file twice yields the same fingerprint, which is
	// exactly the property the once-per-version diagnostic depends on.
	again, err := source()
	if err == nil {
		t.Fatal("the second poll accepted what the first refused")
	}
	if again.Hash != first.Hash {
		t.Fatal("the same bytes produced two fingerprints")
	}

	// The whole file is validated, not just [autosave]: a version `bn` would
	// refuse is one the guard refuses too.
	writeConfig(t, root, "[autosave]\nmin_interval = \"5m\"\nnot_a_key = 1\n")
	if _, err := source(); err == nil {
		t.Fatal("a config with an unknown key was accepted")
	}

	// Below the minimum is refused for the same reason it is in `bn`: a floor
	// under 30s is a commit per keystroke.
	writeConfig(t, root, "[autosave]\nmin_interval = \"1s\"\n")
	if _, err := source(); err == nil {
		t.Fatal("an interval under the minimum was accepted")
	}
}

// With no bytes there is nothing to fingerprint, and saying so is what lets the
// watcher fall back to the diagnostic for deduplication.
func TestPolicySourceReportsAnUnreadableFileWithoutAFingerprint(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	if err := os.Remove(filepath.Join(root, config.FileName)); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	snapshot, err := source()
	if err == nil {
		t.Fatal("a missing config was accepted")
	}
	if snapshot.Hash != "" {
		t.Fatalf("unreadable bytes produced fingerprint %q", snapshot.Hash)
	}
	if !strings.Contains(err.Error(), config.FileName) {
		t.Fatalf("the diagnostic does not name the file: %v", err)
	}
}

// The vault a guard watches is fixed by the single-instance lock it holds inside
// it. An environment that changes under the running process must not silently
// move it to another vault's configuration.
func TestPolicySourceStaysOnTheVaultResolvedAtStartup(t *testing.T) {
	newVault(t, "10m")
	source := policySource(loadVault(t))

	other := t.TempDir()
	writeConfig(t, other, "[autosave]\nmin_interval = \"off\"\n")
	t.Setenv("BNOTES_VAULT", other)

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if snapshot.Policy.Canonical != "10m" {
		t.Fatalf("the source followed $BNOTES_VAULT to %q", snapshot.Policy.Canonical)
	}
}

// The hash is the configuration's own composite fingerprint, so the value the
// guard dedups on is the one the rest of the system would compute for the same
// vault — across both layers, since the guard acts on the merged policy.
func TestPolicySourceFingerprintIsTheCompositeConfigFingerprint(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	sources, err := config.ReadSources(root)
	if err != nil {
		t.Fatalf("read sources: %v", err)
	}
	if want := sources.Fingerprint().Composite; snapshot.Hash != want {
		t.Fatalf("fingerprint %q, want %q", snapshot.Hash, want)
	}
}

// The guard's dedup key is what decides whether a changed policy is ever
// adopted, so it has to move when the overlay moves. `autosave.min_interval` is
// a portable key, but an unenrolled vault — every vault today — may legally
// carry it in the overlay, and a guard that fingerprinted only brainnotes.toml
// would read the new floor on every poll and adopt it never.
func TestPolicySourceNoticesAnOverlayOnlyChange(t *testing.T) {
	root := newVault(t, "5m")
	overlay := filepath.Join(root, config.LocalFileName)
	source := policySource(loadVault(t))

	before, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if err := os.WriteFile(overlay, []byte("[autosave]\nmin_interval = \"30s\"\n"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	after, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if after.Hash == before.Hash {
		t.Error("the fingerprint ignored the overlay, so the watcher would never adopt the new floor")
	}
	if after.Policy.Canonical != "30s" {
		t.Errorf("policy = %q, want the overlay's floor", after.Policy.Canonical)
	}

	if err := os.Remove(overlay); err != nil {
		t.Fatalf("remove overlay: %v", err)
	}
	restored, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if restored.Hash != before.Hash || restored.Policy.Canonical != "5m" {
		t.Errorf("deleting the overlay did not restore the portable floor: %q %q",
			restored.Hash, restored.Policy.Canonical)
	}
}

// --- Startup ------------------------------------------------------------------
