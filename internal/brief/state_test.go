package brief

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeBriefNote(t *testing.T, root, relativePath string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir note parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("brief state fixture\n"), 0o644); err != nil {
		t.Fatalf("write note %s: %v", relativePath, err)
	}
}

func readBriefState(t *testing.T, root string) ([]byte, briefStateFile) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(briefStateRelPath)))
	if err != nil {
		t.Fatalf("read brief state: %v", err)
	}
	var state briefStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode brief state: %v\n%s", err, data)
	}
	return data, state
}

func localDateAt(ts time.Time) LocalDate {
	return LocalDate(ts.Format("2006-01-02"))
}

func mustCardID(t *testing.T, date LocalDate, slot SlotKind, source, candidate string) CardID {
	t.Helper()
	id, err := CardIDFor(date, slot, source, candidate)
	if err != nil {
		t.Fatalf("CardIDFor(%s, %s, %s, %s): %v", date, slot, source, candidate, err)
	}
	return id
}

func makeCard(t *testing.T, date LocalDate, slot SlotKind, source, candidate string) Card {
	t.Helper()
	card := Card{
		ID:   mustCardID(t, date, slot, source, candidate),
		Slot: slot,
		Source: NoteRef{
			RelPath: source,
			Title:   source,
		},
		Metadata: CandidateMetadata{
			Score:        1.0,
			Relationship: "related",
			Bridge:       "bridge",
			Reasons:      []string{"reason"},
		},
	}
	if slot == SlotConnect {
		card.Candidate = &NoteRef{
			RelPath: candidate,
			Title:   candidate,
		}
	}
	return card
}

func TestCardIDForDeterministicAndDirected(t *testing.T) {
	t.Parallel()
	date := LocalDate("2026-08-04")
	composed, err := CardIDFor(date, SlotContinue, "Café.md", "")
	if err != nil {
		t.Fatalf("CardIDFor composed: %v", err)
	}
	decomposed, err := CardIDFor(date, SlotContinue, "Cafe\u0301.md", "")
	if err != nil {
		t.Fatalf("CardIDFor decomposed: %v", err)
	}
	if composed != decomposed {
		t.Fatalf("NFC card IDs differ: %q != %q", composed, decomposed)
	}

	connectForward, err := CardIDFor(date, SlotConnect, "Source.md", "Candidate.md")
	if err != nil {
		t.Fatalf("CardIDFor connect forward: %v", err)
	}
	connectReverse, err := CardIDFor(date, SlotConnect, "Candidate.md", "Source.md")
	if err != nil {
		t.Fatalf("CardIDFor connect reverse: %v", err)
	}
	if connectForward == connectReverse {
		t.Fatalf("directed connect IDs unexpectedly match: %q", connectForward)
	}

	if _, err := CardIDFor(date, SlotContinue, "Source.md", "Candidate.md"); err == nil {
		t.Fatal("CardIDFor continue with candidate succeeded, want error")
	}
	if _, err := CardIDFor(date, SlotConnect, "Source.md", ""); err == nil {
		t.Fatal("CardIDFor connect without candidate succeeded, want error")
	}
}

func TestFileStateStoreReadIsLazyAndNonMutating(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBriefNote(t, root, "Source.md")
	store := NewFileStateStore(root)

	selection, ok, err := store.ReadDailySelection("2026-08-04")
	if err != nil {
		t.Fatalf("ReadDailySelection: %v", err)
	}
	if ok || selection.Date != "" || len(selection.Cards) != 0 {
		t.Fatalf("ReadDailySelection missing = (%#v, %v), want empty selection and false", selection, ok)
	}

	last, found, err := store.LastNoteExposure("Source.md")
	if err != nil {
		t.Fatalf("LastNoteExposure: %v", err)
	}
	if found || !last.IsZero() {
		t.Fatalf("LastNoteExposure missing = (%v, %v), want (zero, false)", last, found)
	}

	at := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	activity, err := store.RollingActivity(at)
	if err != nil {
		t.Fatalf("RollingActivity: %v", err)
	}
	if activity.Last7Days != (ActivitySummary{WindowDays: 7}) || activity.Last14Days != (ActivitySummary{WindowDays: 14}) {
		t.Fatalf("RollingActivity empty = %#v", activity)
	}

	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("read-only access created .bnotes: %v", err)
	}
}

