package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBookRouting(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	if !strings.Contains(out, created) {
		t.Fatalf("stdout %q does not mention %q", out, created)
	}
	data, err := os.ReadFile(created)
	if err != nil {
		t.Fatalf("read created: %v", err)
	}
	got := string(data)
	want := "---\ntitle: Dune\ncreated: 2026-05-18\nauthor: Herbert\n---\nslug: dune\n"
	if got != want {
		t.Fatalf("content mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestNewRouteLookupCaseInsensitive(t *testing.T) {
	root := buildNewFixtureWithRouteKey(t, "BOOK")

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	if !strings.Contains(out, created) {
		t.Fatalf("stdout %q does not mention %q", out, created)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected %s to exist: %v", created, err)
	}
}

func TestNewRouteLookupMatchesResolvedTemplateName(t *testing.T) {
	tests := []struct {
		name        string
		routeKey    string
		templateArg string
		title       string
	}{
		{name: "route key book matches book.md", routeKey: "book", templateArg: "book.md", title: "Dune"},
		{name: "route key book matches Book Template", routeKey: "book", templateArg: "Book Template", title: "Beloved"},
		{name: "route key Book Template matches book", routeKey: `"Book Template"`, templateArg: "book", title: "Hyperion"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := buildNewFixtureWithRouteKey(t, tc.routeKey)

			out, err := runNew(t, "new", tc.templateArg, tc.title, "--var", "author=Herbert")
			if err != nil {
				t.Fatalf("execute: %v\noutput: %s", err, out)
			}

			created := filepath.Join(root, "References", "Books", tc.title+".md")
			if !strings.Contains(out, created) {
				t.Fatalf("stdout %q does not mention %q", out, created)
			}
			if _, err := os.Stat(created); err != nil {
				t.Fatalf("expected %s to exist: %v", created, err)
			}
		})
	}
}

func TestNewRouteLookupUsesCanonicalDestinationForEquivalentAliases(t *testing.T) {
	root := buildNewFixtureWithRoutes(t, "book = \"References/Books\"\n\"Book Template\" = \" ./References/Books/ \"\n")

	tests := []struct {
		templateArg string
		title       string
	}{
		{templateArg: "book", title: "Dune"},
		{templateArg: "book.md", title: "Hyperion"},
		{templateArg: "Book Template", title: "Beloved"},
	}
	for _, tc := range tests {
		t.Run(tc.templateArg, func(t *testing.T) {
			out, err := runNew(t, "new", tc.templateArg, tc.title, "--var", "author=Herbert")
			if err != nil {
				t.Fatalf("execute: %v\noutput: %s", err, out)
			}

			created := filepath.Join(root, "References", "Books", tc.title+".md")
			if !strings.Contains(out, created) {
				t.Fatalf("stdout %q does not mention %q", out, created)
			}
			if _, err := os.Stat(created); err != nil {
				t.Fatalf("expected %s to exist: %v", created, err)
			}
		})
	}
}

func TestNewRouteLookupRejectsConflictingAliasesForSameTemplate(t *testing.T) {
	tests := []struct {
		templateArg string
		title       string
	}{
		{templateArg: "book", title: "Dune"},
		{templateArg: "book.md", title: "Hyperion"},
		{templateArg: "Book Template", title: "Beloved"},
	}
	for _, tc := range tests {
		t.Run(tc.templateArg, func(t *testing.T) {
			root := buildNewFixtureWithRoutes(t, "book = \"References/Books\"\n\"Book Template\" = \"People\"\n")

			_, err := runNew(t, "new", tc.templateArg, tc.title, "--var", "author=Herbert")
			if err == nil {
				t.Fatal("expected conflicting routes error, got nil")
			}
			if !strings.Contains(err.Error(), "conflicting routes") {
				t.Fatalf("expected conflicting routes error, got %v", err)
			}
			if !strings.Contains(err.Error(), `"Book Template"`) || !strings.Contains(err.Error(), `"book"`) {
				t.Fatalf("expected conflicting keys in error, got %v", err)
			}

			for _, created := range []string{
				filepath.Join(root, "References", "Books", tc.title+".md"),
				filepath.Join(root, "People", tc.title+".md"),
			} {
				if _, statErr := os.Stat(created); !os.IsNotExist(statErr) {
					t.Fatalf("unexpected file created at %s", created)
				}
			}
		})
	}
}

func TestNewRouteLookupCaseOnlyDestinationDifferenceMatchesFilesystem(t *testing.T) {
	root := buildNewFixtureWithRoutes(t, "book = \"References/Books\"\n\"Book Template\" = \"references/books\"\n")

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert")
	if newTestFilesystemCaseInsensitive(t, root) {
		if err != nil {
			t.Fatalf("execute: %v\noutput: %s", err, out)
		}
		created := strings.TrimSpace(out)
		if created == "" {
			t.Fatal("expected created path on stdout, got empty output")
		}
		if _, err := os.Stat(created); err != nil {
			t.Fatalf("expected %s to exist: %v", created, err)
		}
		rel, err := filepath.Rel(root, created)
		if err != nil {
			t.Fatalf("rel created path: %v", err)
		}
		if !strings.EqualFold(filepath.ToSlash(rel), "references/books/Dune.md") {
			t.Fatalf("created relative path = %q, want case-insensitive references/books/Dune.md", filepath.ToSlash(rel))
		}
		return
	}

	if err == nil {
		t.Fatal("expected conflicting routes error on case-sensitive filesystem, got nil")
	}
	if !strings.Contains(err.Error(), "conflicting routes") {
		t.Fatalf("expected conflicting routes error, got %v", err)
	}
}
