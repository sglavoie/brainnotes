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

// Title returns the filename stem (no extension). Returns "" for a nil Note.
func (n *Note) Title() string {
	if n == nil {
		return ""
	}
	base := filepath.Base(n.Path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Folder returns the note's containing folder relative to the given vault
// root, with forward slashes. A note directly under the root returns "".
func (n *Note) Folder(root string) (string, error) {
	if n == nil {
		return "", errors.New("vault: nil note")
	}
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
//
// If dest already exists, its file mode is preserved across the rewrite. Go's
// os.CreateTemp creates files with mode 0600, so without this step every Save
// would silently strip group/world read bits from a 0644 note. When dest does
// not exist (new note) the default 0600 from CreateTemp is widened to 0644 to
// match the usual umask-derived behavior of editors like Obsidian.
func atomicWrite(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("note: mkdir %q: %w", dir, err)
	}
	// Capture the existing mode, if any, before writing the temp file. Stat
	// errors other than "not exist" propagate so we don't silently clobber a
	// file we couldn't introspect.
	var destMode os.FileMode
	if info, err := os.Stat(dest); err == nil {
		destMode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("note: stat %q: %w", dest, err)
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
	// Apply the destination's prior mode (or a sensible default for a new file)
	// before the rename so the final inode lands with the right perms. Doing
	// this on the temp path keeps the operation atomic with respect to readers.
	wantMode := destMode
	if wantMode == 0 {
		wantMode = 0o644
	}
	if err := os.Chmod(tmpPath, wantMode); err != nil {
		cleanup()
		return fmt.Errorf("note: chmod %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("note: rename %q -> %q: %w", tmpPath, dest, err)
	}
	// fsync the parent directory so the rename is durable across power loss.
	// POSIX guarantees rename atomicity within a running system, but the
	// directory entry update isn't persisted until the directory itself is
	// synced. A failure to open or sync the directory is non-fatal — the file
	// content is still written and visible to the running process — so callers
	// only see this surface as a rare logged-once warning in practice.
	if dirF, err := os.Open(dir); err == nil {
		_ = dirF.Sync()
		_ = dirF.Close()
	}
	return nil
}
