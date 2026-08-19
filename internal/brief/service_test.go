package brief

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestServiceDailyStableAcrossCallsAndRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 22, 9, 30, 0, 0, location)

	writeSelectionNote(t, root, "Continue.md", "# Continue\nalpha beta [[Bridge]]\n", now.Add(-30*time.Minute))
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[ConnectOne]] [[ConnectTwo]]\n", now.AddDate(0, 0, -2))
	writeSelectionNote(t, root, "ConnectOne.md", "# Connect One\nalpha beta gamma delta\n", now.AddDate(0, 0, -20))
	writeSelectionNote(t, root, "ConnectTwo.md", "# Connect Two\nalpha beta gamma\n", now.AddDate(0, 0, -18))
	writeSelectionNote(t, root, "Rediscover.md", "# Rediscover\nolder note content.\n", now.AddDate(0, 0, -40))

	clock := now
	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return clock }))
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily first: %v", err)
	}
	second, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily second: %v", err)
	}
	if !reflect.DeepEqual(slotCardIDs(first.Brief), slotCardIDs(second.Brief)) {
		t.Fatalf("daily card IDs changed across repeated calls: first=%v second=%v", slotCardIDs(first.Brief), slotCardIDs(second.Brief))
	}
	if len(first.Warnings) != 0 || len(second.Warnings) != 0 {
		t.Fatalf("unexpected warnings: first=%v second=%v", first.Warnings, second.Warnings)
	}

	reloadedStore := NewFileStateStore(root, WithClock(func() time.Time { return clock }))
	reloadedSelector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: reloadedStore,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector (reloaded): %v", err)
	}
	reloadedService, err := NewService(reloadedStore, reloadedSelector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService (reloaded): %v", err)
	}
	third, err := reloadedService.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily reloaded: %v", err)
	}
	if !reflect.DeepEqual(slotCardIDs(first.Brief), slotCardIDs(third.Brief)) {
		t.Fatalf("daily card IDs changed across restart: first=%v third=%v", slotCardIDs(first.Brief), slotCardIDs(third.Brief))
	}
}

func TestServiceAnotherContinueRegeneratesConnectAtomically(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 22, 11, 0, 0, 0, location)

	writeSelectionNote(t, root, "ContinueA.md", "# Continue A\nalpha beta [[Bridge]]\n", now.Add(-10*time.Minute))
	writeSelectionNote(t, root, "ContinueB.md", "# Continue B\nalpha beta [[Bridge]]\n", now.Add(-20*time.Minute))
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[ConnectOne]] [[ConnectTwo]]\n", now.AddDate(0, 0, -1))
	writeSelectionNote(t, root, "ConnectOne.md", "# Connect One\nalpha beta gamma delta\n", now.AddDate(0, 0, -8))
	writeSelectionNote(t, root, "ConnectTwo.md", "# Connect Two\nalpha beta gamma\n", now.AddDate(0, 0, -7))
	writeSelectionNote(t, root, "Rediscover.md", "# Rediscover\nolder note content.\n", now.AddDate(0, 0, -30))

	clock := now
	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return clock }))
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	initial, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily initial: %v", err)
	}
	beforeContinue := cardForSlot(t, initial.Brief, SlotContinue)
	beforeConnect := cardForSlot(t, initial.Brief, SlotConnect)
	if beforeConnect.Candidate == nil {
		t.Fatalf("initial connect card missing candidate: %#v", beforeConnect)
	}

	replaced, err := service.Another(corpus.Snapshot, SlotContinue)
	if err != nil {
		t.Fatalf("Another continue: %v", err)
	}
	if got, want := len(replaced.Affected), 2; got != want {
		t.Fatalf("affected slot count = %d, want %d", got, want)
	}
	assertAffectedCardsMatchDaily(t, replaced)
	afterContinue := cardForSlot(t, replaced.Daily.Brief, SlotContinue)
	afterConnect := cardForSlot(t, replaced.Daily.Brief, SlotConnect)
	if afterContinue.Source.RelPath == beforeContinue.Source.RelPath {
		t.Fatalf("continue source did not advance: still %q", afterContinue.Source.RelPath)
	}
	if afterConnect.Source.RelPath != afterContinue.Source.RelPath {
		t.Fatalf("connect source = %q, want %q", afterConnect.Source.RelPath, afterContinue.Source.RelPath)
	}
	if beforeConnect.ID == afterConnect.ID {
		t.Fatalf("connect card ID did not change after continue replacement: %q", afterConnect.ID)
	}

	saved, ok, err := store.ReadDailySelection(LocalDate(clock.Format("2006-01-02")))
	if err != nil {
		t.Fatalf("ReadDailySelection: %v", err)
	}
	if !ok {
		t.Fatal("saved selection missing after continue replacement")
	}
	savedBySlot := cardBySlot(saved.Cards)
	if savedBySlot[SlotContinue].Source.RelPath != afterContinue.Source.RelPath {
		t.Fatalf("saved continue source = %q, want %q", savedBySlot[SlotContinue].Source.RelPath, afterContinue.Source.RelPath)
	}
	if savedBySlot[SlotConnect].Source.RelPath != afterContinue.Source.RelPath {
		t.Fatalf("saved connect source = %q, want %q", savedBySlot[SlotConnect].Source.RelPath, afterContinue.Source.RelPath)
	}
}

