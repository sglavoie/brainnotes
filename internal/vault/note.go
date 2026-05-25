package vault

import (
	"crypto/rand"
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

	root string

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
	if n.Path == "" {
		return errors.New("note: load: empty path")
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
	if n.Path == "" {
		return errors.New("note: save: empty path")
	}
	if err := n.ensureSavePathContained(); err != nil {
		return err
	}
	data, err := frontmatter.Marshal(n.doc, fmCfg)
	if err != nil {
		return fmt.Errorf("note: marshal %q: %w", n.Path, err)
	}
	return atomicWrite(n.Path, data)
}

func (n *Note) ensureSavePathContained() error {
	if n.root == "" {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(n.root)
	if err != nil {
		return fmt.Errorf("note: resolve root %q: %w", n.root, err)
	}
	realDir, err := resolveDirForWrite(filepath.Dir(n.Path))
	if err != nil {
		return fmt.Errorf("note: resolve save dir %q: %w", filepath.Dir(n.Path), err)
	}
	realDest := filepath.Join(realDir, filepath.Base(n.Path))
	rel, err := filepath.Rel(realRoot, realDest)
	if err != nil {
		return fmt.Errorf("note: rel %q: %w", realDest, err)
	}
	if relEscapesRoot(rel) || filepath.IsAbs(rel) {
		return fmt.Errorf("note: save path %q resolves outside vault root %q", n.Path, n.root)
	}
	return nil
}

// resolveDirForWrite resolves dir to the real location a create/rename would
// use: it follows symlinks on the deepest existing ancestor, then reapplies any
// still-missing suffix underneath that resolved ancestor.
func resolveDirForWrite(dir string) (string, error) {
	dir = filepath.Clean(dir)
	var suffix []string
	for {
		_, err := os.Lstat(dir)
		switch {
		case err == nil:
			targetInfo, statErr := os.Stat(dir)
			if statErr != nil {
				return "", statErr
			}
			if !targetInfo.IsDir() {
				return "", fmt.Errorf("%q is not a directory", dir)
			}
			resolved, evalErr := filepath.EvalSymlinks(dir)
			if evalErr != nil {
				return "", evalErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		case os.IsNotExist(err):
			parent := filepath.Dir(dir)
			if parent == dir {
				return "", fmt.Errorf("no existing ancestor for %q", dir)
			}
			suffix = append(suffix, filepath.Base(dir))
			dir = parent
		default:
			return "", err
		}
	}
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

// Title returns the filename stem (no extension). Returns "" for a nil Note
// or a Note with an empty path.
func (n *Note) Title() string {
	if n == nil || n.Path == "" {
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
	if root == "" {
		return "", errors.New("note: empty root")
	}
	if n.Path == "" {
		return "", errors.New("note: folder: empty path")
	}
	rel, err := filepath.Rel(root, n.Path)
	if err != nil {
		return "", fmt.Errorf("note: rel %q: %w", n.Path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("note: path %q is outside root %q", n.Path, root)
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
// not exist (new note), the mode is derived from a sibling probe file created
// with 0666 so the final note respects the process umask instead of forcing
// world-readable 0644 permissions.
func atomicWrite(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("note: mkdir %q: %w", dir, err)
	}
	// Capture the existing mode, if any, before writing the temp file. Stat
	// errors other than "not exist" propagate so we don't silently clobber a
	// file we couldn't introspect. destExists distinguishes "file has 0o000
	// permissions" from "file does not exist" so the mode check below does
	// not silently widen a deliberately restrictive mode.
	var destMode os.FileMode
	var destExists bool
	if info, err := os.Stat(dest); err == nil {
		destMode = info.Mode().Perm()
		destExists = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("note: stat %q: %w", dest, err)
	}

	tmp, tmpPath, err := openExclusiveTemp(dir, ".note-", ".tmp", 0o600)
	if err != nil {
		return fmt.Errorf("note: tempfile in %q: %w", dir, err)
	}
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
	if !destExists {
		wantMode, err = defaultNewFileMode(dir)
		if err != nil {
			cleanup()
			return err
		}
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

func openExclusiveTemp(dir, prefix, suffix string, perm os.FileMode) (*os.File, string, error) {
	const attempts = 1000
	for i := 0; i < attempts; i++ {
		var randBytes [8]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return nil, "", fmt.Errorf("note: random temp suffix: %w", err)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s%x%s", prefix, randBytes, suffix))
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return f, path, nil
		}
		if os.IsExist(err) {
			continue
		}
		return nil, "", err
	}
	return nil, "", fmt.Errorf("note: unique temp file in %q: too many collisions", dir)
}

func defaultNewFileMode(dir string) (os.FileMode, error) {
	probe, probePath, err := openExclusiveTemp(dir, ".note-mode-", ".tmp", 0o666)
	if err != nil {
		return 0, fmt.Errorf("note: probe tempfile in %q: %w", dir, err)
	}
	cleanup := func() { _ = os.Remove(probePath) }
	if err := probe.Close(); err != nil {
		cleanup()
		return 0, fmt.Errorf("note: close %q: %w", probePath, err)
	}
	info, err := os.Stat(probePath)
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("note: stat %q: %w", probePath, err)
	}
	cleanup()
	return info.Mode().Perm(), nil
}
