package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// Note is a handle to a markdown file in the vault. Construction is cheap:
// the file is not read until Load or an accessor needs it.
type Note struct {
	// Path is the absolute path on disk.
	Path string

	doc    frontmatter.Document
	loaded bool
}

// Load reads and parses the note from disk. Safe to call repeatedly; only the
// first call performs I/O.
func (n *Note) Load() error {
	if n == nil {
		return errors.New("vault: nil note")
	}
	if n.loaded {
		return nil
	}
	data, err := os.ReadFile(n.Path)
	if err != nil {
		return fmt.Errorf("note: read %q: %w", n.Path, err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return fmt.Errorf("note: parse %q: %w", n.Path, err)
	}
	n.doc = doc
	n.loaded = true
	return nil
}

// Loaded reports whether the note has been read from disk.
func (n *Note) Loaded() bool { return n != nil && n.loaded }

// Save writes the note atomically: it serializes the document, writes to a
// sibling temp file, fsyncs, and renames over the destination. The caller
// passes the frontmatter config so wikilink-typed properties are wrapped
// consistently; nil is permitted to skip wrapping.
func (n *Note) Save(fmCfg *config.FrontmatterConfig) error {
	if n == nil {
		return errors.New("vault: nil note")
	}
	if !n.loaded {
		return fmt.Errorf("note: save %q: not loaded", n.Path)
	}
	data, err := frontmatter.Marshal(n.doc, fmCfg)
	if err != nil {
		return fmt.Errorf("note: marshal %q: %w", n.Path, err)
	}
	return atomicWrite(n.Path, data)
}

// Front returns a pointer to the note's frontmatter map. Load is called
// implicitly if the note has not yet been read. The returned pointer aliases
// the underlying storage so callers can mutate in place.
func (n *Note) Front() (*frontmatter.Map, error) {
	if err := n.Load(); err != nil {
		return nil, err
	}
	return &n.doc.Front, nil
}

// Body returns the note body (everything after the closing `---` line). Load
// is called implicitly.
func (n *Note) Body() (string, error) {
	if err := n.Load(); err != nil {
		return "", err
	}
	return n.doc.Body, nil
}

// SetBody replaces the note body. Load is called implicitly so the existing
// frontmatter is preserved.
func (n *Note) SetBody(body string) error {
	if err := n.Load(); err != nil {
		return err
	}
	n.doc.Body = body
	return nil
}

// Title returns the filename stem (no extension).
func (n *Note) Title() string {
	base := filepath.Base(n.Path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Folder returns the note's containing folder relative to the given vault
// root, with forward slashes. A note directly under the root returns "".
func (n *Note) Folder(root string) (string, error) {
	rel, err := filepath.Rel(root, n.Path)
	if err != nil {
		return "", fmt.Errorf("note: rel %q: %w", n.Path, err)
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		return "", nil
	}
	return dir, nil
}

// atomicWrite writes data to dest atomically by writing a sibling temp file,
// fsyncing it, and renaming over dest. The temp file is removed on any error
// so it does not linger.
func atomicWrite(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("note: mkdir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".note-*.tmp")
	if err != nil {
		return fmt.Errorf("note: tempfile in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("note: write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("note: fsync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("note: close %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("note: rename %q -> %q: %w", tmpPath, dest, err)
	}
	return nil
}
