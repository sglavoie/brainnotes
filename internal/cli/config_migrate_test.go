package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// legacyVaultConfig is the shape every vault predating the overlay has: one
// portable file carrying keys that name this machine. It is what `bn config
// migrate` exists to repair, so it is the fixture nearly everything here runs
// against.
const legacyVaultConfig = `[frontmatter.properties.tags]
type  = "list"
items = "string"

[open]
# The editor this machine opens notes in.
editor = "vim"

[serve]
addr = "127.0.0.1:8080"

[capture]
inbox_note = "Inbox.md"
`

// migrateVault builds a vault holding portable, plus an overlay when local is
// non-empty, and returns the root and the two paths.
func migrateVault(t *testing.T, portable, local string) (root, portablePath, localPath string) {
	t.Helper()
	root, portablePath = configVault(t, portable)
	localPath = filepath.Join(root, config.LocalFileName)
	if local != "" {
		writeLocalOverlay(t, root, local)
	}
	return root, portablePath, localPath
}

func TestConfigMigrate_MovesEachLocalKeyIntoTheOverlay(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")

	out, _, err := runConfig(t, "config", "migrate")
	if err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	want := "open.editor: " + config.FileName + " -> " + config.LocalFileName + "\n" +
		"serve.addr: " + config.FileName + " -> " + config.LocalFileName + "\n"
	if out != want {
		t.Fatalf("config migrate printed\n%q\nwant\n%q", out, want)
	}

	portable := readConfigFile(t, portablePath)
	for _, gone := range []string{`editor = "vim"`, `addr = "127.0.0.1:8080"`} {
		if strings.Contains(portable, gone) {
			t.Fatalf("%s still assigns %s:\n%s", config.FileName, gone, portable)
		}
	}
	// The tables the keys left, and the comments around them, stay where they
	// were: migration moves assignments, not sections.
	for _, kept := range []string{"[open]", "# The editor this machine opens notes in.", "[serve]", `inbox_note = "Inbox.md"`} {
		if !strings.Contains(portable, kept) {
			t.Fatalf("%s lost %q:\n%s", config.FileName, kept, portable)
		}
	}

	overlay := readConfigFile(t, localPath)
	for _, moved := range []string{"[open]", `editor = "vim"`, "[serve]", `addr = "127.0.0.1:8080"`} {
		if !strings.Contains(overlay, moved) {
			t.Fatalf("%s does not carry %q:\n%s", config.LocalFileName, moved, overlay)
		}
	}
	assertJournalEntries(t, root, 1)
}

// What the vault does is the thing that must not move. The planner refuses a
// migration that would change any effective value, and this is the same claim
// measured from outside: the loaded configuration is identical afterwards,
// except for the provenance of the keys that moved.
func TestConfigMigrate_LeavesEveryEffectiveValueWhereItWas(t *testing.T) {
	migrateVault(t, legacyVaultConfig, "")
	before := loadConfigFixture(t)

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	after := loadConfigFixture(t)

	if after.Open.Editor != before.Open.Editor || after.Open.Editor != "vim" {
		t.Fatalf("open.editor = %q, want %q", after.Open.Editor, before.Open.Editor)
	}
	if after.Serve.Addr != before.Serve.Addr || after.Serve.Addr != "127.0.0.1:8080" {
		t.Fatalf("serve.addr = %q, want %q", after.Serve.Addr, before.Serve.Addr)
	}
	if after.Capture.InboxNote != before.Capture.InboxNote {
		t.Fatalf("capture.inbox_note = %q, want %q", after.Capture.InboxNote, before.Capture.InboxNote)
	}

	for key, want := range map[string]config.Layer{
		"open.editor":        config.LayerLocal,
		"serve.addr":         config.LayerLocal,
		"capture.inbox_note": config.LayerPortable,
	} {
		got, ok := after.SourceOf(key)
		if !ok {
			t.Fatalf("%s is assigned by no file after the migration", key)
		}
		if got != want {
			t.Fatalf("%s now comes from %q, want %q", key, got, want)
		}
	}
}

