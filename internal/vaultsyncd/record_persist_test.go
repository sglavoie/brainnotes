package vaultsyncd

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestAnIdlePassDoesNotRewriteTheCompanionRecord is the cost half of persist: a
// running companion whose passes change nothing but the run timestamps leaves
// the record — and the two full-cache flushes a replacement costs — alone until
// recordRefresh has passed, and still saves at once when the schedule moves.
//
// Every pass here runs in one companion, as Run's loop does, because the skip
// rests on what this process last wrote. Once builds a fresh companion each
// time and so always saves; that is covered by the tests that drive it.
func TestAnIdlePassDoesNotRewriteTheCompanionRecord(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())

	err := locked(w.root(), func(c *companion) error {
		c.pass(context.Background())
		first := recordFile(t, w.root())

		w.clock.Advance(DefaultInterval)
		c.pass(context.Background())
		if !os.SameFile(first, recordFile(t, w.root())) {
			t.Fatal("an idle pass inside the refresh window replaced the companion record")
		}

		w.clock.Advance(recordRefresh)
		c.pass(context.Background())
		refreshed := recordFile(t, w.root())
		if os.SameFile(first, refreshed) {
			t.Fatal("an idle pass after the refresh window did not refresh the companion record")
		}

		// A failure is a change to the schedule, and a schedule change is saved
		// in the same pass however recently the record was last written.
		corruptExportRecord(t, w.root())
		w.clock.Advance(DefaultInterval)
		c.pass(context.Background())
		if os.SameFile(refreshed, recordFile(t, w.root())) {
			t.Fatal("a failing worker's streak was not saved at once")
		}
		return nil
	}, w.options())
	if err != nil {
		t.Fatal(err)
	}
	record, err := LoadRecord(w.root())
	if err != nil || record.worker(WorkerExport).Failures != 1 {
		t.Fatalf("export failures on disk = %d, %v; want 1", record.worker(WorkerExport).Failures, err)
	}
}

// A refresh after import must not continually postpone the later workers'
// evidence. Each pass reloads the durable record, so an unsaved retire timestamp
// is otherwise lost before the next pass can include it in an import save.
func TestEveryIdleWorkerKeepsFreshDurableTimestamps(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	err := locked(w.root(), func(c *companion) error {
		for elapsed := time.Duration(0); elapsed < time.Hour; elapsed += DefaultInterval {
			pass := c.pass(context.Background())
			record, err := LoadRecord(w.root())
			if err != nil {
				t.Fatal(err)
			}
			for _, unit := range pass.Units {
				if !unit.Ran || unit.Error != "" {
					continue
				}
				worker := record.worker(unit.Worker)
				if age := pass.FinishedAt.Sub(worker.LastRunAt); age >= recordRefresh {
					t.Fatalf("%s durable last run is %v old after a successful pass", unit.Worker, age)
				}
				if age := pass.FinishedAt.Sub(worker.LastSuccessAt); age >= recordRefresh {
					t.Fatalf("%s durable last success is %v old after a successful pass", unit.Worker, age)
				}
			}
			w.clock.Advance(DefaultInterval)
		}
		return nil
	}, w.options())
	if err != nil {
		t.Fatal(err)
	}
}

// TestAnUnusableRecordIsReplacedEvenInsideTheRefreshWindow. The skip compares
// against what is on disk, and an unusable record is not a baseline: the pass
// that discarded it must write a good one rather than judge the fresh record
// the same as the one it last saved.
func TestAnUnusableRecordIsReplacedEvenInsideTheRefreshWindow(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)

	err := locked(w.root(), func(c *companion) error {
		c.pass(context.Background())
		if err := os.WriteFile(RecordPath(w.root()), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.clock.Advance(DefaultInterval)
		c.pass(context.Background())
		return nil
	}, w.options())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecord(w.root()); err != nil {
		t.Fatalf("the unusable record was left in place: %v", err)
	}
}

func recordFile(t *testing.T, root string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(RecordPath(root))
	if err != nil {
		t.Fatalf("stat the companion record: %v", err)
	}
	return info
}
