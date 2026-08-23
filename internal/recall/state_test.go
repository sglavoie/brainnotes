package recall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeStateRecallNote(t *testing.T, root, relativePath string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir note parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("recall state fixture\n"), 0o644); err != nil {
		t.Fatalf("write note %s: %v", relativePath, err)
	}
}

func readRecallState(t *testing.T, root string) ([]byte, recallStateFile) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(recallStateRelPath)))
	if err != nil {
		t.Fatalf("read recall state: %v", err)
	}
	var state recallStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode recall state: %v\n%s", err, data)
	}
	return data, state
}

func TestStateStoreReadIsLazyAndNonMutating(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Candidate.md")
	store := NewStateStore(root)

	if err := store.Validate(); err != nil {
		t.Fatalf("Validate missing state: %v", err)
	}
	signal, err := store.Signal("Source.md", "Candidate.md")
	if err != nil {
		t.Fatalf("Signal missing state: %v", err)
	}
	if signal != (RecallSignal{}) {
		t.Fatalf("missing signal = %#v, want zero", signal)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("read-only state access created .bnotes: %v", err)
	}
}

func TestStateStoreWithNotePathsUsesSnapshotForReadsAndFilesystemForWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Candidate.md")
	writeStateRecallNote(t, root, "OutsideSource.md")
	writeStateRecallNote(t, root, "OutsideCandidate.md")
	if err := NewStateStore(root).SetFeedback("OutsideSource.md", "OutsideCandidate.md", FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("seed outside-snapshot feedback: %v", err)
	}

	store := NewStateStore(root, WithNotePaths([]string{"Candidate.md", "Source.md"}))

	if err := store.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	signal, err := store.Signal("OutsideSource.md", "OutsideCandidate.md")
	if err != nil || signal != (RecallSignal{}) {
		t.Fatalf("outside-snapshot read signal = %#v, err=%v; want zero", signal, err)
	}
	if err := store.SetFeedback("Source.md", "Candidate.md", FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("SetFeedback through provided paths: %v", err)
	}
	signal, err = store.Signal("OutsideSource.md", "OutsideCandidate.md")
	if err != nil || signal != (RecallSignal{}) {
		t.Fatalf("outside-snapshot read signal after write = %#v, err=%v; want zero", signal, err)
	}

	signal, err = NewStateStore(root).Signal("OutsideSource.md", "OutsideCandidate.md")
	if err != nil || !signal.Useful {
		t.Fatalf("outside-snapshot signal after write = %#v, err=%v; want preserved useful feedback", signal, err)
	}
}

func TestStateStoreWithNotePathsRejectsInvalidSnapshot(t *testing.T) {
	t.Parallel()
	store := NewStateStore(t.TempDir(), WithNotePaths([]string{"../escape.md"}))
	if err := store.Validate(); err == nil || !strings.Contains(err.Error(), "note path snapshot") {
		t.Fatalf("Validate error = %v, want invalid note-path snapshot", err)
	}
}

