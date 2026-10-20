package restorestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSource writes a stand-in archive outside the vault and returns its path.
func writeSource(t *testing.T, data string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(source, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestLoad_NoIntentIsAbsence(t *testing.T) {
	root := t.TempDir()
	intent, err := Load(root)
	if err != nil || intent != nil {
		t.Fatalf("Load = %#v, %v; want nil, nil", intent, err)
	}
	pending, err := Pending(root)
	if err != nil || pending {
		t.Fatalf("Pending = %v, %v; want false, nil", pending, err)
	}
}

func TestBeginSetPhaseLoadClear_RoundTrip(t *testing.T) {
	root := t.TempDir()
	source := writeSource(t, "archive bytes")
	before := time.Now().UTC()

	begun, err := Begin(root, source, true)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	assertBegun(t, root, source, before, begun)
	assertLoaded(t, root, begun)
	if pending, err := Pending(root); err != nil || !pending {
		t.Fatalf("Pending = %v, %v; want true, nil", pending, err)
	}

	if err := SetPhase(root, begun, PhaseHistory); err != nil {
		t.Fatalf("SetPhase: %v", err)
	}
	if begun.Phase != PhaseHistory {
		t.Fatalf("SetPhase left Phase = %q, want %q", begun.Phase, PhaseHistory)
	}
	assertLoaded(t, root, begun)

	if err := Clear(root); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	assertCleared(t, root)
	if err := Clear(root); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
}

// assertBegun checks what Begin returned and what it left on disk: the staged
// copy of the archive and owner-only permissions on the recovery state.
func assertBegun(t *testing.T, root, source string, before time.Time, begun *Intent) {
	t.Helper()
	wantArchive := filepath.Join(root, RelDir, archiveName)
	if begun.Version != version || begun.Source != source || begun.Archive != wantArchive || !begun.Wipe || begun.Phase != PhaseContent {
		t.Fatalf("Begin = %#v", begun)
	}
	if begun.Started.Before(before) || begun.Started.Location() != time.UTC {
		t.Fatalf("Started = %v, want a UTC time at or after %v", begun.Started, before)
	}
	staged, err := os.ReadFile(wantArchive)
	if err != nil || string(staged) != "archive bytes" {
		t.Fatalf("staged archive = %q, %v", staged, err)
	}
	assertMode(t, filepath.Join(root, ".bnotes"), 0o700)
	assertMode(t, filepath.Join(root, RelDir), 0o700)
	assertMode(t, path(root), 0o600)
}

func assertLoaded(t *testing.T, root string, want *Intent) {
	t.Helper()
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSameIntent(t, loaded, want)
}

func assertCleared(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, RelDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("recovery directory after Clear: %v, want not exist", err)
	}
	if intent, err := Load(root); err != nil || intent != nil {
		t.Fatalf("Load after Clear = %#v, %v; want nil, nil", intent, err)
	}
}

func TestSetPhase_RefusesUnknownPhaseWithoutWriting(t *testing.T) {
	root := t.TempDir()
	intent, err := Begin(root, writeSource(t, "x"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetPhase(root, intent, "finished"); err == nil || !strings.Contains(err.Error(), "invalid phase") {
		t.Fatalf("SetPhase(finished) = %v, want invalid phase", err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.Phase != PhaseContent {
		t.Fatalf("Load = %#v, %v; want the content phase untouched", loaded, err)
	}
}

func TestBegin_MissingSourcePublishesNoIntent(t *testing.T) {
	root := t.TempDir()
	if _, err := Begin(root, filepath.Join(t.TempDir(), "absent.tar.gz"), false); err == nil {
		t.Fatal("Begin with a missing source succeeded")
	}
	if intent, err := Load(root); err != nil || intent != nil {
		t.Fatalf("Load = %#v, %v; want nil, nil", intent, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, RelDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("recovery directory holds %d entries after a failed Begin, want none", len(entries))
	}
}

func TestLoad_RejectsMalformedOrForeignIntents(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, RelDir, archiveName)
	valid := func() map[string]any {
		return map[string]any{
			"version": version,
			"source":  "/elsewhere/backup.tar.gz",
			"archive": archive,
			"wipe":    false,
			"phase":   PhaseContent,
			"started": "2026-01-02T03:04:05Z",
		}
	}
	with := func(key string, value any) string {
		record := valid()
		if value == nil {
			delete(record, key)
		} else {
			record[key] = value
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	cases := []struct {
		name string
		data string
		want string
	}{
		{"malformed JSON", `{"version": 1,`, "decode intent"},
		{"wrong version", with("version", version+1), "unsupported or incomplete"},
		{"missing version", with("version", nil), "unsupported or incomplete"},
		{"unknown phase", with("phase", "finished"), "unsupported or incomplete"},
		{"missing archive", with("archive", nil), "unsupported or incomplete"},
		{"archive outside the recovery directory", with("archive", filepath.Join(t.TempDir(), archiveName)), "escapes recovery directory"},
		{"archive escaping by dot-dot", with("archive", filepath.Join(root, RelDir, "..", "..", archiveName)), "escapes recovery directory"},
		{"different file in the recovery directory", with("archive", filepath.Join(root, RelDir, "other.tar.gz")), "escapes recovery directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeIntent(t, root, tc.data)
			assertRejected(t, root, tc.want)
		})
	}

	t.Run("an equivalent spelling of the archive path is accepted", func(t *testing.T) {
		writeIntent(t, root, with("archive", filepath.Join(root, RelDir, ".", archiveName)))
		intent, err := Load(root)
		if err != nil || intent == nil {
			t.Fatalf("Load = %#v, %v; want the intent", intent, err)
		}
	})
}

// assertRejected requires Load and Pending both to refuse the intent on disk.
func assertRejected(t *testing.T, root, want string) {
	t.Helper()
	intent, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Load = %#v, %v; want an error containing %q", intent, err, want)
	}
	if intent != nil {
		t.Fatalf("Load returned %#v alongside an error", intent)
	}
	if pending, err := Pending(root); err == nil || pending {
		t.Fatalf("Pending = %v, %v; want false and the same error", pending, err)
	}
}

func writeIntent(t *testing.T, root, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, RelDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(root), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSameIntent(t *testing.T, got, want *Intent) {
	t.Helper()
	if got == nil {
		t.Fatal("intent is nil")
	}
	if got.Version != want.Version || got.Source != want.Source || got.Archive != want.Archive || got.Wipe != want.Wipe || got.Phase != want.Phase || !got.Started.Equal(want.Started) {
		t.Fatalf("intent = %#v, want %#v", got, want)
	}
}

func assertMode(t *testing.T, p string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", p, got, want)
	}
}
