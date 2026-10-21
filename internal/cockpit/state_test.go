package cockpit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// writeTestFile writes a note under an existing vault root, creating parent
// dirs. Unlike newTestVault it reuses a caller-owned root so tests can rewrite a
// note and rebuild the queue against the same on-disk vault.
func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// openVault opens a vault at root without merging DefaultIgnore, matching
// newTestVault's construction.
func openVault(root string) (*vault.Vault, error) {
	return vault.Open(&config.Config{Vault: root})
}

func fixedStore(t *testing.T, root string, now time.Time) *StateStore {
	t.Helper()
	store := NewStateStore(root)
	store.now = func() time.Time { return now }
	return store
}

// writeJSONState marshals a state file and writes it to a vault-relative path,
// creating parent dirs. Used to seed cockpit.json / review.json fixtures.
func writeJSONState(t *testing.T, root, relPath string, state stateFile) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func TestStateStore_PersistDoneClearSnoozeAndCounts(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

	loc := time.FixedZone("cockpit-test", -4*60*60)
	now := time.Date(2026, 6, 8, 15, 30, 0, 0, loc)
	store := fixedStore(t, root, now)

	queue := buildQueue(t, root)
	task := findTask(t, queue, RuleBrokenTarget, "missing")

	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf(".bnotes before save: %v, want not exist", err)
	}

	if err := store.MarkDone(task); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(stateRelPath))); err != nil || info.IsDir() {
		t.Fatalf("state file after save: info=%#v err=%v, want a file", info, err)
	}

	doneQueue, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter done queue: %v", err)
	}
	if hasTaskID(doneQueue, task.ID) {
		t.Fatalf("done task remained active: %#v", taskDetails(doneQueue.Tasks))
	}
	if doneQueue.Counts.DoneHidden != 1 || doneQueue.Counts.SnoozedHidden != 0 {
		t.Fatalf("done counts = %#v, want one done-hidden", doneQueue.Counts)
	}
	if doneQueue.Counts.TotalGenerated != doneQueue.Counts.Active+1 {
		t.Fatalf("total generated = %#v, want active+1", doneQueue.Counts)
	}

	state, loaded, err := store.load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !loaded {
		t.Fatalf("state was not persisted")
	}
	if rec := state.Items[task.ID]; rec.State != stateDone || !rec.UpdatedAt.Equal(now) {
		t.Fatalf("done record = %#v, want done at %s", rec, now)
	}

	if err := store.Clear(task); err != nil {
		t.Fatalf("clear: %v", err)
	}
	clearedQueue, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter cleared queue: %v", err)
	}
	if !hasTaskID(clearedQueue, task.ID) {
		t.Fatalf("cleared task did not return to active queue")
	}
	if clearedQueue.Counts.DoneHidden != 0 || clearedQueue.Counts.SnoozedHidden != 0 {
		t.Fatalf("cleared counts = %#v, want no hidden", clearedQueue.Counts)
	}

	if err := store.Snooze(task, SnoozeNextWeek); err != nil {
		t.Fatalf("snooze week: %v", err)
	}
	state, _, err = store.load()
	if err != nil {
		t.Fatalf("load snoozed state: %v", err)
	}
	wantDeadline := time.Date(2026, 6, 15, 0, 0, 0, 0, loc)
	rec := state.Items[task.ID]
	if rec.State != stateSnoozed || rec.SnoozedUntil == nil || !rec.SnoozedUntil.Equal(wantDeadline) {
		t.Fatalf("snoozed record = %#v, want deadline %s", rec, wantDeadline)
	}
	snoozedQueue, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter snoozed queue: %v", err)
	}
	if hasTaskID(snoozedQueue, task.ID) || snoozedQueue.Counts.SnoozedHidden != 1 {
		t.Fatalf("snoozed queue = %#v, want task hidden as snoozed", snoozedQueue.Counts)
	}
}

