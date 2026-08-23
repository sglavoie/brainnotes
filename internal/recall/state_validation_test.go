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
