package mutationmatrix

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// TestConfigMutationsWithNothingToDoLeaveNoHistory covers the config-command
// answers the matrix rows cannot express.
//
// A row's no-op runs after its prepare, and for `config unset` the prepare is
// precisely what assigns the key, so "unsetting a key that is not there" has
// nowhere to live in the table. Neither does the case a converging script
// actually produces — asserting the default against a vault that assigns
// nothing — because a row's no-op runs against a vault its own apply has
// already moved off the default. A dry run has nowhere to live either: it is
// not a mutation, and the table describes mutations.
//
// All of them are the same claim, which is the one thing a scriptable config
// surface has to get right to be safe in a loop: an invocation with nothing to
// do touches neither the file, nor the undo journal, nor the history — so a
// script that reasserts the vault's policy on every run does not produce a
// commit on every run. "Nothing to do" is measured against the policy in force,
// not against the bytes: this vault assigns no floor at all, and every call
// below names the floor it is already on.
func TestConfigMutationsWithNothingToDoLeaveNoHistory(t *testing.T) {
	h := newHarness(t, nil)
	before := h.head()
	original := h.readFile(config.FileName)

	for _, args := range [][]string{
		{"config", "unset", autosaveKey},
		{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval},
		{"config", "set", autosaveKey, "300s"},
		{"config", "set", autosaveKey, "1m", "--dry-run"},
		{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval, "--dry-run"},
		{"config", "unset", autosaveKey, "--dry-run"},
	} {
		if err := runCLI(t, h, args...); err != nil {
			t.Fatalf("bn %s: %v", strings.Join(args, " "), err)
		}
		if got := h.head(); got != before {
			t.Fatalf("`bn %s` committed %q", strings.Join(args, " "), h.subject("HEAD"))
		}
		if got := h.readFile(config.FileName); got != original {
			t.Fatalf("`bn %s` rewrote %s:\n%s", strings.Join(args, " "), config.FileName, got)
		}
	}

	// Not even the undo journal: nothing was captured, so nothing was recorded.
	if h.exists(".bnotes") {
		t.Fatalf(".bnotes exists; a command with nothing to do left artifacts: %v", h.tree())
	}
}

// TestConfigMigrateWithNothingToDoLeavesNoHistory is the same claim for the
// repair command, and it is the property that makes the repair safe to run
// without reading the vault first.
//
// A vault that was never misplaced does not fit a matrix row, whose no-op runs
// after the prepare that creates the work. Neither does a dry run over real
// work, which is not a mutation at all. Both have to be silent: a repair that
// commits on every invocation is one nobody can put in a setup script, and a
// preview that writes is not a preview.
func TestConfigMigrateWithNothingToDoLeavesNoHistory(t *testing.T) {
	h := newHarness(t, nil)

	// Every key in the fixture is portable and already in the portable file.
	before, original := h.head(), h.readFile(config.FileName)
	if err := runCLI(t, h, "config", "migrate"); err != nil {
		t.Fatalf("bn config migrate on an already-correct vault: %v", err)
	}
	assertConfigPairUntouched(t, h, before, original, "`bn config migrate` with nothing to move")
	if h.exists(".bnotes") {
		t.Fatalf(".bnotes exists; a command with nothing to do left artifacts: %v", h.tree())
	}

	// Now give it real work, and check the dry run stays silent about it.
	misplaceMachineLocalKey(t, h)
	before, original = h.head(), h.readFile(config.FileName)
	if err := runCLI(t, h, "config", "migrate", "--dry-run"); err != nil {
		t.Fatalf("bn config migrate --dry-run: %v", err)
	}
	assertConfigPairUntouched(t, h, before, original, "`bn config migrate --dry-run`")
}