// A repair that is not safe to re-run is a repair nobody can put in a script.
func TestConfigMigrate_IsIdempotent(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("first config migrate: %v", err)
	}
	portable, overlay := readConfigFile(t, portablePath), readConfigFile(t, localPath)

	out, _, err := runConfig(t, "config", "migrate")
	if err != nil {
		t.Fatalf("second config migrate: %v", err)
	}
	if !strings.Contains(out, "Nothing to migrate") {
		t.Fatalf("a second migration printed %q, want the nothing-to-do answer", out)
	}
	if got := readConfigFile(t, portablePath); got != portable {
		t.Fatalf("a second migration rewrote %s:\n%s", config.FileName, got)
	}
	if got := readConfigFile(t, localPath); got != overlay {
		t.Fatalf("a second migration rewrote %s:\n%s", config.LocalFileName, got)
	}
	// One entry, from the first run. A no-op records nothing, so it cannot put a
	// commit on a vault that was already correct.
	assertJournalEntries(t, root, 1)
}

// A vault already in the right shape is the other no-op, and it must not create
// an overlay it does not need: an absent overlay and an empty one are different
// states all the way into the configuration fingerprint.
func TestConfigMigrate_LeavesACorrectVaultUntouched(t *testing.T) {
	root, cfgPath := configVault(t, commentedAutosaveConfig)

	out, _, err := runConfig(t, "config", "migrate")
	if err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if !strings.Contains(out, "Nothing to migrate") || !strings.Contains(out, root) {
		t.Fatalf("output = %q, want the nothing-to-do answer naming the vault", out)
	}
	if got := readConfigFile(t, cfgPath); got != commentedAutosaveConfig {
		t.Fatalf("a no-op migration rewrote the file:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, config.LocalFileName)); !os.IsNotExist(err) {
		t.Fatalf("a no-op migration created an overlay (stat err = %v)", err)
	}
	assertNoMutationArtifacts(t, root)
}

func TestConfigMigrate_DryRunPreviewsBothFilesAndWritesNothing(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")

	out, _, err := runConfig(t, "config", "migrate", "--dry-run")
	if err != nil {
		t.Fatalf("config migrate --dry-run: %v", err)
	}
	for _, want := range []string{
		"open.editor: " + config.FileName + " -> " + config.LocalFileName,
		"serve.addr: " + config.FileName + " -> " + config.LocalFileName,
		"--- " + config.FileName,
		`- 7: editor = "vim"`,
		`- 10: addr = "127.0.0.1:8080"`,
		"--- " + config.LocalFileName,
		"+ 1: # Machine-local BrainNotes configuration.",
		`+ 8: editor = "vim"`,
		`+ 11: addr = "127.0.0.1:8080"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output does not contain %q:\n%s", want, out)
		}
	}
	if got := readConfigFile(t, portablePath); got != legacyVaultConfig {
		t.Fatalf("a dry run rewrote %s:\n%s", config.FileName, got)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("a dry run created the overlay (stat err = %v)", err)
	}
	assertNoMutationArtifacts(t, root)
}

// The vault this command exists for is the one no other command can run on: an
// enrolled vault whose keys are misplaced is refused by the loader itself.
// Putting the repair behind a successful load would put it behind the fault it
// repairs.
func TestConfigMigrate_RepairsAnEnrolledVaultTheLoaderRefuses(t *testing.T) {
	root, _ := configVault(t, legacyVaultConfig)
	enrollVaultFixture(t, root)

	if _, _, err := runConfig(t, "config", "get", "autosave.min_interval"); err == nil {
		t.Fatal("the loader accepted an enrolled vault with misplaced keys")
	} else if !strings.Contains(err.Error(), "belong in "+config.LocalFileName) {
		t.Fatalf("loader error = %q, want the misplacement refusal", err)
	}

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate on an enrolled vault: %v", err)
	}
	if _, _, err := runConfig(t, "config", "get", "autosave.min_interval"); err != nil {
		t.Fatalf("the migrated vault still does not load: %v", err)
	}
}

// A configuration the loader rejects for any reason other than misplacement is
// not something to migrate: the planner has to read what the vault currently
// means, and it cannot read a file that does not parse.
func TestConfigMigrate_RefusesAConfigurationItCannotRead(t *testing.T) {
	const broken = "[autosave]\nmin_interval = \"soon\"\n"
	root, cfgPath := configVault(t, broken)

	_, _, err := runConfig(t, "config", "migrate")
	if err == nil {
		t.Fatal("a configuration that does not load was migrated")
	}
	for _, want := range []string{"does not load", "cannot be migrated", "fix it first"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, cfgPath); got != broken {
		t.Fatalf("a refused migration rewrote the file:\n%s", got)
	}
	assertNoMutationArtifacts(t, root)
}

// The originals are recoverable, which is the other half of "safe to run": a
// migration that turns out to be unwanted is one `bn undo` away from the file
// pair it replaced.
func TestConfigMigrate_IsUndoable(t *testing.T) {
	_, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if out, err := runCLI(t, "", "undo"); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	if got := readConfigFile(t, portablePath); got != legacyVaultConfig {
		t.Fatalf("undo did not restore %s:\n%s", config.FileName, got)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("undo left the created overlay behind (stat err = %v)", err)
	}
}

// The plan is computed from bytes on disk, and it is only valid against those
// bytes. Another writer landing between the read and the commit has to become a
// refusal — a migration is a whole-file rewrite, so an overwrite here would
// discard their edit entirely.
func TestConfigMigrate_RefusesWhenTheFileChangedUnderIt(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")
	concurrent := legacyVaultConfig + "\n[autosave]\nmin_interval = \"1h\"\n"
	stubBeforeConfigCommit(t, func() {
		if err := os.WriteFile(portablePath, []byte(concurrent), 0o644); err != nil {
			t.Fatalf("simulate a concurrent writer: %v", err)
		}
	})

	_, _, err := runConfig(t, "config", "migrate")
	if err == nil {
		t.Fatal("a stale migration was committed over a concurrent edit")
	}
	for _, want := range []string{"changed on disk", "nothing was written", "re-run it"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if got := readConfigFile(t, portablePath); got != concurrent {
		t.Fatalf("the other writer's file was overwritten:\n%s", got)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("a refused migration still created the overlay (stat err = %v)", err)
	}
	assertNoJournalEntries(t, root)
}

// The same race on the other file, and the one only an expectation of *absence*
// catches: an overlay that appeared under a migration that was going to create
// it belongs to somebody else, and writing over it would destroy it.
func TestConfigMigrate_RefusesWhenAnOverlayAppearedUnderIt(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")
	const concurrent = "[open]\neditor = \"hx\"\n"
	stubBeforeConfigCommit(t, func() {
		if err := os.WriteFile(localPath, []byte(concurrent), 0o644); err != nil {
			t.Fatalf("simulate a concurrent writer: %v", err)
		}
	})

	_, _, err := runConfig(t, "config", "migrate")
	if err == nil {
		t.Fatal("a migration clobbered an overlay that appeared under it")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("error = %q, want the conflict refusal", err)
	}
	if got := readConfigFile(t, localPath); got != concurrent {
		t.Fatalf("the other writer's overlay was overwritten:\n%s", got)
	}
	if got := readConfigFile(t, portablePath); got != legacyVaultConfig {
		t.Fatalf("half the migration landed:\n%s", got)
	}
	assertNoJournalEntries(t, root)
}

// Migration moves keys in both directions: a portable key parked in the overlay
// never reaches the vault it describes, which is the failure the other direction
// cannot cause.
func TestConfigMigrate_MovesAPortableKeyOutOfTheOverlay(t *testing.T) {
	root, portablePath, localPath := migrateVault(t, commentedAutosaveConfig,
		"[open]\neditor = \"vim\"\n\n[capture]\ninbox_note = \"Later.md\"\n")

	out, _, err := runConfig(t, "config", "migrate")
	if err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	want := "capture.inbox_note: " + config.LocalFileName + " -> " + config.FileName +
		", replacing the assignment it was already shadowing\n"
	if out != want {
		t.Fatalf("config migrate printed %q, want %q", out, want)
	}
	if got := readConfigFile(t, localPath); strings.Contains(got, "inbox_note") {
		t.Fatalf("%s still assigns capture.inbox_note:\n%s", config.LocalFileName, got)
	}
	if got := readConfigFile(t, localPath); !strings.Contains(got, `editor = "vim"`) {
		t.Fatalf("%s lost the key it owns:\n%s", config.LocalFileName, got)
	}
	if got := readConfigFile(t, portablePath); !strings.Contains(got, `inbox_note = "Later.md"`) {
		t.Fatalf("%s did not receive capture.inbox_note:\n%s", config.FileName, got)
	}
	// The overlay was winning before the move, so the value that survives is the
	// overlay's and not the one the portable file already had.
	if got := loadConfigFixture(t).Capture.InboxNote; got != "Later.md" {
		t.Fatalf("capture.inbox_note = %q after the migration, want the value that was in force", got)
	}
	assertJournalEntries(t, root, 1)
}

// A key both files assign has only ever had one value: the overlay's. The
// migration keeps that one and drops the dead assignment, because leaving the
// key bound twice in one file is a configuration that stops loading and
// refusing outright would strand the vault `bn config set` sends here.
func TestConfigMigrate_DropsAShadowedAssignmentRatherThanDuplicatingIt(t *testing.T) {
	_, portablePath, localPath := migrateVault(t, legacyVaultConfig, "[open]\neditor = \"hx\"\n")
	if got := loadConfigFixture(t).Open.Editor; got != "hx" {
		t.Fatalf("fixture premise: open.editor = %q, want the overlay to be winning", got)
	}

	out, _, err := runConfig(t, "config", "migrate")
	if err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	want := "open.editor: dropped from " + config.FileName + "; " + config.LocalFileName +
		" already assigned it and that is the value in force\n" +
		"serve.addr: " + config.FileName + " -> " + config.LocalFileName + "\n"
	if out != want {
		t.Fatalf("config migrate printed\n%q\nwant\n%q", out, want)
	}

	if got := readConfigFile(t, portablePath); strings.Contains(got, "editor =") {
		t.Fatalf("%s still assigns open.editor:\n%s", config.FileName, got)
	}
	overlay := readConfigFile(t, localPath)
	if strings.Contains(overlay, `"vim"`) {
		t.Fatalf("the shadowed value was copied into the overlay:\n%s", overlay)
	}
	if got := loadConfigFixture(t).Open.Editor; got != "hx" {
		t.Fatalf("open.editor = %q after the migration, want the value that was in force", got)
	}
}

// A move takes lines out of one file and puts them into the other, so the two
// writes only mean anything together. This is the structural half of "survives
// interruption": at the last moment before the commit — both files planned,
// nothing applied — neither destination has been touched, so there is no window
// in which one file is migrated and the other is not. What an interruption
// *after* that point leaves behind is decided by the transaction's fsynced
// intent record, which internal/vault proves at every durable transition in
// TestFileTxnRecoversFromEveryDurableTransition.
func TestConfigMigrate_StagesBothFilesBeforeTouchingEither(t *testing.T) {
	_, portablePath, localPath := migrateVault(t, legacyVaultConfig, "")
	staged := false
	stubBeforeConfigCommit(t, func() {
		staged = true
		if got := readConfigFile(t, portablePath); got != legacyVaultConfig {
			t.Errorf("%s was rewritten before the commit:\n%s", config.FileName, got)
		}
		if _, err := os.Stat(localPath); !os.IsNotExist(err) {
			t.Errorf("%s existed before the commit (stat err = %v)", config.LocalFileName, err)
		}
	})

	if _, _, err := runConfig(t, "config", "migrate"); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if !staged {
		t.Fatal("the migration committed without passing the staging boundary")
	}
}

// configMigrateCrashExitCode is how the helper below reports that it reached the
// interruption point, distinct from any exit code the test binary itself uses.
const configMigrateCrashExitCode = 7
