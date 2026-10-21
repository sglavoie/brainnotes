package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// TestConfigMigrateCrashHelper is the child process. os.Exit inside the hook
// skips every deferred cleanup exactly as a kill does, so the parent inspects
// the same disk a restart would see.
func TestConfigMigrateCrashHelper(t *testing.T) {
	if os.Getenv("BN_CONFIG_MIGRATE_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestConfigMigrate_LeavesNeitherFileMigratedWhenKilledBeforeCommit")
	}
	// The package's TestMain scrubs $BNOTES_VAULT out of this process, so the
	// helper points itself at the fixture the parent built rather than
	// inheriting it.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(bnotesVaultEnv, os.Getenv("BN_CONFIG_MIGRATE_CRASH_ROOT"))
	beforeConfigCommit = func() { os.Exit(configMigrateCrashExitCode) }
	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	t.Fatal("the migration committed without reaching the interruption point")
}

// Killed with both halves planned and neither written, the vault is the one the
// migration started from — and it is still migratable, because nothing the
// abandoned run left behind blocks the retry. "Run it again" is the whole
// recovery procedure.
func TestConfigMigrate_LeavesNeitherFileMigratedWhenKilledBeforeCommit(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")

	cmd := exec.Command(os.Args[0], "-test.run=TestConfigMigrateCrashHelper", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		"BN_CONFIG_MIGRATE_CRASH_HELPER=1",
		"BN_CONFIG_MIGRATE_CRASH_ROOT="+root,
	)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != configMigrateCrashExitCode {
		t.Fatalf("helper = %v, want exit %d\n%s", err, configMigrateCrashExitCode, output)
	}

	if got := readConfigFile(t, portablePath); got != legacyVaultConfig {
		t.Fatalf("an interrupted migration rewrote %s:\n%s", config.FileName, got)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("an interrupted migration created %s (stat err = %v)", config.LocalFileName, err)
	}

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate after the interruption: %v", err)
	}
	if got := readConfigFile(t, localPath); !strings.Contains(got, `editor = "vim"`) {
		t.Fatalf("the retry did not migrate open.editor:\n%s", got)
	}
	if got := readConfigFile(t, portablePath); strings.Contains(got, `editor = "vim"`) {
		t.Fatalf("the retry left open.editor in %s:\n%s", config.FileName, got)
	}
}

func TestConfigMigrate_RejectsArguments(t *testing.T) {
	configVault(t, legacyVaultConfig)

	if _, _, err := runConfig(t, "config", "migrate", "open.editor"); err == nil {
		t.Fatal("`bn config migrate open.editor` was accepted")
	}
}

// enrollVaultFixture writes the synchronization enrollment marker, which is what
// turns misplacement from a legacy shape into a fault the loader refuses.
func enrollVaultFixture(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(config.EnrollmentMarker))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir enrollment dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write enrollment marker: %v", err)
	}
}

// assertNoJournalEntries is assertNoMutationArtifacts for a vault that may
// legitimately have a .bnotes directory already: a refusal during staging must
// still leave no undo entry explaining a change that never landed.
func assertNoJournalEntries(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".bnotes", "journal"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read journal: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused migration recorded %d journal entries", len(entries))
	}
}