func TestStateStorePersistsFeedbackAndOpenedSignals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Folder/Candidate.md")
	location := time.FixedZone("recall-test", -6*60*60)
	now := time.Date(2026, time.July, 13, 15, 30, 0, 0, location)
	store := NewStateStore(root, WithClock(func() time.Time { return now }))

	if err := store.SetFeedback("Source.md", "Folder/Candidate.md", FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("SetFeedback useful: %v", err)
	}
	signal, err := store.Signal("Source.md", "Folder/Candidate.md")
	if err != nil || !signal.Useful || signal.Dismissed {
		t.Fatalf("useful Signal = %#v, err=%v", signal, err)
	}

	reloaded := NewStateStore(root, WithClock(func() time.Time { return now }))
	signal, err = reloaded.Signal("Source.md", "Folder/Candidate.md")
	if err != nil || !signal.Useful {
		t.Fatalf("persisted useful Signal = %#v, err=%v", signal, err)
	}
	if err := reloaded.SetFeedback("Source.md", "Folder/Candidate.md", FeedbackDismissed, time.Time{}); err != nil {
		t.Fatalf("SetFeedback dismissed: %v", err)
	}
	signal, err = reloaded.Signal("Source.md", "Folder/Candidate.md")
	if err != nil || signal.Useful || !signal.Dismissed {
		t.Fatalf("dismissed Signal = %#v, err=%v", signal, err)
	}

	deadline := time.Date(2026, time.July, 20, 0, 0, 0, 0, location)
	if err := reloaded.SetFeedback("Source.md", "Folder/Candidate.md", FeedbackLater, deadline); err != nil {
		t.Fatalf("SetFeedback later: %v", err)
	}
	if err := reloaded.RecordOpened("Source.md", "Folder/Candidate.md"); err != nil {
		t.Fatalf("RecordOpened: %v", err)
	}
	signal, err = reloaded.Signal("Source.md", "Folder/Candidate.md")
	if err != nil || !signal.SnoozedUntil.Equal(deadline) || !signal.LastOpenedAt.Equal(now) {
		t.Fatalf("later/open Signal = %#v, err=%v", signal, err)
	}

	if err := reloaded.SetFeedback("Source.md", "Folder/Candidate.md", FeedbackClear, time.Time{}); err != nil {
		t.Fatalf("SetFeedback clear: %v", err)
	}
	signal, err = reloaded.Signal("Source.md", "Folder/Candidate.md")
	if err != nil {
		t.Fatalf("Signal after clear: %v", err)
	}
	if signal.Useful || signal.Dismissed || !signal.SnoozedUntil.IsZero() || !signal.LastOpenedAt.Equal(now) {
		t.Fatalf("clear Signal = %#v, want only open history", signal)
	}

	data, state := readRecallState(t, root)
	if state.Version != recallStateVersion || len(state.Records) != 1 {
		t.Fatalf("persisted state = %#v", state)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("state does not end in a newline: %q", data)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".bnotes"))
	if err != nil {
		t.Fatalf("read .bnotes: %v", err)
	}
	if len(entries) != 2 || entries[0].Name() != "locks" || !entries[0].IsDir() || entries[1].Name() != "recall.json" {
		t.Fatalf("state directory contains leftover atomic-write files: %v", entries)
	}
}

func TestStateStoreReadIgnoresStaleRecordsAndWritePrunesThem(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{"Source.md", "Expired.md", "Missing.md", "Other.md"} {
		writeStateRecallNote(t, root, relativePath)
	}
	location := time.FixedZone("recall-test", -6*60*60)
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, location)
	clock := now
	store := NewStateStore(root, WithClock(func() time.Time { return clock }))
	deadline := time.Date(2026, time.July, 14, 0, 0, 0, 0, location)
	for _, candidate := range []string{"Expired.md", "Missing.md"} {
		if err := store.SetFeedback("Source.md", candidate, FeedbackLater, deadline); err != nil {
			t.Fatalf("snooze %s: %v", candidate, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "Missing.md")); err != nil {
		t.Fatalf("remove stale candidate: %v", err)
	}
	before, _ := readRecallState(t, root)
	clock = time.Date(2026, time.July, 15, 9, 0, 0, 0, location)

	readStore := NewStateStore(root, WithClock(func() time.Time { return clock }))
	expired, err := readStore.Signal("Source.md", "Expired.md")
	if err != nil {
		t.Fatalf("expired Signal: %v", err)
	}
	missing, err := readStore.Signal("Source.md", "Missing.md")
	if err != nil {
		t.Fatalf("missing-note Signal: %v", err)
	}
	if expired != (RecallSignal{}) || missing != (RecallSignal{}) {
		t.Fatalf("stale signals: expired=%#v missing=%#v", expired, missing)
	}
	afterRead, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(recallStateRelPath)))
	if err != nil {
		t.Fatalf("read state after signals: %v", err)
	}
	if !bytes.Equal(before, afterRead) {
		t.Fatalf("read-only stale handling rewrote state\nbefore:\n%s\nafter:\n%s", before, afterRead)
	}

	if err := readStore.SetFeedback("Source.md", "Other.md", FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("explicit write: %v", err)
	}
	_, state := readRecallState(t, root)
	if len(state.Records) != 1 {
		t.Fatalf("records after pruning = %#v, want only Other.md", state.Records)
	}
	otherID, err := PairID("Source.md", "Other.md")
	if err != nil {
		t.Fatalf("PairID Other.md: %v", err)
	}
	if state.Records[otherID].Rating != string(FeedbackUseful) {
		t.Fatalf("remaining record = %#v, want useful Other.md", state.Records)
	}
}

