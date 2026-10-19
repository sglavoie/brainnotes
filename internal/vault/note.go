package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// Note is a handle to a markdown file in the vault. Construction is cheap:
// the file is not read until Load or an accessor needs it.
type Note struct {
	// Path is the absolute path on disk.
	Path string

	root string

	doc    frontmatter.Document
	loaded bool
	// sourceHash is the SHA-256 of the bytes Load read. It lets a caller pin a
	// transaction to the exact revision it parsed, so a concurrent external edit
	// between Load and commit is rejected instead of silently overwritten.
	sourceHash string
	// sourcePath is the path sourceHash was read from. Save compares it to Path
	// because a caller may retarget Path after loading, and the loaded revision
	// says nothing about a different destination.
	sourcePath string
}

// NewNote returns a handle to the absolute path within the vault rooted at root.
// Save needs the root to name a lock file and to check containment, so a caller
// that intends to write must build notes here (or through Vault) rather than
// with a bare Note literal.
func NewNote(root, path string) *Note {
	return &Note{Path: path, root: root}
}

// NewNoteFromSource returns a note that is already loaded from data, without
// touching the disk. It is for a caller that already holds the note's bytes — a
// cache keyed by revision, say — and would otherwise have to read the file again
// just to get a parsed handle.
//
// The resulting note is indistinguishable from one NewNote+Load produced for the
// same bytes: it carries the same document, the same sourceHash, and the same
// sourcePath, so Save's compare-and-swap precondition still refers to the exact
// revision the caller parsed. Handing over bytes that are not what is on disk at
// path therefore makes the next Save a conflict, which is the honest outcome
// rather than a lost update.
//
// It cannot delegate to Load: Load early-returns on an already-loaded note and
// would otherwise read the file, which is the I/O this constructor exists to
// avoid. The four field assignments are duplicated deliberately and pinned by a
// test that compares both constructions field for field.
func NewNoteFromSource(root, path string, data []byte) (*Note, error) {
	if path == "" {
		return nil, errors.New("note: source: empty path")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("note: source %q: path must be absolute", path)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("note: parse %q: %w", path, err)
	}
	n := NewNote(root, path)
	n.doc = doc
	n.sourceHash = HashBytes(data)
	n.sourcePath = path
	n.loaded = true
	return n, nil
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
	if !filepath.IsAbs(n.Path) {
		return fmt.Errorf("note: load %q: path must be absolute", n.Path)
	}
	data, err := os.ReadFile(n.Path)
	if err != nil {
		return fmt.Errorf("note: read %q: %w", n.Path, err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return &ParseError{Path: n.Path, Err: err}
	}
	n.doc = doc
	n.sourceHash = HashBytes(data)
	n.sourcePath = n.Path
	n.loaded = true
	return nil
}

// ParseError reports a note whose file was read but whose frontmatter could
// not be parsed. It is a distinct type so a read-only caller can tell a single
// malformed note (skippable, see Vault.NotesLenient) from an I/O failure that
// says the walk itself cannot be trusted.
type ParseError struct {
	// Path is the absolute path of the note.
	Path string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("note: parse %q: %v", e.Path, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// Loaded reports whether the note has been read from disk.
func (n *Note) Loaded() bool { return n != nil && n.loaded }

// SourceHash returns the hex SHA-256 of the bytes Load read, or "" when the
// note has not been loaded.
func (n *Note) SourceHash() string {
	if n == nil {
		return ""
	}
	return n.sourceHash
}

// Marshal serializes the note the same way Save would, without writing it.
func (n *Note) Marshal(fmCfg *config.FrontmatterConfig) ([]byte, error) {
	if n == nil {
		return nil, errors.New("vault: nil note")
	}
	if !n.loaded {
		return nil, fmt.Errorf("note: marshal %q: not loaded", n.Path)
	}
	data, err := frontmatter.Marshal(n.doc, fmCfg)
	if err != nil {
		return nil, fmt.Errorf("note: marshal %q: %w", n.Path, err)
	}
	return data, nil
}

// Save writes the note atomically: it serializes the document, writes to a
// sibling temp file, fsyncs, and installs it over the destination through the
// same path-preserving replacement layer FileTxn uses. The caller passes the
// frontmatter config so wikilink-typed properties are wrapped consistently; nil
// is permitted to skip wrapping.
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
	if !filepath.IsAbs(n.Path) {
		return fmt.Errorf("note: save %q: path must be absolute", n.Path)
	}
	// A rootless note can neither name a lock file nor check containment, so saving
	// one would skip both without saying so. Refuse rather than degrade.
	if n.root == "" {
		return fmt.Errorf("note: save %q: note has no vault root (build it with vault.NewNote)", n.Path)
	}
	if err := n.ensureSavePathContained(); err != nil {
		return err
	}
	// Named explicitly rather than left to LockPaths below, because a save is one
	// of the surfaces the policy exists for and reading it here is how the
	// invariant stays visible to anyone changing this function.
	if err := GuardMutation(n.root, n.Path); err != nil {
		return err
	}
	data, err := frontmatter.Marshal(n.doc, fmCfg)
	if err != nil {
		return fmt.Errorf("note: marshal %q: %w", n.Path, err)
	}
	// The lock keeps two bn writers off one note for the whole save, so the
	// precondition check below is not racing another transaction's plan. It is not
	// what makes the write safe against an editor, which takes no lock: the
	// replacement itself is a compare-and-swap that judges the bytes it displaces,
	// and the expectation it judges them against is the same revision this check
	// used.
	release, err := LockPaths(n.root, n.Path)
	if err != nil {
		return err
	}
	defer release()
	preimage, err := n.verifySourceUnchanged()
	if err != nil {
		return err
	}
	if err := atomicWrite(n.Path, data, preimage, 0); err != nil {
		return err
	}
	n.sourceHash = HashBytes(data)
	n.sourcePath = n.Path
	return nil
}

// verifySourceUnchanged rejects a write planned against a revision that is no
// longer on disk, so a concurrent edit between Load and Save is a conflict rather
// than a lost update. The check only means something for the file Load read: a
// caller that retargets Path is writing somewhere the loaded revision makes no
// claim about, so that write keeps the older unconditional behavior.
//
// It returns the expectation the replacement itself must hold the destination
// to, which is the same revision this check used. The check is a plain read, so
// on its own it would lose a save that lands between it and the replacement; the
// replacement re-judges the identical expectation against the bytes it actually
// displaces, which is what closes that window.
func (n *Note) verifySourceUnchanged() (FileExpectation, error) {
	if n.Path != n.sourcePath {
		return ExpectAnyContent(), nil
	}
	expect := ExpectHash(n.sourceHash)
	err := verifyCurrentState(n.Path, expect)
	if err == nil {
		return expect, nil
	}
	// A destination this process cannot read cannot be compared against, and
	// saving a mode-0000 note is supported, so treat it as unverifiable instead of
	// refusing the write. The replacement cannot read what it displaces either,
	// for exactly the same reason, so it is not asked to.
	if errors.Is(err, fs.ErrPermission) {
		return ExpectAnyContent(), nil
	}
	return FileExpectation{}, err
}

func (n *Note) ensureSavePathContained() error {
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
	return textnorm.NFC(strings.TrimSuffix(base, filepath.Ext(base)))
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
	if !filepath.IsAbs(n.Path) {
		return "", fmt.Errorf("note: folder %q: path must be absolute", n.Path)
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