func TestFileStateStoreSaveReplaceAndExposureQueries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{
		"Continue-A.md",
		"Continue-B.md",
		"Rediscover.md",
		"Connect-A.md",
		"Connect-B.md",
	} {
		writeBriefNote(t, root, relativePath)
	}
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 4, 15, 30, 0, 0, location)
	date := localDateAt(now)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))

	initial := DailySelection{
		Date: date,
		Cards: []Card{
			makeCard(t, date, SlotContinue, "Continue-A.md", ""),
			makeCard(t, date, SlotRediscover, "Rediscover.md", ""),
			makeCard(t, date, SlotConnect, "Continue-A.md", "Connect-A.md"),
		},
	}
	if err := store.SaveDailySelection(initial); err != nil {
		t.Fatalf("SaveDailySelection: %v", err)
	}

	reloaded := NewFileStateStore(root, WithClock(func() time.Time { return now }))
	saved, ok, err := reloaded.ReadDailySelection(date)
	if err != nil {
		t.Fatalf("ReadDailySelection after save: %v", err)
	}
	if !ok {
		t.Fatal("ReadDailySelection after save reported missing selection")
	}
	if got, want := len(saved.Cards), 3; got != want {
		t.Fatalf("saved selection card count = %d, want %d", got, want)
	}
	if got, want := []SlotKind{saved.Cards[0].Slot, saved.Cards[1].Slot, saved.Cards[2].Slot}, []SlotKind{
		SlotContinue,
		SlotRediscover,
		SlotConnect,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved slot order = %v, want %v", got, want)
	}

	lastNote, ok, err := reloaded.LastNoteExposure("Rediscover.md")
	if err != nil || !ok || !lastNote.Equal(now) {
		t.Fatalf("LastNoteExposure = (%v, %v, %v), want (%v, true, nil)", lastNote, ok, err, now)
	}
	lastPair, ok, err := reloaded.LastPairExposure("Continue-A.md", "Connect-A.md")
	if err != nil || !ok || !lastPair.Equal(now) {
		t.Fatalf("LastPairExposure initial = (%v, %v, %v), want (%v, true, nil)", lastPair, ok, err, now)
	}

	replaced, err := reloaded.ReplaceDailySelection(date, []Card{
		makeCard(t, date, SlotContinue, "Continue-B.md", ""),
		makeCard(t, date, SlotConnect, "Continue-B.md", "Connect-B.md"),
	})
	if err != nil {
		t.Fatalf("ReplaceDailySelection: %v", err)
	}
	if got, want := len(replaced.Cards), 3; got != want {
		t.Fatalf("replaced card count = %d, want %d", got, want)
	}
	if replaced.Cards[0].Source.RelPath != "Continue-B.md" {
		t.Fatalf("continue replacement source = %q, want Continue-B.md", replaced.Cards[0].Source.RelPath)
	}
	if replaced.Cards[1].Source.RelPath != "Rediscover.md" {
		t.Fatalf("rediscover source after replacement = %q, want Rediscover.md", replaced.Cards[1].Source.RelPath)
	}
	if replaced.Cards[2].Candidate == nil || replaced.Cards[2].Candidate.RelPath != "Connect-B.md" {
		t.Fatalf("connect replacement candidate = %#v, want Connect-B.md", replaced.Cards[2].Candidate)
	}

	lastPair, ok, err = reloaded.LastPairExposure("Continue-B.md", "Connect-B.md")
	if err != nil || !ok || !lastPair.Equal(now) {
		t.Fatalf("LastPairExposure replacement = (%v, %v, %v), want (%v, true, nil)", lastPair, ok, err, now)
	}

	data, state := readBriefState(t, root)
	if state.Version != briefStateVersion {
		t.Fatalf("state version = %d, want %d", state.Version, briefStateVersion)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("state file does not end with newline: %q", data)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".bnotes"))
	if err != nil {
		t.Fatalf("read .bnotes: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "brief.json" {
		t.Fatalf(".bnotes contains leftover atomic-write files: %v", entries)
	}
}

func TestFileStateStoreLastNoteOpenTracksCardTargets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{
		"Continue.md",
		"Rediscover.md",
		"Connect.md",
	} {
		writeBriefNote(t, root, relativePath)
	}
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, location)
	date := localDateAt(now)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))

	selection := DailySelection{
		Date: date,
		Cards: []Card{
			makeCard(t, date, SlotContinue, "Continue.md", ""),
			makeCard(t, date, SlotRediscover, "Rediscover.md", ""),
			makeCard(t, date, SlotConnect, "Continue.md", "Connect.md"),
		},
	}
	if err := store.SaveDailySelection(selection); err != nil {
		t.Fatalf("SaveDailySelection: %v", err)
	}

	continueOpenAt := now.Add(30 * time.Minute)
	connectOpenAt := now.Add(90 * time.Minute)
	if _, err := store.RecordOpen(selection.Cards[0].ID, continueOpenAt); err != nil {
		t.Fatalf("RecordOpen continue: %v", err)
	}
	if _, err := store.RecordOpen(selection.Cards[2].ID, connectOpenAt); err != nil {
		t.Fatalf("RecordOpen connect: %v", err)
	}

	last, ok, err := store.LastNoteOpen("Continue.md")
	if err != nil || !ok || !last.Equal(continueOpenAt) {
		t.Fatalf("LastNoteOpen continue = (%v, %v, %v), want (%v, true, nil)", last, ok, err, continueOpenAt)
	}
	last, ok, err = store.LastNoteOpen("Connect.md")
	if err != nil || !ok || !last.Equal(connectOpenAt) {
		t.Fatalf("LastNoteOpen connect = (%v, %v, %v), want (%v, true, nil)", last, ok, err, connectOpenAt)
	}
	last, ok, err = store.LastNoteOpen("Rediscover.md")
	if err != nil || ok || !last.IsZero() {
		t.Fatalf("LastNoteOpen rediscover without open = (%v, %v, %v), want (zero, false, nil)", last, ok, err)
	}
}

