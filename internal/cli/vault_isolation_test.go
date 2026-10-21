package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
)

// This file wires this package's fixtures into the shared verification-time
// isolation guard in internal/vaultfixture, which is the counterpart of the
// production storage guard in internal/vault: that one refuses a mutation to
// storage `bn` must not write to, the shared one refuses a *verification* run
// against a vault a session must not touch.
//
// The rules and their regression tests live in internal/vaultfixture because
// two-vault synchronization work and the browser harness need the same refusals
// from outside this package, and a safety rule with two Go implementations is a
// safety rule with two behaviours. What stays here is the wiring that cannot
// move: the package-wide scrub, and the checks markVaultRoot and useVaultRoot
// call, so every CLI fixture is covered by construction rather than by
// remembering to call them.
//
// The manual half of the same rule is scripts/vault_sandbox.py, the only
// sanctioned way to run a mutating `bn` command by hand during verification.

// bnotesVaultEnv is the environment variable that overrides a working directory
// when `bn` resolves its vault.
const bnotesVaultEnv = vaultfixture.EnvVault

// TestMain removes $BNOTES_VAULT from the test process before any test runs.
//
// Every helper in this package that needs a vault points the variable at its own
// fixture with t.Setenv, so nothing here has a use for the developer's ambient
// value — but a command run from a test that forgot to set one would otherwise
// resolve it, which is exactly the incident internal/vaultfixture documents.
// Scrubbing it once makes the unsafe resolution unreachable instead of merely
// discouraged.
func TestMain(m *testing.M) {
	if err := vaultfixture.ScrubProcessEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// requireSandboxVaultRoot refuses a fixture root that is not throwaway storage.
// It is the check markVaultRoot can make: the root is about to become a vault,
// and whether the process will resolve it is not yet known.
func requireSandboxVaultRoot(t *testing.T, root string) {
	t.Helper()
	if err := vaultfixture.IsolationError(root, root); err != nil {
		t.Fatalf("refusing to build a vault fixture: %v", err)
	}
}

// requireIsolatedVault refuses unless the vault this process would resolve right
// now is the asserted sandbox. It is the check useVaultRoot can make, because by
// then $BNOTES_VAULT names the fixture.
func requireIsolatedVault(t *testing.T, sandbox string) {
	t.Helper()
	if err := vaultfixture.IsolationError(vaultfixture.AmbientVaultRoot(), sandbox); err != nil {
		t.Fatalf("refusing to run a mutating verification: %v", err)
	}
}

// TestCLIFixturesAreGuardedAgainstAnAmbientVault is the regression test for the
// incident, stated as a property of *this package's* fixtures rather than of the
// predicate: with an ambient $BNOTES_VAULT naming a real vault, the guard the
// fixtures call refuses, and `bn`'s own loader confirms that ambient value is
// what it would have resolved. internal/vaultfixture owns the refusal cases
// themselves.
func TestCLIFixturesAreGuardedAgainstAnAmbientVault(t *testing.T) {
	if got := os.Getenv(bnotesVaultEnv); got != "" {
		t.Fatalf("$%s = %q at test start; TestMain must scrub it before any test resolves a vault", bnotesVaultEnv, got)
	}

	sandbox := t.TempDir()
	ambient := filepath.Join(t.TempDir(), "BrainNotesVault")
	if err := os.MkdirAll(ambient, 0o755); err != nil {
		t.Fatalf("mkdir ambient vault: %v", err)
	}
	markVaultRootUnguarded(t, ambient, testPropertyDefinitions)
	t.Setenv(bnotesVaultEnv, ambient)

	if got := vaultfixture.AmbientVaultRoot(); got != ambient {
		t.Fatalf("AmbientVaultRoot() = %q, want the ambient $%s value %q", got, bnotesVaultEnv, ambient)
	}
	err := vaultfixture.IsolationError(vaultfixture.AmbientVaultRoot(), sandbox)
	if err == nil {
		t.Fatal("the guard admitted the real vault while the run asserted a sandbox")
	}
	if !strings.Contains(err.Error(), "BrainNotesVault") {
		t.Fatalf("refusal = %q, want it to name the real vault", err)
	}

	// The guard's resolution must not drift from the one `bn` performs.
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Vault != ambient {
		t.Fatalf("config.Load resolved %q, the guard resolved %q", cfg.Vault, ambient)
	}
}

// guardProbeEnv names the fixture helper a probe subprocess should run against a
// root it must refuse. It is empty in the parent process.
const guardProbeEnv = "BN_CLI_GUARD_PROBE"

// TestTheFixtureHelpersThemselvesRefuseARealVault is the wiring test, and it is
// the one that would catch the regression that matters here.
//
// Everything else in this file asserts about vaultfixture.IsolationError — which
// internal/vaultfixture already tests, and which would keep passing if somebody
// deleted the requireSandboxVaultRoot call out of markVaultRoot. The question
// this package actually owns is whether *its* helpers still consult the guard,
// and the only way to observe that is to hand one a root it must refuse and see
// it refuse.
//
// The helpers report refusal through t.Fatalf, which a test in the same process
// cannot catch, so each probe runs in a subprocess of this test binary and the
// assertion is on that process's exit status and output. That is the standard
// shape for testing a code path that ends a test.
func TestTheFixtureHelpersThemselvesRefuseARealVault(t *testing.T) {
	if helper := os.Getenv(guardProbeEnv); helper != "" {
		runGuardProbe(t, helper)
		return
	}
	for _, helper := range []string{"markVaultRoot", "useVaultRoot"} {
		t.Run(helper, func(t *testing.T) {
			out, err := runGuardProbeSubprocess(t, helper)
			if err == nil {
				t.Fatalf("%s accepted a root named like the real vault, so the guard is not wired into it:\n%s", helper, out)
			}
			// Matched against the refusal's own wording, not against the probe
			// root's name. The probe root is deliberately called BrainNotesVault,
			// so that string appears in *any* message quoting the path — including
			// the "write brainnotes.toml" failure a helper would report if the
			// guard were deleted and the unguarded write then failed for its own
			// reasons. That would read as a pass for a guard that is gone.
			// "it is the real vault" is emitted only by IsolationError.
			if !strings.Contains(out, "it is the real vault") {
				t.Fatalf("%s failed, but not with the isolation refusal:\n%s", helper, out)
			}
		})
	}
}

// runGuardProbe is the subprocess half: it calls one helper with a root the
// guard must reject, and passing means never returning from that call.
func runGuardProbe(t *testing.T, helper string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "BrainNotesVault")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir probe root: %v", err)
	}
	switch helper {
	case "markVaultRoot":
		markVaultRoot(t, root, testPropertyDefinitions)
	case "useVaultRoot":
		useVaultRoot(t, root, testPropertyDefinitions)
	default:
		t.Fatalf("unknown probe helper %q", helper)
	}
}

func runGuardProbeSubprocess(t *testing.T, helper string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestTheFixtureHelpersThemselvesRefuseARealVault$", "-test.v")
	cmd.Env = append(os.Environ(), guardProbeEnv+"="+helper)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestCLIFixtureHelpersAdmitARealSandbox keeps the wiring from being vacuous: a
// fixture built and used the sanctioned way passes both checks, so a guard that
// refused unconditionally would not read as green here.
func TestCLIFixtureHelpersAdmitARealSandbox(t *testing.T) {
	root := t.TempDir()
	useVaultRoot(t, root, testPropertyDefinitions)

	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Vault != root {
		t.Fatalf("config.Load resolved %q, want the fixture %q", cfg.Vault, root)
	}
}
