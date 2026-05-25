package template

import (
	"errors"
	"strings"
	"testing"
)

func TestFindMissing(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	_, err := Find(cfg, "nope")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestFindEmptyName(t *testing.T) {
	cfg := newDiscoverConfig(t)
	if _, err := Find(cfg, ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestFindWhitespaceOnlyName(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	for _, name := range []string{"   ", "\t", " \n "} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error for whitespace-only name", name)
		}
	}
}

func TestFindRejectsPathSeparators(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	for _, name := range []string{"../etc/passwd", "sub/book", `sub\book`} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error, got nil", name)
		}
	}
}

func TestFindNilConfig(t *testing.T) {
	if _, err := Find(nil, "book"); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestFindDirectoriesIgnored(t *testing.T) {
	// A subdirectory named like a template must not be returned.
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates", "Book.md"))
	_, err := Find(cfg, "book")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestFindNotFoundIncludesCandidates(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	_, err := Find(cfg, "ghost")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"ghost", "ghost.md", "ghost Template.md"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing candidate %q", msg, want)
		}
	}
}

func TestFindMissingTemplateDir(t *testing.T) {
	// When the templates directory does not exist, Find should return an error
	// (not panic or return an empty path).
	cfg := newDiscoverConfig(t)
	// Do not create the Templates directory.
	_, err := Find(cfg, "book")
	if err == nil {
		t.Fatal("expected error for missing template dir")
	}
	if strings.Contains(err.Error(), "nil pointer") {
		t.Fatalf("got nil pointer error; should be a clean read error: %v", err)
	}
}

func TestFindRejectsNULByte(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	if _, err := Find(cfg, "book\x00.md"); err == nil {
		t.Fatal("expected error for NUL byte in name")
	}
}

func TestFindRejectsDotNames(t *testing.T) {
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	for _, name := range []string{".", ".."} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error", name)
		}
	}
}

func TestFindEmptyStemAfterExtensionStrip(t *testing.T) {
	// A name that consists only of the configured extension reduces to an
	// empty stem; the third candidate (" Template.md") would have a leading
	// space and could accidentally match a poorly-named file. Reject early.
	cfg := newDiscoverConfig(t)
	mustMkdirAll(t, vaultPath(cfg, "Templates"))
	for _, name := range []string{".md", ".MD"} {
		if _, err := Find(cfg, name); err == nil {
			t.Fatalf("Find(%q): expected error for empty stem", name)
		}
	}
}
