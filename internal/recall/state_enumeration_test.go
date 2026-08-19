package recall

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateStoreActiveFeedbackForSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, relativePath := range []string{
		"Source.md",
		"OtherSource.md",
		"A-Dismissed.md",
		"B-Later.md",
		"C-Useful.md",
		"Expired.md",
		"OpenOnly.md",
		"Cleared.md",
		"Missing.md",
		"OtherCandidate.md",
		"StaleSource.md",
		"StaleCandidate.md",
	} {
		writeStateRecallNote(t, root, relativePath)
	}
	location := time.FixedZone("recall-enumeration", -6*60*60)
	initial := time.Date(2026, time.July, 13, 15, 30, 0, 0, location)
	now := initial
	store := NewStateStore(root, WithClock(func() time.Time { return now }))
	deadline := time.Date(2026, time.July, 20, 0, 0, 0, 0, location)

	set := func(source, candidate string, state FeedbackState, until time.Time) {
		t.Helper()
		if err := store.SetFeedback(source, candidate, state, until); err != nil {
			t.Fatalf("SetFeedback(%s, %s, %s): %v", source, candidate, state, err)
		}
	}
	set("Source.md", "C-Useful.md", FeedbackUseful, time.Time{})
	set("Source.md", "A-Dismissed.md", FeedbackDismissed, time.Time{})
	set("Source.md", "B-Later.md", FeedbackLater, deadline)
	set("Source.md", "Expired.md", FeedbackLater, initial.Add(time.Hour))
	set("Source.md", "Missing.md", FeedbackUseful, time.Time{})
	set("Source.md", "Cleared.md", FeedbackUseful, time.Time{})
	if err := store.RecordOpened("Source.md", "OpenOnly.md"); err != nil {
		t.Fatalf("RecordOpened open-only: %v", err)
	}
	if err := store.RecordOpened("Source.md", "Cleared.md"); err != nil {
		t.Fatalf("RecordOpened cleared: %v", err)
	}
	set("Source.md", "Cleared.md", FeedbackClear, time.Time{})
	set("OtherSource.md", "OtherCandidate.md", FeedbackUseful, time.Time{})
	set("StaleSource.md", "StaleCandidate.md", FeedbackUseful, time.Time{})
	if err := os.Remove(filepath.Join(root, "Missing.md")); err != nil {
		t.Fatalf("remove missing candidate: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "StaleSource.md")); err != nil {
		t.Fatalf("remove stale source: %v", err)
	}
	now = initial.Add(2 * time.Hour)

	before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(recallStateRelPath)))
	if err != nil {
		t.Fatalf("read state before enumeration: %v", err)
	}
	got, err := NewStateStore(root, WithClock(func() time.Time { return now })).ActiveFeedbackForSource("Source.md")
	if err != nil {
		t.Fatalf("ActiveFeedbackForSource: %v", err)
	}
	want := []struct {
		candidate string
		state     FeedbackState
	}{
		{candidate: "A-Dismissed.md", state: FeedbackDismissed},
		{candidate: "B-Later.md", state: FeedbackLater},
		{candidate: "C-Useful.md", state: FeedbackUseful},
	}
	if len(got) != len(want) {
		t.Fatalf("active feedback = %#v, want %d entries", got, len(want))
	}
	for i, entry := range got {
		if entry.CandidateRelPath != want[i].candidate || entry.State != want[i].state {
			t.Fatalf("active feedback[%d] = %#v, want candidate=%q state=%q", i, entry, want[i].candidate, want[i].state)
		}
	}
	if !got[1].Until.Equal(deadline) || got[1].Until.Format(time.RFC3339) != deadline.Format(time.RFC3339) {
		t.Fatalf("later deadline = %s, want %s", got[1].Until.Format(time.RFC3339), deadline.Format(time.RFC3339))
	}
	after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(recallStateRelPath)))
	if err != nil {
		t.Fatalf("read state after enumeration: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("enumeration rewrote stale state\nbefore:\n%s\nafter:\n%s", before, after)
	}
	staleSource, err := NewStateStore(root, WithClock(func() time.Time { return now })).ActiveFeedbackForSource("StaleSource.md")
	if err != nil || staleSource != nil {
		t.Fatalf("stale-source feedback = %#v, err=%v", staleSource, err)
	}

	got[0].CandidateRelPath = "mutated"
	got[1].State = FeedbackUseful
	got[1].Until = time.Time{}
	again, err := NewStateStore(root, WithClock(func() time.Time { return now })).ActiveFeedbackForSource("Source.md")
	if err != nil {
		t.Fatalf("ActiveFeedbackForSource defensive reread: %v", err)
	}
	if len(again) != len(want) || again[0].CandidateRelPath != want[0].candidate || again[1].State != want[1].state || !again[1].Until.Equal(deadline) {
		t.Fatalf("mutating returned feedback changed state: %#v", again)
	}

	for _, source := range []string{"MissingSource.md", "../Source.md", "Folder/../Source.md"} {
		feedback, err := NewStateStore(root).ActiveFeedbackForSource(source)
		if source == "MissingSource.md" {
			if err != nil || feedback != nil {
				t.Fatalf("missing source feedback = %#v, err=%v", feedback, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ActiveFeedbackForSource(%q) succeeded, want path error", source)
		}
	}
}

func TestStateStoreActiveFeedbackMissingStateIsLazy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStateRecallNote(t, root, "Source.md")

	feedback, err := NewStateStore(root).ActiveFeedbackForSource("Source.md")
	if err != nil {
		t.Fatalf("ActiveFeedbackForSource missing state: %v", err)
	}
	if feedback != nil {
		t.Fatalf("missing-state feedback = %#v, want nil", feedback)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("enumeration created .bnotes: %v", err)
	}
}

func TestStateStoreActiveFeedbackRejectsUnsafeState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		wantErr error
	}{
		{name: "corrupt", content: "{not json\n", wantErr: ErrCorruptState},
		{name: "unsupported", content: "{\"version\":2,\"records\":{}}\n", wantErr: ErrUnsupportedStateVersion},
		{name: "invalid record", wantErr: ErrCorruptState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeStateRecallNote(t, root, "Source.md")
			writeStateRecallNote(t, root, "Candidate.md")
			statePath := filepath.Join(root, filepath.FromSlash(recallStateRelPath))
			if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
				t.Fatalf("mkdir state directory: %v", err)
			}
			content := test.content
			if content == "" {
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
				content = string(data)
			}
			if err := os.WriteFile(statePath, []byte(content), 0o644); err != nil {
				t.Fatalf("write state: %v", err)
			}
			if _, err := NewStateStore(root).ActiveFeedbackForSource("Source.md"); !errors.Is(err, test.wantErr) {
				t.Fatalf("ActiveFeedbackForSource error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
		})
	}
}

func TestStateStoreActiveFeedbackRejectsSymlinkedSidecar(t *testing.T) {
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
	if _, err := NewStateStore(root).ActiveFeedbackForSource("Source.md"); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("ActiveFeedbackForSource symlinked sidecar error = %v", err)
	}
}

func TestStateStoreActiveFeedbackRejectsSymlinkedStateFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideState := filepath.Join(outside, "recall.json")
	if err := os.WriteFile(outsideState, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write outside state: %v", err)
	}
	stateDirectory := filepath.Join(root, ".bnotes")
	if err := os.MkdirAll(stateDirectory, 0o755); err != nil {
		t.Fatalf("mkdir state directory: %v", err)
	}
	if err := os.Symlink(outsideState, filepath.Join(stateDirectory, "recall.json")); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink recall.json: %v", err)
	}
	if _, err := NewStateStore(root).ActiveFeedbackForSource("Source.md"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("ActiveFeedbackForSource symlinked state error = %v", err)
	}
}