func TestFileStateStoreVisitAndOpenActivityWindows(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBriefNote(t, root, "Source.md")
	location := time.FixedZone("brief-test", -5*60*60)
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, location)
	store := NewFileStateStore(root)

	dateToday := localDateAt(now)
	activity, err := store.RecordVisit(dateToday, now)
	if err != nil {
		t.Fatalf("RecordVisit first: %v", err)
	}
	if activity.Last7Days.VisitDays != 1 || activity.Last14Days.VisitDays != 1 {
		t.Fatalf("visit activity after first visit = %#v", activity)
	}
	activity, err = store.RecordVisit(dateToday, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RecordVisit idempotent: %v", err)
	}
	if activity.Last7Days.VisitDays != 1 || activity.Last14Days.VisitDays != 1 {
		t.Fatalf("visit activity after duplicate visit = %#v", activity)
	}

	dateOld := localDateAt(now.AddDate(0, 0, -10))
	if _, err := store.RecordVisit(dateOld, now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("RecordVisit old date: %v", err)
	}

	cardID := mustCardID(t, dateToday, SlotContinue, "Source.md", "")
	if _, err := store.RecordOpen(cardID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("RecordOpen recent: %v", err)
	}
	if _, err := store.RecordOpen(cardID, now.AddDate(0, 0, -8)); err != nil {
		t.Fatalf("RecordOpen 8-days-ago: %v", err)
	}
	activity, err = store.RollingActivity(now)
	if err != nil {
		t.Fatalf("RollingActivity: %v", err)
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

func TestFileStateStorePrunesOnlyOnExplicitWrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{"Keep.md", "Source.md", "Missing.md"} {
		writeBriefNote(t, root, relativePath)
	}
	location := time.FixedZone("brief-test", -6*60*60)
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, location)
	dateNow := localDateAt(now)
	oldDate := localDateAt(now.AddDate(0, 0, -100))
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))

	oldSelection := DailySelection{
		Date: oldDate,
		Cards: []Card{
			makeCard(t, oldDate, SlotRediscover, "Keep.md", ""),
		},
	}
	if err := store.saveDailySelectionAt(oldSelection, now.AddDate(0, 0, -100)); err != nil {
		t.Fatalf("save old selection: %v", err)
	}
	currentSelection := DailySelection{
		Date: dateNow,
		Cards: []Card{
			makeCard(t, dateNow, SlotRediscover, "Keep.md", ""),
			makeCard(t, dateNow, SlotConnect, "Source.md", "Missing.md"),
		},
	}
	if err := store.SaveDailySelection(currentSelection); err != nil {
		t.Fatalf("save current selection: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "Missing.md")); err != nil {
		t.Fatalf("remove missing note fixture: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(briefStateRelPath)))
	if err != nil {
		t.Fatalf("read state before query: %v", err)
	}
	if _, _, err := store.LastPairExposure("Source.md", "Missing.md"); err != nil {
		t.Fatalf("LastPairExposure before prune: %v", err)
	}
	afterRead, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(briefStateRelPath)))
	if err != nil {
		t.Fatalf("read state after query: %v", err)
	}
	if !bytes.Equal(before, afterRead) {
		t.Fatalf("read-only query rewrote state\nbefore:\n%s\nafter:\n%s", before, afterRead)
	}

	if _, err := store.RecordVisit(dateNow, now); err != nil {
		t.Fatalf("RecordVisit prune write: %v", err)
	}
	_, state := readBriefState(t, root)
	if len(state.DailySelections) != 1 {
		t.Fatalf("daily selections after prune = %d, want 1", len(state.DailySelections))
	}
	selection := state.DailySelections[string(dateNow)]
	if got, want := len(selection.Cards), 1; got != want {
		t.Fatalf("selection cards after missing-note prune = %d, want %d", got, want)
	}
	if _, ok := state.PairExposures[func() string {
		_, _, id, _ := normalizeBriefPair("Source.md", "Missing.md")
		return id
	}()]; ok {
		t.Fatal("pair exposure for removed note still present after explicit write")
	}
}
