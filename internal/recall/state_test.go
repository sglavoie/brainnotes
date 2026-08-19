package recall

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestStateStoreCorruptAndUnsupportedStateAreNeverOverwritten(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Candidate.md")
	statePath := filepath.Join(root, filepath.FromSlash(recallStateRelPath))
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state directory: %v", err)
	}

	tests := []struct {
		name    string
		content string
		wantErr error
	}{
		{name: "corrupt JSON", content: "{not json\n", wantErr: ErrCorruptState},
		{name: "unsupported version", content: "{\"version\":2,\"records\":{}}\n", wantErr: ErrUnsupportedStateVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(statePath, []byte(test.content), 0o644); err != nil {
				t.Fatalf("seed state: %v", err)
			}
			store := NewStateStore(root)
			if err := store.Validate(); !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if err := store.SetFeedback("Source.md", "Candidate.md", FeedbackUseful, time.Time{}); !errors.Is(err, test.wantErr) {
				t.Fatalf("SetFeedback error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			got, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read preserved state: %v", err)
			}
			if string(got) != test.content {
				t.Fatalf("failed write replaced bad state\ngot:  %q\nwant: %q", got, test.content)
			}
		})
	}

	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove state before cached-missing case: %v", err)
	}
	guarded := NewStateStore(root)
	if err := guarded.Validate(); err != nil {
		t.Fatalf("Validate missing state: %v", err)
	}
	appeared := []byte("{new corruption after validation\n")
	if err := os.WriteFile(statePath, appeared, 0o644); err != nil {
		t.Fatalf("write state after validation: %v", err)
	}
	if err := guarded.SetFeedback("Source.md", "Candidate.md", FeedbackUseful, time.Time{}); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("cached-missing SetFeedback error = %v, want corrupt state", err)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after guarded write: %v", err)
	}
	if !bytes.Equal(got, appeared) {
		t.Fatalf("cached missing snapshot overwrote new corruption: got %q want %q", got, appeared)
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

func TestStateStoreValidatesFeedbackDeadlinesAndSafePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Caf\u00e9.md")
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	store := NewStateStore(root, WithClock(func() time.Time { return now }))

	tests := []struct {
		name      string
		source    string
		candidate string
		feedback  FeedbackState
		until     time.Time
	}{
		{name: "unknown feedback", source: "Source.md", candidate: "Caf\u00e9.md", feedback: "maybe"},
		{name: "later missing deadline", source: "Source.md", candidate: "Caf\u00e9.md", feedback: FeedbackLater},
		{name: "later past deadline", source: "Source.md", candidate: "Caf\u00e9.md", feedback: FeedbackLater, until: now.Add(-time.Hour)},
		{name: "deadline with useful", source: "Source.md", candidate: "Caf\u00e9.md", feedback: FeedbackUseful, until: now.Add(time.Hour)},
		{name: "escaping source", source: "../Source.md", candidate: "Caf\u00e9.md", feedback: FeedbackUseful},
		{name: "absolute source", source: filepath.Join(root, "Source.md"), candidate: "Caf\u00e9.md", feedback: FeedbackUseful},
		{name: "dot segments", source: "Folder/../Source.md", candidate: "Caf\u00e9.md", feedback: FeedbackUseful},
		{name: "same pair", source: "Source.md", candidate: "Source.md", feedback: FeedbackUseful},
		{name: "missing candidate", source: "Source.md", candidate: "Missing.md", feedback: FeedbackUseful},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := store.SetFeedback(test.source, test.candidate, test.feedback, test.until); err == nil {
				t.Fatal("SetFeedback succeeded, want validation error")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes", "recall.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid writes created recall.json: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, ".bnotes", "locks")); err != nil || !info.IsDir() {
		t.Fatalf("invalid write did not use the recall path lock: info=%v err=%v", info, err)
	}

	composedID, err := PairID("Source.md", "Caf\u00e9.md")
	if err != nil {
		t.Fatalf("composed PairID: %v", err)
	}
	decomposedID, err := PairID("Source.md", "Cafe\u0301.md")
	if err != nil {
		t.Fatalf("decomposed PairID: %v", err)
	}
	if composedID != decomposedID {
		t.Fatalf("NFC pair IDs differ: %q != %q", composedID, decomposedID)
	}

	reverseID, err := PairID("Caf\u00e9.md", "Source.md")
	if err != nil {
		t.Fatalf("reverse PairID: %v", err)
	}
	if composedID == reverseID {
		t.Fatalf("directed pair IDs unexpectedly match: %q", composedID)
	}
}

func TestStateStoreRejectsInvalidPersistedRecord(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")
	writeStateRecallNote(t, root, "Candidate.md")
	statePath := filepath.Join(root, filepath.FromSlash(recallStateRelPath))
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state directory: %v", err)
	}
	invalid := recallStateFile{
		Version: recallStateVersion,
		Records: map[string]recallStateRecord{
			"wrong-id": {
				SourceRelPath:    "Source.md",
				CandidateRelPath: "Candidate.md",
				Rating:           string(FeedbackUseful),
				UpdatedAt:        time.Now(),
			},
		},
	}
	data, err := json.Marshal(invalid)
	if err != nil {
		t.Fatalf("marshal invalid state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write invalid state: %v", err)
	}
	if err := NewStateStore(root).Validate(); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("Validate error = %v, want corrupt state", err)
	}
}

func TestFeedbackStateValidity(t *testing.T) {
	t.Parallel()
	valid := []FeedbackState{FeedbackUseful, FeedbackDismissed, FeedbackLater, FeedbackClear}
	for _, state := range valid {
		if !state.Valid() {
			t.Errorf("%q is not valid", state)
		}
	}
	if FeedbackState("unknown").Valid() {
		t.Error("unknown feedback state is valid")
	}
	if got, want := valid, []FeedbackState{"useful", "dismissed", "later", "clear"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("feedback vocabulary = %v, want %v", got, want)
	}
}

func TestStateStoreRejectsSymlinkedSidecar(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "recall.json"), []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write outside state: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".bnotes")); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink .bnotes: %v", err)
	}
	if err := NewStateStore(root).Validate(); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("Validate symlinked sidecar error = %v", err)
	}
}
