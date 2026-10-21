//go:build darwin || linux

package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	// lowDescriptorLimit is a soft RLIMIT_NOFILE whose usable lock budget lands
	// far below the 256 paths the escalation threshold used to be floored at.
	lowDescriptorLimit = 160
	// lowDescriptorNoteCount is the band that floor got wrong: fewer notes than
	// the legacy cap, so escalation was skipped, but more notes than
	// lowDescriptorLimit can hold lock descriptors for, so the commit failed
	// during lock acquisition.
	lowDescriptorNoteCount = 200
)

// lowerDescriptorLimit drops the process soft descriptor limit for the duration
// of one test. The escalation threshold is derived from the live limit, so this is
// the only way to reach the low-budget band through the production call path. It
// must not be used from a parallel test: the limit is process-wide.
func lowerDescriptorLimit(t *testing.T, limit int) {
	t.Helper()

	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Skipf("cannot read RLIMIT_NOFILE: %v", err)
	}
	if original.Cur <= uint64(limit) {
		t.Skipf("process descriptor limit is already %d, at or below the %d this test lowers to", original.Cur, limit)
	}
	lowered := original
	lowered.Cur = uint64(limit)
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered); err != nil {
		t.Skipf("cannot lower RLIMIT_NOFILE to %d: %v", limit, err)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
			t.Errorf("restore RLIMIT_NOFILE to %d: %v", original.Cur, err)
		}
	})
}

func requireLowDescriptorEscalation(t *testing.T) {
	t.Helper()

	lowerDescriptorLimit(t, lowDescriptorLimit)
	if budget := vault.MaxLockedPaths(); budget >= lowDescriptorNoteCount {
		t.Fatalf("vault.MaxLockedPaths() = %d under a %d-descriptor limit, want fewer than the %d notes in the set",
			budget, lowDescriptorLimit, lowDescriptorNoteCount)
	}
}

// TestSetFrontmatterAtomicLowDescriptorSetCommitsThroughTheVaultGuard is the
// whole-list renormalize on a process with few descriptors. The set is smaller
// than the legacy 256-note cap, so the old floored threshold reported it as
// individually lockable and the commit died on EMFILE rather than escalating to
// the single exclusive vault guard.
func TestSetFrontmatterAtomicLowDescriptorSetCommitsThroughTheVaultGuard(t *testing.T) {
	requireLowDescriptorEscalation(t)

	root := t.TempDir()
	rels, ranks := atomicSetOfSize(t, root, lowDescriptorNoteCount)

	result, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{})
	if err != nil {
		t.Fatalf("SetFrontmatterAtomic over %d notes under a %d-descriptor limit: %v", len(rels), lowDescriptorLimit, err)
	}
	if !result.Changed || result.Journal == nil {
		t.Fatalf("result = %#v, want a changed set with one journal entry", result)
	}
	if len(result.Journal.Files) != len(rels) {
		t.Fatalf("journal files = %d, want %d in the one entry", len(result.Journal.Files), len(rels))
	}
	entries, err := os.ReadDir(JournalRoot(root))
	if err != nil {
		t.Fatalf("read journal root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want 1 undoable action", len(entries))
	}
	for i, rel := range rels {
		want := "pinned: " + ranks[i] + "\n"
		if got := readFrontmatterTestFile(t, root, rel); !strings.Contains(got, want) {
			t.Fatalf("%s missing %q:\n%s", rel, want, got)
		}
	}
}

// TestSetFrontmatterAtomicLowDescriptorSetRefusesAStaleNote holds the
// all-or-nothing rule in the same band: escalating for descriptor reasons must not
// weaken the preconditions, so one note replaced after planning leaves every other
// note untouched and journals nothing.
func TestSetFrontmatterAtomicLowDescriptorSetRefusesAStaleNote(t *testing.T) {
	requireLowDescriptorEscalation(t)

	root := t.TempDir()
	rels, ranks := atomicSetOfSize(t, root, lowDescriptorNoteCount)
	before := make(map[string]string, len(rels))
	for _, rel := range rels {
		before[rel] = readFrontmatterTestFile(t, root, rel)
	}

	stale := rels[len(rels)/2]
	concurrent := "---\npinned: 99.0\n---\nSomeone else wrote it.\n"
	_, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{},
		withFrontmatterBeforeAtomicCommit(func() error {
			return os.WriteFile(filepath.Join(root, stale), []byte(concurrent), 0o644)
		}),
	)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	for _, rel := range rels {
		want := before[rel]
		if rel == stale {
			want = concurrent
		}
		if got := readFrontmatterTestFile(t, root, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want missing", err)
	}
}
