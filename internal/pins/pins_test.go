package pins

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestClassify_HostileValues(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		present  bool
		wantRank float64
		wantKind Kind
	}{
		{name: "float", value: 1.5, present: true, wantRank: 1.5, wantKind: Ranked},
		{name: "whole float", value: 2.0, present: true, wantRank: 2, wantKind: Ranked},
		{name: "negative float", value: -3.0, present: true, wantRank: -3, wantKind: Ranked},
		{name: "int", value: 3, present: true, wantRank: 3, wantKind: Ranked},
		{name: "int64", value: int64(4), present: true, wantRank: 4, wantKind: Ranked},
		{name: "numeric string", value: "2.5", present: true, wantRank: 2.5, wantKind: Ranked},
		{name: "padded numeric string", value: " 2.5 ", present: true, wantRank: 2.5, wantKind: Ranked},
		{name: "false", value: false, present: true, wantKind: NotPinned},
		{name: "absent", value: nil, present: false, wantKind: NotPinned},
		{name: "true", value: true, present: true, wantKind: Unranked},
		{name: "non-numeric string", value: "high", present: true, wantKind: Unranked},
		{name: "empty string", value: "", present: true, wantKind: Unranked},
		{name: "list", value: []any{1.0, 2.0}, present: true, wantKind: Unranked},
		{name: "map", value: map[string]any{"rank": 1.0}, present: true, wantKind: Unranked},
		{name: "time", value: time.Now(), present: true, wantKind: Unranked},
		{name: "nil present", value: nil, present: true, wantKind: Unranked},
		{name: "NaN", value: math.NaN(), present: true, wantKind: Unranked},
		{name: "positive infinity", value: math.Inf(1), present: true, wantKind: Unranked},
		{name: "negative infinity", value: math.Inf(-1), present: true, wantKind: Unranked},
		{name: "infinite string", value: "Inf", present: true, wantKind: Unranked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rank, kind := Classify(tc.value, tc.present)
			if kind != tc.wantKind {
				t.Fatalf("Classify(%#v, %v) kind = %v, want %v", tc.value, tc.present, kind, tc.wantKind)
			}
			if kind == Ranked && rank != tc.wantRank {
				t.Fatalf("Classify(%#v, %v) rank = %v, want %v", tc.value, tc.present, rank, tc.wantRank)
			}
		})
	}
}

func newVault(t *testing.T, files map[string]string) (string, []*vault.Note) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	v, err := vault.Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	return v.Root, notes
}

func relPaths(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.RelPath)
	}
	return out
}

func assertOrder(t *testing.T, got []Entry, want []string) {
	t.Helper()
	gotPaths := relPaths(got)
	if len(gotPaths) != len(want) {
		t.Fatalf("order = %v, want %v", gotPaths, want)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Fatalf("order = %v, want %v", gotPaths, want)
		}
	}
}

func TestList_OrdersRankedBeforeUnrankedAndSkipsUnpinned(t *testing.T) {
	root, notes := newVault(t, map[string]string{
		"b.md":        "---\npinned: 2.0\n---\nbody\n",
		"a.md":        "---\npinned: 1.0\n---\nbody\n",
		"neg.md":      "---\npinned: -1.5\n---\nbody\n",
		"true.md":     "---\npinned: true\n---\nbody\n",
		"high.md":     "---\npinned: high\n---\nbody\n",
		"off.md":      "---\npinned: false\n---\nbody\n",
		"plain.md":    "---\ntitle: plain\n---\nbody\n",
		"folder/c.md": "---\npinned: 1.5\n---\nbody\n",
	})

	got := List(root, notes)
	assertOrder(t, got, []string{"neg.md", "a.md", "folder/c.md", "b.md", "high.md", "true.md"})
	if got[0].Kind != Ranked || got[len(got)-1].Kind != Unranked {
		t.Fatalf("kinds = %v", got)
	}
}

func TestList_DuplicateRanksBreakTieOnPathDeterministically(t *testing.T) {
	root, notes := newVault(t, map[string]string{
		"zebra.md":  "---\npinned: 1.0\n---\nbody\n",
		"apple.md":  "---\npinned: 1.0\n---\nbody\n",
		"middle.md": "---\npinned: 1.0\n---\nbody\n",
	})

	want := []string{"apple.md", "middle.md", "zebra.md"}
	for i := 0; i < 5; i++ {
		assertOrder(t, List(root, notes), want)
	}
}

func TestList_NormalizesPathForTiebreak(t *testing.T) {
	// A decomposed "é" starts with the byte 'e', so a raw byte comparison
	// would sort it before "z.md"; composed it is U+00E9 and sorts after.
	decomposed := "e\u0301.md"
	root, notes := newVault(t, map[string]string{
		decomposed: "---\npinned: 1.0\n---\nbody\n",
		"z.md":     "---\npinned: 1.0\n---\nbody\n",
	})

	got := List(root, notes)
	if len(got) != 2 {
		t.Fatalf("List = %v, want 2 entries", relPaths(got))
	}
	if got[0].RelPath != "z.md" {
		t.Fatalf("order = %v, want z.md first", relPaths(got))
	}
}

func TestList_SkipsUnreadableNoteWithoutHidingTheRest(t *testing.T) {
	root, notes := newVault(t, map[string]string{
		"good.md": "---\npinned: 1.0\n---\nbody\n",
	})
	// Vault.Notes refuses to load a note with broken frontmatter at all, so
	// the hostile note is handed in as a bare handle the way a caller holding
	// a stale path would.
	broken := filepath.Join(root, "bad.md")
	if err := os.WriteFile(broken, []byte("---\npinned: [1.0\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write bad.md: %v", err)
	}
	notes = append(notes, vault.NewNote(root, broken), vault.NewNote(root, filepath.Join(root, "missing.md")))

	assertOrder(t, List(root, notes), []string{"good.md"})
}

func TestList_SkipsNoteOutsideRoot(t *testing.T) {
	root, notes := newVault(t, map[string]string{
		"good.md": "---\npinned: 1.0\n---\nbody\n",
	})
	outside := t.TempDir()
	path := filepath.Join(outside, "elsewhere.md")
	if err := os.WriteFile(path, []byte("---\npinned: 1.0\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write elsewhere.md: %v", err)
	}

	assertOrder(t, List(root, append(notes, vault.NewNote(root, path))), []string{"good.md"})
}

func TestList_NilNoteIsSkipped(t *testing.T) {
	root, notes := newVault(t, map[string]string{
		"good.md": "---\npinned: 1.0\n---\nbody\n",
	})

	assertOrder(t, List(root, append([]*vault.Note{nil}, notes...)), []string{"good.md"})
}