func TestStateStoreSignalsDriveRanking(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{
		"Source.md",
		"Useful.md",
		"Dismissed.md",
		"Snoozed.md",
		"Recent.md",
	} {
		writeStateRecallNote(t, root, relativePath)
	}
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	store := NewStateStore(root, WithClock(func() time.Time { return now }))
	if err := store.SetFeedback("Source.md", "Useful.md", FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("useful feedback: %v", err)
	}
	if err := store.SetFeedback("Source.md", "Dismissed.md", FeedbackDismissed, time.Time{}); err != nil {
		t.Fatalf("dismissed feedback: %v", err)
	}
	if err := store.SetFeedback("Source.md", "Snoozed.md", FeedbackLater, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("later feedback: %v", err)
	}
	if err := store.RecordOpened("Source.md", "Recent.md"); err != nil {
		t.Fatalf("record recent open: %v", err)
	}

	documents := []Document{
		rankDocumentFixture("Source.md", "Source", "shared retrieval signal"),
		rankDocumentFixture("Useful.md", "Useful", "shared retrieval signal"),
		rankDocumentFixture("Dismissed.md", "Dismissed", "shared retrieval signal"),
		rankDocumentFixture("Snoozed.md", "Snoozed", "shared retrieval signal"),
		rankDocumentFixture("Recent.md", "Recent", "shared retrieval signal"),
	}
	documents[4].CreatedAt = now.Add(-FullNoveltyAge)
	baseline, err := BuildRanker(documents, nil, RankOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("BuildRanker baseline: %v", err)
	}
	baselineResults, err := baseline.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank baseline: %v", err)
	}
	if got := resultsByPath(baselineResults)["Recent.md"].Signals.Recall; got != 1 {
		t.Fatalf("baseline recent recall = %v, want full age novelty", got)
	}

	ranker, err := BuildRanker(documents, nil, RankOptions{
		Now:    func() time.Time { return now },
		Recall: store,
	})
	if err != nil {
		t.Fatalf("BuildRanker with state: %v", err)
	}
	results, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank with state: %v", err)
	}
	byPath := resultsByPath(results)
	if _, ok := byPath["Dismissed.md"]; ok {
		t.Fatal("dismissed candidate was ranked")
	}
	if _, ok := byPath["Snoozed.md"]; ok {
		t.Fatal("snoozed candidate was ranked")
	}
	if got := byPath["Useful.md"].Signals.Recall; got != UsefulRecallBoost {
		t.Fatalf("useful recall = %v, want %v", got, UsefulRecallBoost)
	}
	if got := byPath["Recent.md"].Signals.Recall; got != 0 {
		t.Fatalf("recent-open recall = %v, want zero at selection time", got)
	}

	for _, candidate := range []string{"Dismissed.md", "Snoozed.md"} {
		if err := store.SetFeedback("Source.md", candidate, FeedbackClear, time.Time{}); err != nil {
			t.Fatalf("clear %s: %v", candidate, err)
		}
	}
	restored, err := BuildRanker(documents, nil, RankOptions{
		Now:    func() time.Time { return now },
		Recall: store,
	})
	if err != nil {
		t.Fatalf("BuildRanker restored: %v", err)
	}
	restoredResults, err := restored.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank restored: %v", err)
	}
	restoredByPath := resultsByPath(restoredResults)
	if _, ok := restoredByPath["Dismissed.md"]; !ok {
		t.Fatal("cleared dismissed candidate was not restored")
	}
	if _, ok := restoredByPath["Snoozed.md"]; !ok {
		t.Fatal("cleared snoozed candidate was not restored")
	}
}
