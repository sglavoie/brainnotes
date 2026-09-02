package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// This file is the verification-time counterpart of the production storage
// guard in internal/vault: that one refuses a mutation to storage `bn` must not
// write to, this one refuses a *verification* run against a vault a session must
// not touch.
//
// It exists because of a real incident. A refactoring session verified `bn
// rename-key` by running the built binary after a plain `cd` into a scratch
// directory, and $BNOTES_VAULT was set in the ambient shell to the developer's
// real vault. `cd` does not override the environment, so the equivalence matrix
// renamed a frontmatter key in 214 real notes. The notes were recovered from the
// journal, but two accidental commits and two journal entries had to be cleaned
// up afterwards by hand.
//
// The rule this file enforces is therefore the rule that incident violated: a
// mutating verification may only ever resolve an isolated temporary vault that
// the test itself created and named, and never the real vault. It is wired into
// markVaultRoot and useVaultRoot, so every CLI fixture is covered by
// construction rather than by remembering to call it, and TestMain scrubs
// $BNOTES_VAULT so an ambient value cannot be resolved by anything in this
// package in the first place.
//
// The manual half of the same rule is scripts/vault_sandbox.py, which is the
// only sanctioned way to run a mutating `bn` command by hand during
// verification. Both implement the same three refusals.

// bnotesVaultEnv is the environment variable that overrides a working directory
// when `bn` resolves its vault. Spelled here rather than imported because
// internal/config keeps it unexported.
const bnotesVaultEnv = "BNOTES_VAULT"

// realVaultDirNames are directory names that identify a real vault wherever
// they appear in a resolved path. The temp-directory rule below already refuses
// every path outside the system temporary directory; this list is the
// belt-and-braces half, so a real vault reached through a symlink or a bind
// mount under the temp directory is still refused, and so the refusal message
// names the vault that was actually damaged.
var realVaultDirNames = []string{"BrainNotesVault"}

// TestMain removes $BNOTES_VAULT from the test process before any test runs.
//
// Every helper in this package that needs a vault points the variable at its own
// fixture with t.Setenv, so nothing here has a use for the developer's ambient
// value — but a command run from a test that forgot to set one would otherwise
// resolve it, which is exactly the incident above. Scrubbing it once makes the
// unsafe resolution unreachable instead of merely discouraged.
func TestMain(m *testing.M) {
	if err := os.Unsetenv(bnotesVaultEnv); err != nil {
		fmt.Fprintf(os.Stderr, "cli: cannot scrub $%s: %v\n", bnotesVaultEnv, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// vaultIsolationError reports why resolved is not the isolated temporary vault
// the caller asserted, or nil when it is.
//
// It fails closed on every input it cannot prove safe: an unasserted sandbox and
// an unresolvable vault are both refusals, because "no evidence of isolation" is
// what the incident looked like from inside the process.
func vaultIsolationError(resolved, sandbox string) error {
	if strings.TrimSpace(sandbox) == "" {
		return errors.New("no isolated vault was asserted; a mutating verification must name the sandbox it is allowed to write to")
	}
	if strings.TrimSpace(resolved) == "" {
		return fmt.Errorf("no vault resolved, so a mutating verification cannot be proved to target the sandbox %q", sandbox)
	}
	resolvedPath, err := normalizeVaultPath(resolved)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", resolved, err)
	}
	sandboxPath, err := normalizeVaultPath(sandbox)
	if err != nil {
		return fmt.Errorf("resolve sandbox %q: %w", sandbox, err)
	}
	if err := refuseRealVault(resolvedPath); err != nil {
		return err
	}
	if err := refuseOutsideTempDir(resolvedPath, sandboxPath); err != nil {
		return err
	}
	return refuseOutsideSandbox(resolvedPath, sandboxPath)
}

// refuseRealVault refuses a path that names a real vault at any depth.
func refuseRealVault(resolved string) error {
	for _, name := range realVaultDirNames {
		if pathHasElement(resolved, name) {
			return fmt.Errorf(
				"refusing to verify against %q: it is the real vault %q, which no test or manual verification may write to",
				resolved, name)
		}
	}
	return nil
}

// refuseOutsideTempDir refuses a vault or a sandbox that is not throwaway
// storage. An isolated vault is one the run created and will delete; anything
// outside the system temporary directory outlives the verification and is
// somebody's data.
func refuseOutsideTempDir(resolved, sandbox string) error {
	tempDir, err := normalizeVaultPath(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve the system temporary directory: %w", err)
	}
	// Ordered rather than a map, so the same input always produces the same
	// refusal message.
	for _, candidate := range []struct{ label, path string }{
		{"asserted sandbox", sandbox},
		{"resolved vault", resolved},
	} {
		if !pathWithin(candidate.path, tempDir) {
			return fmt.Errorf(
				"refusing to verify against %q: the %s is outside the system temporary directory %q, so it is not an isolated vault",
				candidate.path, candidate.label, tempDir)
		}
	}
	return nil
}

// refuseOutsideSandbox refuses a vault that is isolated but is not the one the
// caller asserted. A verification that writes to a temporary vault it did not
// build proves nothing about the command under test.
func refuseOutsideSandbox(resolved, sandbox string) error {
	if !pathWithin(resolved, sandbox) {
		return fmt.Errorf(
			"refusing to verify against %q: it is outside the asserted isolated vault %q", resolved, sandbox)
	}
	return nil
}

// normalizeVaultPath makes a path comparable: absolute, symlink-free where the
// path exists, and lexically clean where it does not. macOS temp directories are
// reached through /var -> /private/var, so a comparison that skipped this would
// refuse every legitimate sandbox.
func normalizeVaultPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs), nil
	}
	return resolved, nil
}

