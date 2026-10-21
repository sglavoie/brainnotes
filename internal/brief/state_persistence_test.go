package brief

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
)

func TestExternalStateWriteJoinsVaultCheckpoint(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	store := NewFileStateStore(root, WithStateRoot(external))
	statePath := filepath.Join(external, filepath.FromSlash(briefStateRelPath))

	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "test external activity", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- store.writeState(root, statePath, []byte("{}\n")) }()

	select {
	case err := <-done:
		t.Fatalf("external state write completed during checkpoint: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("external state appeared during checkpoint: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("external state write after checkpoint: %v", err)
	}
}

func TestExternalStateWriteDoesNotNeedAReadOnlyUnmanagedVaultLock(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("# Note\n"), 0o444); err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("make vault read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	store := NewFileStateStore(root, WithStateRoot(external))
	if _, err := store.RecordVisit(LocalDate("2026-09-22"), time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record visit against read-only unmanaged vault: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("read-only vault gained checkpoint state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(external, filepath.FromSlash(briefStateRelPath))); err != nil {
		t.Fatalf("external activity was not written: %v", err)
	}
}

func TestFileStateStoreCorruptAndUnsupportedStateAreNeverOverwritten(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBriefNote(t, root, "Source.md")
	statePath := filepath.Join(root, filepath.FromSlash(briefStateRelPath))
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state directory: %v", err)
	}
	date := LocalDate("2026-08-04")
	validSelection := DailySelection{
		Date:  date,
		Cards: []Card{makeCard(t, date, SlotContinue, "Source.md", "")},
	}

	tests := []struct {
		name    string
		content string
		wantErr error
	}{
		{name: "corrupt JSON", content: "{not json\n", wantErr: ErrCorruptState},
		{name: "unsupported version", content: "{\"version\":2,\"daily_selections\":{}}\n", wantErr: ErrUnsupportedStateVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(statePath, []byte(test.content), 0o644); err != nil {
				t.Fatalf("seed state: %v", err)
			}
			store := NewFileStateStore(root)
			if err := store.Validate(); !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if err := store.SaveDailySelection(validSelection); !errors.Is(err, test.wantErr) {
				t.Fatalf("SaveDailySelection error = %v, want errors.Is(_, %v)", err, test.wantErr)
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
	guarded := NewFileStateStore(root)
	if err := guarded.Validate(); err != nil {
		t.Fatalf("Validate missing state: %v", err)
	}
	appeared := []byte("{new corruption after validation\n")
	if err := os.WriteFile(statePath, appeared, 0o644); err != nil {
		t.Fatalf("write state after validation: %v", err)
	}
	if err := guarded.SaveDailySelection(validSelection); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("cached-missing SaveDailySelection error = %v, want corrupt state", err)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after guarded write: %v", err)
	}
	if !bytes.Equal(got, appeared) {
		t.Fatalf("cached missing snapshot overwrote corruption: got %q want %q", got, appeared)
	}
}

func TestFileStateStoreRejectsInvalidSelectionAndUnsafeSidecarPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBriefNote(t, root, "Source.md")
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	store := NewFileStateStore(root, WithClock(func() time.Time { return now }))
	date := localDateAt(now)

	invalid := DailySelection{
		Date: date,
		Cards: []Card{
			{
				ID:   CardID("not-a-sha"),
				Slot: SlotContinue,
				Source: NoteRef{
					RelPath: "../Source.md",
				},
			},
		},
	}
	if err := store.SaveDailySelection(invalid); err == nil {
		t.Fatal("SaveDailySelection invalid card succeeded, want validation error")
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("invalid write created .bnotes: %v", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".bnotes")); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink .bnotes: %v", err)
	}
	if err := NewFileStateStore(root).Validate(); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("Validate symlinked sidecar error = %v", err)
	}
}

func TestFileStateStoreSerializesConcurrentWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBriefNote(t, root, "Source.md")
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	store := NewFileStateStore(root)
	cardID := mustCardID(t, localDateAt(now), SlotContinue, "Source.md", "")

	const writes = 24
	var wg sync.WaitGroup
	errs := make(chan error, writes)
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.RecordOpen(cardID, now.Add(time.Duration(i)*time.Minute))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RecordOpen concurrent write error: %v", err)
		}
	}

	reloaded := NewFileStateStore(root)
	activity, err := reloaded.RollingActivity(now.Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("RollingActivity after concurrent writes: %v", err)
	}
	if got, want := activity.Last7Days.OpenCount, writes; got != want {
		t.Fatalf("Last7Days.OpenCount after concurrent writes = %d, want %d", got, want)
	}
}
