package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetSplitArgs(t *testing.T) {
	notes, pairs, err := splitSetArgs([]string{"a.md", "b.md", "k1=v1", "k2=v2"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(notes) != 2 || notes[0] != "a.md" || notes[1] != "b.md" {
		t.Fatalf("notes mismatch: %v", notes)
	}
	if len(pairs) != 2 || pairs[0] != "k1=v1" || pairs[1] != "k2=v2" {
		t.Fatalf("pairs mismatch: %v", pairs)
	}

	if _, _, err := splitSetArgs([]string{"a.md", "k=v", "b.md"}); err == nil {
		t.Fatal("expected error for note after pair")
	}
}

func TestCoerceValueAuto(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"FALSE", false},
		{"42", 42},
		{"-3", -3},
		{"-0", 0},
		{"2026-01-01", nil},
		{"hello", "hello"},
		{"-hello", "-hello"},
	}
	for _, c := range cases {
		got, err := coerceValue(c.in, "")
		if err != nil {
			t.Fatalf("coerceValue(%q) err=%v", c.in, err)
		}
		if c.in == "2026-01-01" {
			if _, ok := got.(interface{ Year() int }); !ok {
				t.Fatalf("expected time.Time for date, got %T", got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("coerceValue(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestSetNegativeInt(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "rating=-3"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "rating: -3\n") {
		t.Fatalf("expected `rating: -3` (int), got:\n%s", got)
	}
	if strings.Contains(got, `rating: "-3"`) || strings.Contains(got, "rating: '-3'") {
		t.Fatalf("negative int should not be quoted as a string, got:\n%s", got)
	}
}

func TestSetOp_RejectsInvalidPair(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	_, err := runCLI(t, "", "set", notePath, "=foo")
	if err == nil {
		t.Fatal("expected error for pair with empty key")
	}
}

func TestSetOp_RejectsNonListKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	_, err := runCLI(t, "", "set", notePath, "status+=foo")
	if err == nil {
		t.Fatal("expected error for += on non-list key")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status") || !strings.Contains(msg, "list_properties") {
		t.Fatalf("error should mention key and config knob, got: %v", err)
	}
}

func TestSetOp_RejectsEmptyValue(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	if _, err := runCLI(t, "", "set", notePath, "tags+="); err == nil {
		t.Fatal("expected error for += with empty value")
	}
	if _, err := runCLI(t, "", "set", notePath, "tags-="); err == nil {
		t.Fatal("expected error for -= with empty value")
	}
}

func TestSetOp_RejectsOnExistingScalar(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "scalar.md")
	if err := os.WriteFile(notePath, []byte("---\ntags: lonely\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	_, err := runCLI(t, "", "set", notePath, "tags+=foo")
	if err == nil {
		t.Fatal("expected error when += targets a scalar")
	}
	if !strings.Contains(err.Error(), "not a list") {
		t.Fatalf("expected 'not a list' error, got: %v", err)
	}
}