// TestConfigMigrateIsIdempotent covers the no-op the matrix row cannot express
// either, because that row's prepare is what creates the work: the same command
// a second time, against the vault its own first run repaired.
//
// It is the property an interrupted run is recovered by. Re-running the repair
// has to be the safe thing to do without first working out whether the previous
// run got far enough to write anything.
func TestConfigMigrateIsIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	misplaceMachineLocalKey(t, h)
	before := h.head()

	if err := runCLI(t, h, "config", "migrate"); err != nil {
		t.Fatalf("bn config migrate: %v", err)
	}
	migrated := h.head()
	if migrated == before {
		t.Fatal("a migration with work to do should have committed")
	}
	portable, overlay := h.readFile(config.FileName), h.readFile(config.LocalFileName)
	assertKeyMovedToTheOverlay(t, h, portable, overlay)

	if err := runCLI(t, h, "config", "migrate"); err != nil {
		t.Fatalf("bn config migrate a second time: %v", err)
	}
	if got := h.head(); got != migrated {
		t.Fatalf("a second `bn config migrate` committed %q", h.subject("HEAD"))
	}
	if got := h.readFile(config.FileName); got != portable {
		t.Fatalf("a second migration rewrote %s:\n%s", config.FileName, got)
	}
	if got := h.readFile(config.LocalFileName); got != overlay {
		t.Fatalf("a second migration rewrote %s:\n%s", config.LocalFileName, got)
	}
}

// misplaceMachineLocalKey commits a key in the file that does not own it.
// open.editor names this machine, so the portable file is the wrong file for it
// — legal here only because this vault is not enrolled in synchronization.
// Committing it means what follows measures the migration and not the edit that
// made one necessary.
func misplaceMachineLocalKey(t *testing.T, h *harness) {
	t.Helper()
	h.writeFile(config.FileName, fixtureConfig+"\n[open]\neditor = \"hx\"\n")
	h.rawGit("add", "-A")
	h.rawGit("commit", "-q", "-m", "misplace a machine-local key")
}

// assertConfigPairUntouched states what "nothing to do" means for a command that
// writes two files: neither file moved, and the overlay was not conjured into
// existence by a run that had no reason to create it.
func assertConfigPairUntouched(t *testing.T, h *harness, before, original, what string) {
	t.Helper()
	if got := h.head(); got != before {
		t.Fatalf("%s committed %q", what, h.subject("HEAD"))
	}
	if got := h.readFile(config.FileName); got != original {
		t.Fatalf("%s rewrote %s:\n%s", what, config.FileName, got)
	}
	if h.exists(config.LocalFileName) {
		t.Fatalf("%s created %s", what, config.LocalFileName)
	}
}

func assertKeyMovedToTheOverlay(t *testing.T, h *harness, portable, overlay string) {
	t.Helper()
	if strings.Contains(portable, "editor") {
		t.Fatalf("%s still assigns the moved key:\n%s", config.FileName, portable)
	}
	if !strings.Contains(overlay, `editor = "hx"`) {
		t.Fatalf("%s did not receive the moved assignment:\n%s", config.LocalFileName, overlay)
	}
	// The file the migration created stays out of the content history. It is the
	// one file at the vault root that deliberately does not describe the vault,
	// and the commit that moved the key into it must not carry it along.
	if _, tracked := h.show("HEAD", config.LocalFileName); tracked {
		t.Fatalf("%s reached the content history", config.LocalFileName)
	}
}