// pathHasElement reports whether any path segment of p equals name.
func pathHasElement(p, name string) bool {
	for _, element := range strings.Split(filepath.ToSlash(p), "/") {
		if element == name {
			return true
		}
	}
	return false
}

// pathWithin reports whether child is dir itself or lives under it.
func pathWithin(child, dir string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ambientVaultRoot reports the vault root `bn` would resolve right now from
// process state alone, with no --vault flag: $BNOTES_VAULT if it is set, else
// the nearest ancestor of the working directory holding brainnotes.toml. It
// returns "" when nothing resolves.
//
// It reads process state deliberately. config.Load takes the same inputs as
// injectable options and also decodes the file, and a guard that decoded would
// mistake an intentionally invalid fixture config for an isolation failure. The
// two resolutions are pinned together by TestMutationVerificationRejectsAmbientVault.
func ambientVaultRoot() string {
	if env := strings.TrimSpace(os.Getenv(bnotesVaultEnv)); env != "" {
		return env
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// requireSandboxVaultRoot refuses a fixture root that is not throwaway storage.
// It is the check markVaultRoot can make: the root is about to become a vault,
// and whether the process will resolve it is not yet known.
func requireSandboxVaultRoot(t *testing.T, root string) {
	t.Helper()
	if err := vaultIsolationError(root, root); err != nil {
		t.Fatalf("refusing to build a vault fixture: %v", err)
	}
}

// requireIsolatedVault refuses unless the vault this process would resolve right
// now is the asserted sandbox. It is the check useVaultRoot can make, because by
// then $BNOTES_VAULT names the fixture.
func requireIsolatedVault(t *testing.T, sandbox string) {
	t.Helper()
	if err := vaultIsolationError(ambientVaultRoot(), sandbox); err != nil {
		t.Fatalf("refusing to run a mutating verification: %v", err)
	}
}

// TestMutationVerificationRejectsAmbientVault is the regression test for the
// incident described at the top of this file. It proves the guard refuses the
// exact shape that damaged the real vault — an ambient $BNOTES_VAULT pointing
// somewhere other than the sandbox the run asserted — and that it still admits a
// genuine sandbox, so the refusals are not vacuous.
func TestMutationVerificationRejectsAmbientVault(t *testing.T) {
	if got := os.Getenv(bnotesVaultEnv); got != "" {
		t.Fatalf("$%s = %q at test start; TestMain must scrub it before any test resolves a vault", bnotesVaultEnv, got)
	}

	t.Run("ambient env is what the guard reads", func(t *testing.T) {
		sandbox := t.TempDir()
		ambient := filepath.Join(t.TempDir(), "BrainNotesVault")
		if err := os.MkdirAll(ambient, 0o755); err != nil {
			t.Fatalf("mkdir ambient vault: %v", err)
		}
		markVaultRootUnguarded(t, ambient, testPropertyDefinitions)
		t.Setenv(bnotesVaultEnv, ambient)

		if got := ambientVaultRoot(); got != ambient {
			t.Fatalf("ambientVaultRoot() = %q, want the ambient $%s value %q", got, bnotesVaultEnv, ambient)
		}
		assertRefused(t, vaultIsolationError(ambientVaultRoot(), sandbox), "BrainNotesVault")

		// The guard's own resolution must not drift from the one `bn` performs.
		cfg, err := config.Load(config.LoadOptions{})
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		if cfg.Vault != ambient {
			t.Fatalf("config.Load resolved %q, guard resolved %q", cfg.Vault, ambient)
		}
	})

	t.Run("refusals", func(t *testing.T) { runVaultIsolationRefusalCases(t) })
	t.Run("a real sandbox is admitted", func(t *testing.T) { runVaultIsolationAcceptCases(t) })
}

// runVaultIsolationRefusalCases covers every way a verification can fail to be
// isolated: the real vault by name, ordinary durable storage, another sandbox,
// an unasserted sandbox, and nothing resolved at all.
func runVaultIsolationRefusalCases(t *testing.T) {
	t.Helper()
	sandbox := t.TempDir()
	// An absolute path outside the temp tree stands in for durable storage. It
	// need not exist: the guard refuses on where a path is, before anything
	// there is opened.
	durable := filepath.Join(string(filepath.Separator), "Users", "someone", "Notes")
	cases := []struct {
		name     string
		resolved string
		sandbox  string
		want     string
	}{
		{"the real vault", filepath.Join(sandbox, "BrainNotesVault"), sandbox, "BrainNotesVault"},
		{"durable storage", durable, sandbox, "outside the system temporary directory"},
		{"another sandbox", t.TempDir(), sandbox, "outside the asserted isolated vault"},
		{"a durable sandbox", sandbox, durable, "outside the system temporary directory"},
		{"no sandbox asserted", sandbox, "", "no isolated vault was asserted"},
		{"nothing resolved", "", sandbox, "no vault resolved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRefused(t, vaultIsolationError(tc.resolved, tc.sandbox), tc.want)
		})
	}
}

// runVaultIsolationAcceptCases keeps the guard honest: a sandbox the run built,
// and a vault nested inside it, are both allowed. Without these a guard that
// refused unconditionally would pass every refusal case above.
func runVaultIsolationAcceptCases(t *testing.T) {
	t.Helper()
	sandbox := t.TempDir()
	nested := filepath.Join(sandbox, "vault")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested vault: %v", err)
	}
	for _, resolved := range []string{sandbox, nested} {
		if err := vaultIsolationError(resolved, sandbox); err != nil {
			t.Fatalf("vaultIsolationError(%q, %q) = %v, want nil", resolved, sandbox, err)
		}
	}
}

// assertRefused fails unless err is a refusal whose message contains want, so a
// refusal that fires for the wrong reason is not mistaken for a passing guard.
func assertRefused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the guard admitted a vault it must refuse (expected a refusal mentioning %q)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %q, want it to mention %q", err, want)
	}
}
