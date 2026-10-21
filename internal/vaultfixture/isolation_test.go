package vaultfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// TestMain scrubs $BNOTES_VAULT before any test in this package runs, which is
// the same thing ScrubProcessEnv exists for other packages to do. A test here
// that needs the variable set points it at its own fixture through Vault.Use.
func TestMain(m *testing.M) {
	if err := ScrubProcessEnv(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// TestIsolationRefusals covers every way a verification can fail to be
// isolated: the real vault by name, ordinary durable storage, another sandbox,
// an unasserted sandbox, and nothing resolved at all.
func TestIsolationRefusals(t *testing.T) {
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
			assertRefused(t, IsolationError(tc.resolved, tc.sandbox), tc.want)
		})
	}
}

// TestIsolationAdmitsASandbox keeps the guard honest: a sandbox the run built,
// and a vault nested inside it, are both allowed. Without this a guard that
// refused unconditionally would pass every refusal case above.
func TestIsolationAdmitsASandbox(t *testing.T) {
	sandbox := t.TempDir()
	nested := filepath.Join(sandbox, "vault")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested vault: %v", err)
	}
	for _, resolved := range []string{sandbox, nested} {
		if err := IsolationError(resolved, sandbox); err != nil {
			t.Fatalf("IsolationError(%q, %q) = %v, want nil", resolved, sandbox, err)
		}
	}
}

// TestAmbientEnvIsWhatBnResolves is the regression test for the incident this
// guard exists for, and the pin that keeps EnvVault from drifting away from the
// name internal/config actually reads. It builds the exact shape that damaged
// the real vault — an ambient $BNOTES_VAULT naming a real vault while the run
// asserted a sandbox — and proves both that `bn`'s own loader resolves it and
// that the guard refuses it.
func TestAmbientEnvIsWhatBnResolves(t *testing.T) {
	if got := os.Getenv(EnvVault); got != "" {
		t.Fatalf("$%s = %q at test start; TestMain must scrub it before any test resolves a vault", EnvVault, got)
	}

	sandbox := t.TempDir()
	ambient := filepath.Join(t.TempDir(), "BrainNotesVault")
	if err := os.MkdirAll(ambient, 0o755); err != nil {
		t.Fatalf("mkdir ambient vault: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ambient, config.FileName), []byte(DefaultConfig), 0o644); err != nil {
		t.Fatalf("write ambient config: %v", err)
	}
	t.Setenv(EnvVault, ambient)

	if got := AmbientVaultRoot(); got != ambient {
		t.Fatalf("AmbientVaultRoot() = %q, want the ambient $%s value %q", got, EnvVault, ambient)
	}
	assertRefused(t, IsolationError(AmbientVaultRoot(), sandbox), "BrainNotesVault")

	// The guard's own resolution must not drift from the one `bn` performs.
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Vault != ambient {
		t.Fatalf("config.Load resolved %q, guard resolved %q", cfg.Vault, ambient)
	}
	if cfg.VaultOrigin != config.VaultOriginEnv {
		t.Fatalf("config.Load reported origin %q, want %q; $%s is not the name `bn` reads",
			cfg.VaultOrigin, config.VaultOriginEnv, EnvVault)
	}
}

// TestScrubEnvRestoresTheAmbientValue proves the narrow scrub is reversible, so
// a package that scrubs per-test does not silently disarm a later test that
// depends on the variable.
func TestScrubEnvRestoresTheAmbientValue(t *testing.T) {
	t.Setenv(EnvVault, "/somewhere/ambient")
	t.Run("scrubbed", func(t *testing.T) {
		ScrubEnv(t)
		if got, ok := os.LookupEnv(EnvVault); ok {
			t.Fatalf("$%s = %q after ScrubEnv, want it unset", EnvVault, got)
		}
		if got := AmbientVaultRoot(); strings.Contains(got, "ambient") {
			t.Fatalf("AmbientVaultRoot() = %q, want the scrubbed environment to be unreachable", got)
		}
	})
	if got := os.Getenv(EnvVault); got != "/somewhere/ambient" {
		t.Fatalf("$%s = %q after the subtest, want the ambient value restored", EnvVault, got)
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