// The same claim for a vault that already assigns the floor, in each of the
// TOML encodings that resolve to it.
//
// TestConfigMutationsWithNothingToDoLeaveNoHistory runs against a vault with no
// assignment at all, so it cannot see the case this covers: a stored value the
// loader reads as `5m` while the assignment's own bytes spell it some other
// legal way. Judging the no-op on those bytes made `bn config set
// autosave.min_interval 5m` a real mutation against every one of these vaults,
// which is a commit on every run of a script that only ever asserts the floor
// it is already on.
func TestConfigSetLeavesNoHistoryForAnEquivalentEncodingOfTheStoredFloor(t *testing.T) {
	for _, tc := range []struct{ name, stored string }{
		{"a triple-quoted basic string", "[autosave]\nmin_interval = \"\"\"5m\"\"\"\n"},
		{"a triple-quoted literal string", "[autosave]\nmin_interval = '''300s'''\n"},
		{"a triple-quoted string opened by a newline", "[autosave]\nmin_interval = \"\"\"\n5m0s\"\"\"\n"},
		{"a Unicode escape", "[autosave]\nmin_interval = \"5\\u006d\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.writeFile(config.FileName, fixtureConfig+"\n"+tc.stored)
			h.rawGit("add", "-A")
			h.rawGit("commit", "-q", "-m", "store the floor")
			before := h.head()
			original := h.readFile(config.FileName)

			for _, args := range [][]string{
				{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval},
				{"config", "set", autosaveKey, "300s"},
				{"config", "set", autosaveKey, "5m0s"},
				{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval, "--dry-run"},
			} {
				if err := runCLI(t, h, args...); err != nil {
					t.Fatalf("bn %s: %v", strings.Join(args, " "), err)
				}
				if got := h.head(); got != before {
					t.Fatalf("`bn %s` committed %q", strings.Join(args, " "), h.subject("HEAD"))
				}
				if got := h.readFile(config.FileName); got != original {
					t.Fatalf("`bn %s` rewrote %s:\n%s", strings.Join(args, " "), config.FileName, got)
				}
			}
			if h.exists(".bnotes") {
				t.Fatalf(".bnotes exists; a command with nothing to do left artifacts: %v", h.tree())
			}

			// The floor that genuinely moves is still one ordinary mutation, and it
			// replaces the whole stored value rather than the first line of it.
			if err := runCLI(t, h, "config", "set", autosaveKey, "600s"); err != nil {
				t.Fatalf("bn config set 600s: %v", err)
			}
			if h.head() == before {
				t.Fatal("moving the floor should have committed")
			}
			if got := h.readFile(config.FileName); got != fixtureConfig+"\n[autosave]\nmin_interval = \"10m\"\n" {
				t.Fatalf("%s =\n%q\nwant one canonical assignment", config.FileName, got)
			}
		})
	}
}

// TestConfigSetRecordsTheImmediateCommitUnderItsOwnOperation pins the half of
// the lifecycle the matrix rows measure only by shape. The rows prove exactly
// one commit of exactly brainnotes.toml; this proves that commit is the
// explicit, immediate one these commands are supposed to make, recorded under
// the fixed operation token rather than under anything derived from the value
// the user typed.
func TestConfigSetRecordsTheImmediateCommitUnderItsOwnOperation(t *testing.T) {
	h := newHarness(t, nil)

	if err := runCLI(t, h, "config", "set", autosaveKey, "90s"); err != nil {
		t.Fatalf("bn config set: %v", err)
	}
	if subject := h.subject("HEAD"); !strings.Contains(subject, "config-set") {
		t.Fatalf("the commit subject is %q, want it to name the config-set operation", subject)
	}
	if got := h.readFile(config.FileName); !strings.Contains(got, `min_interval = "1m30s"`) {
		t.Fatalf("%s does not hold the canonical value:\n%s", config.FileName, got)
	}

	if err := runCLI(t, h, "config", "unset", autosaveKey); err != nil {
		t.Fatalf("bn config unset: %v", err)
	}
	if subject := h.subject("HEAD"); !strings.Contains(subject, "config-unset") {
		t.Fatalf("the commit subject is %q, want it to name the config-unset operation", subject)
	}
	// The token is the whole commit subject's vocabulary on purpose: the value
	// the user passed is note-adjacent user input and belongs in the local undo
	// journal, not in a message every clone of the vault carries.
	for _, leaked := range []string{"90s", "1m30s", autosaveKey} {
		if subject := h.subject("HEAD~1"); strings.Contains(subject, leaked) {
			t.Fatalf("the commit subject %q leaked %q", subject, leaked)
		}
	}
}