func TestStateStore_SnoozeExpiresAndPrunesRecord(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

	loc := time.FixedZone("cockpit-test", -4*60*60)
	now := time.Date(2026, 6, 8, 15, 30, 0, 0, loc)
	store := fixedStore(t, root, now)

	task := findTask(t, buildQueue(t, root), RuleBrokenTarget, "missing")
	if err := store.Snooze(task, SnoozeTomorrow); err != nil {
		t.Fatalf("snooze tomorrow: %v", err)
	}

	hidden, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter hidden queue: %v", err)
	}
	if hasTaskID(hidden, task.ID) || hidden.Counts.SnoozedHidden != 1 {
		t.Fatalf("hidden queue = %#v, want snoozed task hidden", hidden.Counts)
	}

	store.now = func() time.Time { return time.Date(2026, 6, 9, 0, 0, 1, 0, loc) }
	expired, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter expired queue: %v", err)
	}
	if !hasTaskID(expired, task.ID) {
		t.Fatalf("expired snooze task did not return to active queue")
	}
	if expired.Counts.SnoozedHidden != 0 {
		t.Fatalf("expired counts = %#v, want no snoozed hidden", expired.Counts)
	}

	state, _, err := store.load()
	if err != nil {
		t.Fatalf("load pruned state: %v", err)
	}
	if len(state.Items) != 0 {
		t.Fatalf("expired snooze record was not pruned: %#v", state.Items)
	}
}

func TestStateStore_ContentChangeRotatesSignatureAndPrunesStale(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	store := NewStateStore(root)

	task := findTask(t, buildQueue(t, root), RuleBrokenTarget, "missing")
	if err := store.MarkDone(task); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\nChanged body, same issue: [[Missing]]\n")
	changedQueue := buildQueue(t, root)
	changedTask := findTask(t, changedQueue, RuleBrokenTarget, "missing")
	if changedTask.ID == task.ID || changedTask.Signature == task.Signature {
		t.Fatalf("content change did not rotate identity: before=%s after=%s", task.ID, changedTask.ID)
	}

	filtered, err := store.FilterQueue(changedQueue)
	if err != nil {
		t.Fatalf("filter changed queue: %v", err)
	}
	if !hasTaskID(filtered, changedTask.ID) {
		t.Fatalf("rotated task should be active, got hidden")
	}
	if filtered.Counts.DoneHidden != 0 {
		t.Fatalf("changed counts = %#v, want no done-hidden", filtered.Counts)
	}

	state, _, err := store.load()
	if err != nil {
		t.Fatalf("load pruned state: %v", err)
	}
	if len(state.Items) != 0 {
		t.Fatalf("stale done record was not pruned: %#v", state.Items)
	}
}