func TestServiceAnotherSecondarySlotsReturnMaterializedAffectedCards(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, location)

	writeSelectionNote(t, root, "Continue.md", "# Continue\nalpha beta [[Bridge]]\n", now.Add(-10*time.Minute))
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[ConnectOne]] [[ConnectTwo]]\n", now.AddDate(0, 0, -1))
	writeSelectionNote(t, root, "ConnectOne.md", "# Connect One\nalpha beta gamma delta\n", now.AddDate(0, 0, -8))
	writeSelectionNote(t, root, "ConnectTwo.md", "# Connect Two\nalpha beta gamma\n", now.AddDate(0, 0, -7))
	writeSelectionNote(t, root, "Rediscover.md", "# Rediscover\nolder note content.\n", now.AddDate(0, 0, -30))

	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	initial, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily initial: %v", err)
	}
	initialRediscover := cardForSlot(t, initial.Brief, SlotRediscover)
	initialConnect := cardForSlot(t, initial.Brief, SlotConnect)

	rediscover, err := service.Another(corpus.Snapshot, SlotRediscover)
	if err != nil {
		t.Fatalf("Another rediscover: %v", err)
	}
	assertAffectedCardsMatchDaily(t, rediscover)
	if got := cardForSlot(t, rediscover.Daily.Brief, SlotRediscover); got.Source.RelPath == initialRediscover.Source.RelPath {
		t.Fatalf("rediscover source did not advance: still %q", got.Source.RelPath)
	}

	connect, err := service.Another(corpus.Snapshot, SlotConnect)
	if err != nil {
		t.Fatalf("Another connect: %v", err)
	}
	assertAffectedCardsMatchDaily(t, connect)
	nextConnect := cardForSlot(t, connect.Daily.Brief, SlotConnect)
	if nextConnect.Candidate == nil {
		t.Fatalf("replacement connect card missing candidate: %#v", nextConnect)
	}
	if initialConnect.Candidate == nil {
		t.Fatalf("initial connect card missing candidate: %#v", initialConnect)
	}
	if nextConnect.Candidate.RelPath == initialConnect.Candidate.RelPath {
		t.Fatalf("connect candidate did not advance: still %q", nextConnect.Candidate.RelPath)
	}
}

func TestServiceAnotherPreservesUnavailableAffectedResult(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, time.August, 22, 13, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "Continue.md", "# Continue\nUseful text.\n", now)

	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))
	service, err := NewService(store, staticSelector{}, WithServiceClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.Daily(corpus.Snapshot); err != nil {
		t.Fatalf("Daily initial: %v", err)
	}

	result, err := service.Another(corpus.Snapshot, SlotRediscover)
	if err != nil {
		t.Fatalf("Another rediscover: %v", err)
	}
	if got, want := len(result.Affected), 1; got != want {
		t.Fatalf("affected slot count = %d, want %d", got, want)
	}
	affected := result.Affected[0]
	if affected.Card != nil || affected.Unavailable == nil {
		t.Fatalf("affected unavailable result = %#v", affected)
	}
	if got, want := affected.Unavailable.Message, "No rediscover candidate is available."; got != want {
		t.Fatalf("unavailable message = %q, want %q", got, want)
	}
}

