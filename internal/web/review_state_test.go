package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewStateStore_PersistsDoneClearSnoozeAndCreatesBnotes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	loc := time.FixedZone("review-test", -4*60*60)
	now := time.Date(2026, 6, 8, 15, 30, 0, 0, loc)
	store := newReviewStateStore(root)
	store.now = func() time.Time { return now }

	queue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build queue: %v", err)
	}
	item := findReviewItem(t, queue, reviewKindBrokenTarget, "missing")

	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf(".bnotes before state save: %v, want not exist", err)
	}

	if err := store.markDone(item); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, ".bnotes")); err != nil || !info.IsDir() {
		t.Fatalf(".bnotes after state save: info=%#v err=%v, want directory", info, err)
	}

	doneQueue, err := buildReviewQueue(v, cfg)
	if err != nil {
		t.Fatalf("build done queue: %v", err)
	}
	if hasReviewItemID(doneQueue, item.ID) {
		t.Fatalf("done item remained active: %#v", doneQueue)
	}
	if doneQueue.Counts.DoneHidden != 1 || doneQueue.Counts.SnoozedHidden != 0 {
		t.Fatalf("done counts = %#v, want one done-hidden item", doneQueue.Counts)
	}

	state, loaded, err := store.load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !loaded {
		t.Fatalf("state was not persisted")
	}
	record := state.Items[item.ID]
	if record.State != reviewRecordStateDone || !record.UpdatedAt.Equal(now) {
		t.Fatalf("done record = %#v, want done at %s", record, now)
	}

	if err := store.clear(item); err != nil {
		t.Fatalf("clear: %v", err)
	}
	clearedQueue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build cleared queue: %v", err)
	}
	if !hasReviewItemID(clearedQueue, item.ID) {
		t.Fatalf("cleared item did not return to active queue: %#v", clearedQueue)
	}
	if clearedQueue.Counts.DoneHidden != 0 || clearedQueue.Counts.SnoozedHidden != 0 {
		t.Fatalf("cleared counts = %#v, want no hidden state", clearedQueue.Counts)
	}

	if err := store.snooze(item, reviewSnoozeNextWeek); err != nil {
		t.Fatalf("snooze week: %v", err)
	}
	state, _, err = store.load()
	if err != nil {
		t.Fatalf("load snoozed state: %v", err)
	}
	record = state.Items[item.ID]
	wantDeadline := time.Date(2026, 6, 15, 0, 0, 0, 0, loc)
	if record.State != reviewRecordStateSnoozed || record.SnoozedUntil == nil || !record.SnoozedUntil.Equal(wantDeadline) {
		t.Fatalf("snoozed record = %#v, want deadline %s", record, wantDeadline)
	}
	snoozedQueue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build snoozed queue: %v", err)
	}
	if hasReviewItemID(snoozedQueue, item.ID) || snoozedQueue.Counts.SnoozedHidden != 1 {
		t.Fatalf("snoozed queue = %#v, want item hidden as snoozed", snoozedQueue)
	}
}

func TestReviewStateStore_SnoozeExpiresAndPrunesRecord(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	loc := time.FixedZone("review-test", -4*60*60)
	now := time.Date(2026, 6, 8, 15, 30, 0, 0, loc)
	store := newReviewStateStore(root)
	store.now = func() time.Time { return now }

	queue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build queue: %v", err)
	}
	item := findReviewItem(t, queue, reviewKindBrokenTarget, "missing")
	if err := store.snooze(item, reviewSnoozeTomorrow); err != nil {
		t.Fatalf("snooze tomorrow: %v", err)
	}

	hiddenQueue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build hidden queue: %v", err)
	}
	if hasReviewItemID(hiddenQueue, item.ID) || hiddenQueue.Counts.SnoozedHidden != 1 {
		t.Fatalf("hidden queue = %#v, want snoozed item hidden", hiddenQueue)
	}

	now = time.Date(2026, 6, 9, 0, 0, 1, 0, loc)
	expiredQueue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build expired queue: %v", err)
	}
	if !hasReviewItemID(expiredQueue, item.ID) {
		t.Fatalf("expired snooze item did not return to active queue: %#v", expiredQueue)
	}
	if expiredQueue.Counts.SnoozedHidden != 0 {
		t.Fatalf("expired counts = %#v, want no snoozed hidden items", expiredQueue.Counts)
	}

	state, _, err := store.load()
	if err != nil {
		t.Fatalf("load pruned state: %v", err)
	}
	if len(state.Items) != 0 {
		t.Fatalf("expired snooze record was not pruned: %#v", state.Items)
	}
}

func TestReviewStateStore_ContentChangeReappearsAndPrunesStaleRecord(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	store := newReviewStateStore(root)

	queue, err := buildReviewQueueWithState(v, cfg, store)
	if err != nil {
		t.Fatalf("build queue: %v", err)
	}
	item := findReviewItem(t, queue, reviewKindBrokenTarget, "missing")
	if err := store.markDone(item); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\nChanged body, same issue: [[Missing]]\n")
	_, reopened := testVaultOpen(t, root, "127.0.0.1:0")
	changedQueue, err := buildReviewQueueWithState(reopened, cfg, store)
	if err != nil {
		t.Fatalf("build changed queue: %v", err)
	}
	changedItem := findReviewItem(t, changedQueue, reviewKindBrokenTarget, "missing")
	if changedItem.ID == item.ID || changedItem.Signature == item.Signature {
		t.Fatalf("content change did not rotate review identity: before=%#v after=%#v", item, changedItem)
	}
	if changedQueue.Counts.DoneHidden != 0 {
		t.Fatalf("changed counts = %#v, want no done-hidden items", changedQueue.Counts)
	}

	state, _, err := store.load()
	if err != nil {
		t.Fatalf("load pruned state: %v", err)
	}
	if len(state.Items) != 0 {
		t.Fatalf("stale done record was not pruned: %#v", state.Items)
	}
}

func TestReviewStateStore_CorruptJSONReturnsErrorWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	statePath := filepath.Join(root, filepath.FromSlash(reviewStateRelPath))
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	corrupt := "{ not json"
	if err := os.WriteFile(statePath, []byte(corrupt), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	store := newReviewStateStore(root)
	_, err := buildReviewQueueWithState(v, cfg, store)
	if err == nil || !strings.Contains(err.Error(), "parse review state") {
		t.Fatalf("build queue err = %v, want parse review state error", err)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read corrupt state: %v", err)
	}
	if string(got) != corrupt {
		t.Fatalf("corrupt state was overwritten: got %q want %q", got, corrupt)
	}

	rawQueue, err := buildReviewQueueWithState(v, cfg, nil)
	if err != nil {
		t.Fatalf("build raw queue: %v", err)
	}
	item := findReviewItem(t, rawQueue, reviewKindBrokenTarget, "missing")
	if err := store.markDone(item); err == nil || !strings.Contains(err.Error(), "parse review state") {
		t.Fatalf("mark done err = %v, want parse review state error", err)
	}
}

func hasReviewItemID(queue reviewQueue, id string) bool {
	for _, item := range queue.Items {
		if item.ID == id {
			return true
		}
	}
	return false
}
