package cli

import (
	"strings"
	"testing"
)

func TestRenameRejectsSlashInTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "Books/Dune")
	if err == nil {
		t.Fatal("expected slash-in-title error")
	}
	if !strings.Contains(err.Error(), "may not contain '/'") {
		t.Fatalf("expected slash rejection, got %v", err)
	}
}

func TestRenameRejectsEmptyTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "")
	if err == nil {
		t.Fatal("expected empty-title error")
	}
}

func TestRenameRejectsIdenticalTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "Dune")
	if err == nil {
		t.Fatal("expected identical-title error")
	}
}

// TestRenameRejectsWhitespaceTitle locks in that titles consisting only of
// whitespace are rejected before any file is created. Before the fix, a title
// like "   " produced "   .md" on disk.
func TestRenameRejectsWhitespaceTitle(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"   ", "\t", "\n", " \t "}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for whitespace-only title %q", title)
		}
		if !strings.Contains(err.Error(), "whitespace") && !strings.Contains(err.Error(), "control character") {
			t.Fatalf("expected whitespace/control-char rejection for %q, got %v", title, err)
		}
	}
}

// TestRenameRejectsLeadingTrailingWhitespace locks in that titles with
// leading/trailing whitespace are rejected. Before the fix, "  Foo" became
// "  Foo.md" on disk.
func TestRenameRejectsLeadingTrailingWhitespace(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"  Foo", "Foo  ", " Foo ", "\tFoo"}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for padded title %q", title)
		}
	}
}

// TestRenameRejectsControlCharsInTitle locks in that titles containing ASCII
// control characters (newline, NUL, DEL, etc.) are rejected. Before the fix,
// embedded newlines produced files with literal newlines in their names.
func TestRenameRejectsControlCharsInTitle(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"Foo\nBar", "Foo\rBar", "Foo\x00Bar", "Foo\x7fBar", "A\tB"}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for control-char title %q", title)
		}
	}
}

func TestRenameRejectsWikilinkReservedCharsInTitle(t *testing.T) {
	cases := []string{"Foo#Bar", "Foo|Bar", "Foo[Bar", "Foo]Bar"}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			root := buildRenameFixture(t)
			before := snapshotVault(t, root)

			_, err := runCLI(t, "", "rename", "Dune", title)
			if err == nil {
				t.Fatalf("expected rejection for reserved-char title %q", title)
			}
			if !strings.Contains(err.Error(), "wikilink") && !strings.Contains(err.Error(), "may not contain") {
				t.Fatalf("expected wikilink-safe title error for %q, got %v", title, err)
			}
			if after := snapshotVault(t, root); !mapsEqual(before, after) {
				t.Fatalf("rename rewrote files for rejected title %q:\nbefore=%v\nafter=%v", title, before, after)
			}
		})
	}
}