func TestServiceDailySelectiveRepairForRemovedNotes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 22, 14, 0, 0, 0, location)

	writeSelectionNote(t, root, "Continue.md", "# Continue\nalpha beta [[Bridge]]\n", now.Add(-5*time.Minute))
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[ConnectOne]] [[ConnectTwo]]\n", now.AddDate(0, 0, -1))
	writeSelectionNote(t, root, "ConnectOne.md", "# Connect One\nalpha beta gamma delta\n", now.AddDate(0, 0, -9))
	writeSelectionNote(t, root, "ConnectTwo.md", "# Connect Two\nalpha beta gamma\n", now.AddDate(0, 0, -8))
	writeSelectionNote(t, root, "Rediscover.md", "# Rediscover\nolder note content.\n", now.AddDate(0, 0, -25))

	clock := now
	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return clock }))
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	initial, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily initial: %v", err)
	}
	initialContinue := cardForSlot(t, initial.Brief, SlotContinue)
	initialRediscover := cardForSlot(t, initial.Brief, SlotRediscover)
	initialConnect := cardForSlot(t, initial.Brief, SlotConnect)
	if initialConnect.Candidate == nil {
		t.Fatalf("initial connect card missing candidate: %#v", initialConnect)
	}

	removedPath := filepath.Join(root, filepath.FromSlash(initialConnect.Candidate.RelPath))
	if err := os.Remove(removedPath); err != nil {
		t.Fatalf("remove selected connect candidate: %v", err)
	}

	updatedCorpus := loadBriefCorpus(t, root)
	updatedSelector, err := NewDeterministicSelector(updatedCorpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector after removal: %v", err)
	}
	service, err = NewService(store, updatedSelector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService after removal: %v", err)
	}

	updated, err := service.Daily(updatedCorpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily after removal: %v", err)
	}
	updatedContinue := cardForSlot(t, updated.Brief, SlotContinue)
	updatedRediscover := cardForSlot(t, updated.Brief, SlotRediscover)
	updatedConnect := cardForSlot(t, updated.Brief, SlotConnect)

	if updatedContinue.ID != initialContinue.ID {
		t.Fatalf("continue card changed during selective repair: %q -> %q", initialContinue.ID, updatedContinue.ID)
	}
	if updatedRediscover.ID != initialRediscover.ID {
		t.Fatalf("rediscover card changed during selective repair: %q -> %q", initialRediscover.ID, updatedRediscover.ID)
	}
	if updatedConnect.ID == initialConnect.ID {
		t.Fatalf("connect card did not change after selected candidate was removed: %q", updatedConnect.ID)
	}
	if updatedConnect.Candidate == nil {
		t.Fatalf("updated connect candidate missing: %#v", updatedConnect)
	}
	if updatedConnect.Candidate.RelPath == initialConnect.Candidate.RelPath {
		t.Fatalf("updated connect candidate still points to removed note %q", updatedConnect.Candidate.RelPath)
	}
}

func TestServiceActivitySummariesUseLocalDateBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -5*60*60)
	now := time.Date(2026, time.August, 2, 9, 0, 0, 0, location)

	writeSelectionNote(t, root, "Continue.md", "# Continue\nalpha beta [[Bridge]]\n", now.Add(-10*time.Minute))
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[Connect.md]]\n", now.AddDate(0, 0, -1))
	writeSelectionNote(t, root, "Connect.md", "# Connect\nalpha beta gamma\n", now.AddDate(0, 0, -9))
	writeSelectionNote(t, root, "Rediscover.md", "# Rediscover\nolder note content.\n", now.AddDate(0, 0, -20))

	clock := now
	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return clock }))
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	oldDay, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily old day: %v", err)
	}
	if _, err := service.RecordView(); err != nil {
		t.Fatalf("RecordView old day: %v", err)
	}
	if _, err := service.RecordOpen(cardForSlot(t, oldDay.Brief, SlotContinue).ID); err != nil {
		t.Fatalf("RecordOpen old day: %v", err)
	}

	clock = now.AddDate(0, 0, 8)
	currentDay, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily current day: %v", err)
	}
	if _, err := service.RecordView(); err != nil {
		t.Fatalf("RecordView current day: %v", err)
	}
	activity, err := service.RecordOpen(cardForSlot(t, currentDay.Brief, SlotContinue).ID)
	if err != nil {
		t.Fatalf("RecordOpen current day: %v", err)
	}

	if got, want := activity.Last7Days.OpenCount, 1; got != want {
		t.Fatalf("Last7Days.OpenCount = %d, want %d", got, want)
	}
	if got, want := activity.Last14Days.OpenCount, 2; got != want {
		t.Fatalf("Last14Days.OpenCount = %d, want %d", got, want)
	}
	if got, want := activity.Last7Days.VisitDays, 1; got != want {
		t.Fatalf("Last7Days.VisitDays = %d, want %d", got, want)
	}
	if got, want := activity.Last14Days.VisitDays, 2; got != want {
		t.Fatalf("Last14Days.VisitDays = %d, want %d", got, want)
	}
}

func TestServiceDailyReturnsStructuredWarningForUnreadableState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, location)

	writeSelectionNote(t, root, "Source.md", "# Source\nUseful text.\n", now)
	statePath := filepath.Join(root, filepath.FromSlash(briefStateRelPath))
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	seed := []byte("{not json\n")
	if err := os.WriteFile(statePath, seed, 0o644); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}

	corpus := loadBriefCorpus(t, root)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))
	selector := staticSelector{}
	service, err := NewService(store, selector, WithServiceClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := service.Daily(corpus.Snapshot)
	if err != nil {
		t.Fatalf("Daily with unreadable state: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("Daily returned no warning for unreadable state")
	}
	if result.Warnings[0].Code != WarningStateUnreadable {
		t.Fatalf("warning code = %q, want %q", result.Warnings[0].Code, WarningStateUnreadable)
	}
	if cardForSlot(t, result.Brief, SlotContinue).Source.RelPath == "" {
		t.Fatal("Daily with unreadable state should still compose an ephemeral continue card")
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after Daily: %v", err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatalf("unreadable state was overwritten\nbefore:\n%s\nafter:\n%s", seed, got)
	}
}