func TestStateStore_MigratesCompatibleReviewRecordsWithoutDeletingFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

	task := findTask(t, buildQueue(t, root), RuleBrokenTarget, "missing")

	// Seed a legacy review.json: one record that matches the generated task
	// (should migrate and hide it) and one incompatible record (empty
	// signature — must be dropped without error).
	writeJSONState(t, root, reviewStateRelPath, stateFile{
		Version: stateVersion,
		Items: map[string]stateRecord{
			task.ID: {
				ID:        task.ID,
				Signature: task.Signature,
				State:     stateDone,
				UpdatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			"task_bogus": {
				ID:        "task_bogus",
				Signature: "",
				State:     stateDone,
				UpdatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	})

	store := NewStateStore(root)
	migratedQueue, err := store.FilterQueue(buildQueue(t, root))
	if err != nil {
		t.Fatalf("filter migrated queue: %v", err)
	}
	if hasTaskID(migratedQueue, task.ID) {
		t.Fatalf("migrated done record did not hide task")
	}
	if migratedQueue.Counts.DoneHidden != 1 {
		t.Fatalf("migrated counts = %#v, want one done-hidden", migratedQueue.Counts)
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(reviewStateRelPath))); err != nil {
		t.Fatalf("legacy review.json should still exist after migration: %v", err)
	}

	state, loaded, err := store.load()
	if err != nil {
		t.Fatalf("load migrated state: %v", err)
	}
	if !loaded {
		t.Fatalf("migration produced no state")
	}
	if _, ok := state.Items["task_bogus"]; ok {
		t.Fatalf("incompatible review record was migrated: %#v", state.Items)
	}
}

func TestStateStore_CorruptCockpitStateErrorsAndCorruptReviewIgnored(t *testing.T) {
	t.Run("corrupt cockpit.json errors without overwriting", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

		statePath := filepath.Join(root, filepath.FromSlash(stateRelPath))
		if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
			t.Fatalf("mkdir state dir: %v", err)
		}
		corrupt := "{ not json"
		if err := os.WriteFile(statePath, []byte(corrupt), 0o644); err != nil {
			t.Fatalf("write corrupt state: %v", err)
		}

		store := NewStateStore(root)
		_, err := store.FilterQueue(buildQueue(t, root))
		if err == nil || !strings.Contains(err.Error(), "parse state") {
			t.Fatalf("filter err = %v, want parse state error", err)
		}
		got, err := os.ReadFile(statePath)
		if err != nil || string(got) != corrupt {
			t.Fatalf("corrupt state was overwritten: got %q err=%v", got, err)
		}
	})

	t.Run("corrupt review.json is ignored on migration", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

		reviewPath := filepath.Join(root, filepath.FromSlash(reviewStateRelPath))
		if err := os.MkdirAll(filepath.Dir(reviewPath), 0o755); err != nil {
			t.Fatalf("mkdir review dir: %v", err)
		}
		if err := os.WriteFile(reviewPath, []byte("{ not json"), 0o644); err != nil {
			t.Fatalf("write corrupt review: %v", err)
		}

		store := NewStateStore(root)
		queue := buildQueue(t, root)
		filtered, err := store.FilterQueue(queue)
		if err != nil {
			t.Fatalf("filter with corrupt review: %v", err)
		}
		if filtered.Counts.Active != len(queue.Tasks) || filtered.Counts.DoneHidden != 0 {
			t.Fatalf("corrupt review affected filtering: %#v", filtered.Counts)
		}
	})
}

func TestStateStore_FilterQueueReturnsFilteredResultWhenCleanupSaveFails(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")

	queue := buildQueue(t, root)
	task := findTask(t, queue, RuleBrokenTarget, "missing")
	targetRel := ".bnotes/cockpit-target.json"
	writeJSONState(t, root, targetRel, stateFile{
		Version: stateVersion,
		Items: map[string]stateRecord{
			task.ID: {
				ID:        task.ID,
				Signature: task.Signature,
				State:     stateDone,
				UpdatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			"task_stale": {
				ID:        "task_stale",
				Signature: "stale",
				State:     stateDone,
				UpdatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	})
	statePath := filepath.Join(root, filepath.FromSlash(stateRelPath))
	if err := os.Symlink(filepath.Base(targetRel), statePath); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}

	filtered, err := NewStateStore(root).FilterQueue(queue)
	if !IsStateCleanupError(err) {
		t.Fatalf("filter error = %v, want state cleanup error", err)
	}
	if hasTaskID(filtered, task.ID) {
		t.Fatalf("done task remained active after cleanup failure")
	}
	if filtered.Counts.DoneHidden != 1 || filtered.Counts.TotalGenerated != len(queue.Tasks) {
		t.Fatalf("filtered counts = %#v, want one done-hidden and %d total", filtered.Counts, len(queue.Tasks))
	}

	data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(targetRel)))
	if readErr != nil {
		t.Fatalf("read unchanged state target: %v", readErr)
	}
	var persisted stateFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode unchanged state target: %v", err)
	}
	if _, ok := persisted.Items["task_stale"]; !ok {
		t.Fatalf("failed cleanup unexpectedly changed persisted state: %#v", persisted.Items)
	}
}

func buildQueue(t *testing.T, root string) Queue {
	t.Helper()
	v, err := openVault(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	queue, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	return queue
}

func hasTaskID(queue Queue, id string) bool {
	for _, task := range queue.Tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
