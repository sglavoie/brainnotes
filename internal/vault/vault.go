// Package vault models the on-disk vault: a root directory containing markdown
// notes plus configuration. It provides walking, lookup, and per-note
// load/save primitives used by the CLI commands.
package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// Vault is a handle to a notes directory plus the configuration that controls
// walking and frontmatter behavior.
//
// The configuration is held as an immutable snapshot behind a lock rather than
// as a shared mutable pointer: a live `bn serve` reloads brainnotes.toml while
// requests are in flight, and handing every reader the same mutable struct
// would be a data race the moment a reload lands.
type Vault struct {
	// Root is the absolute path to the vault root directory.
	Root string

	mu  sync.RWMutex
	cfg *config.Config
}

// Open validates that cfg.Vault is an existing directory and returns a Vault
// pinned to its absolute path.
func Open(cfg *config.Config) (*Vault, error) {
	return openVault(cfg, false)
}

// OpenReadOnly opens a replica without transaction recovery or storage writes.
func OpenReadOnly(cfg *config.Config) (*Vault, error) {
	return openVault(cfg, true)
}

func openVault(cfg *config.Config, readOnly bool) (*Vault, error) {
	if cfg == nil {
		return nil, errors.New("vault: nil config")
	}
	// Reject whitespace-only vault paths, but preserve surrounding spaces on
	// real paths. Trimming before filepath.Abs would make a valid directory
	// like " Notes " unreachable by silently retargeting it to "Notes".
	if strings.TrimSpace(cfg.Vault) == "" {
		return nil, errors.New("vault: config has empty vault path")
	}
	abs, err := filepath.Abs(cfg.Vault)
	if err != nil {
		return nil, fmt.Errorf("vault: resolve %q: %w", cfg.Vault, err)
	}
	// If the vault path itself is a symlink to a directory, resolve it.
	// filepath.WalkDir uses os.Lstat on the root entry: if the root is a
	// symlink, Lstat sees a symlink (not a directory) and WalkDir does not
	// descend, silently returning zero notes. We resolve only the final
	// component rather than calling filepath.EvalSymlinks on the whole path,
	// because EvalSymlinks also canonicalizes intermediate components (e.g.
	// /var -> /private/var on macOS), which would change the path prefix and
	// break filepath.Rel comparisons with paths built via filepath.Abs.
	abs, err = resolveRootSymlink(abs)
	if err != nil {
		return nil, fmt.Errorf("vault: resolve root symlink %q: %w", cfg.Vault, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("vault: stat %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault: %q is not a directory", abs)
	}
	// Finish any transaction a previous process left prepared but unapplied, so a
	// vault is whole by the time anything reads it. The result is deliberately
	// ignored: opening a vault is what `bn find` and every other read-only command
	// do, and a recovery that cannot run is not a reason to refuse them. The write
	// path fails closed on the same condition — acquirePathLocks runs the same
	// pass and returns its error — so nothing mutates a vault that is still
	// undecided.
	if !readOnly {
		_ = recoverTxnIntents(abs)
	}
	return &Vault{Root: abs, cfg: cfg.Clone()}, nil
}

// Config returns the current configuration snapshot. The returned value is
// shared and must be treated as read-only; use its Clone method to mutate.
func (v *Vault) Config() *config.Config {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.cfg
}

// Frontmatter returns the frontmatter section of the current snapshot, or an
// empty section when the vault has no configuration.
func (v *Vault) Frontmatter() *config.FrontmatterConfig {
	cfg := v.Config()
	if cfg == nil {
		return &config.FrontmatterConfig{}
	}
	return &cfg.Frontmatter
}

// SetConfig publishes a new configuration snapshot. It stores a deep clone so
// later mutations by the caller cannot reach readers of the published value.
func (v *Vault) SetConfig(cfg *config.Config) {
	if v == nil {
		return
	}
	clone := cfg.Clone()
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cfg = clone
}

// WalkFilter is reserved for future per-walk filtering. The zero value walks
// every markdown note that survives the ignore list.
type WalkFilter struct{}

// Walk visits every `*.md` file under the vault root that is not pruned by the
// configured ignore list. The callback receives an unloaded Note; load lazily
// inside the callback if frontmatter or body is needed.
//
// Hidden directories (names beginning with `.`) below the root are pruned
// unconditionally. Paths named in cfg.Ignore (vault-relative, slash-separated)
// are skipped; ignored directories are pruned before descent.
//
// Symbolic links are skipped, regardless of whether they target a file or a
// directory. Following symlinks would let a note escape the vault root, which
// breaks the in-vault containment guarantee the rest of the CLI relies on; if
// users want to include external files they should add them as regular files.
//
// Returning a non-nil error from fn aborts the walk and propagates the error.
// filepath.SkipDir from fn is honored so callers can skip subtrees.
func (v *Vault) Walk(_ WalkFilter, fn func(*Note) error) error {
	if v == nil {
		return errors.New("vault: nil receiver")
	}
	if fn == nil {
		return errors.New("vault: nil walk callback")
	}
	cfg := v.Config()
	ignore := ignoreSet(cfg)
	templatesDir := ""
	if cfg != nil {
		templatesDir = cleanSlashRel(cfg.Templates.Dir)
		if relEscapesRoot(templatesDir) {
			templatesDir = ""
		}
	}

	walkErr := filepath.WalkDir(v.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == v.Root {
			return nil
		}
		rel, relErr := filepath.Rel(v.Root, p)
		if relErr != nil {
			return fmt.Errorf("vault: rel %q: %w", p, relErr)
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, skip := ignore[relSlash]; skip {
				return filepath.SkipDir
			}
			// Templates dir is matched case-insensitively because the
			// invariant "template files must never be walked" is load-bearing:
			// on case-insensitive filesystems (macOS APFS, Windows NTFS) a
			// directory created as "Templates" but configured as "templates"
			// would otherwise leak unrendered placeholder files into the walk.
			if templatesDir != "" && strings.EqualFold(relSlash, templatesDir) {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks so the walk cannot leak entries pointing outside the
		// vault root. WalkDir reports symlinks with !d.IsDir() even when they
		// target a directory, so this check catches both file and directory
		// symlinks without recursing through them.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// Skip other non-regular files (devices, sockets, named pipes) — they
		// can't be valid notes and reading them may block or fail.
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if _, skip := ignore[relSlash]; skip {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		return fn(v.note(p))
	})
	if walkErr != nil {
		return fmt.Errorf("vault: walk: %w", walkErr)
	}
	return nil
}

func (v *Vault) note(path string) *Note {
	return NewNote(v.Root, path)
}

// Notes loads every note in the vault into memory and returns them. Convenient
// for commands like `find` that need to filter across the entire set.
//
// At the documented working size (~300 notes) memory use is negligible. The
// approach starts to feel slow above a few thousand notes; callers that scale
// past that should switch to Walk and stream.
func (v *Vault) Notes() ([]*Note, error) {
	var notes []*Note
	err := v.Walk(WalkFilter{}, func(n *Note) error {
		if err := n.Load(); err != nil {
			return err
		}
		notes = append(notes, n)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notes, nil
}

// ErrNoteNotFound is wrapped by ResolveByTitle when no note carries the title,
// so a caller can fall back to path resolution without matching message text.
var ErrNoteNotFound = errors.New("vault: no note titled")

// SkippedNote is a note NotesLenient left out because its frontmatter could
// not be parsed.
type SkippedNote struct {
	// Path is the absolute path of the note.
	Path string
	// Err is the underlying parse error, without the path prefix.
	Err error
}

// NotesLenient is Notes for read-only callers: a note whose frontmatter cannot
// be parsed is left out of notes and reported in skipped, in walk order,
// instead of aborting the whole load. Any other failure (an unreadable file, a
// walk error) still aborts, because it says the result cannot be trusted.
//
// Mutating commands keep using Notes. A rewrite planned over a vault with
// silently missing notes could leave links to those notes stale.
func (v *Vault) NotesLenient() (notes []*Note, skipped []SkippedNote, err error) {
	err = v.Walk(WalkFilter{}, func(n *Note) error {
		loadErr := n.Load()
		var parseErr *ParseError
		if errors.As(loadErr, &parseErr) {
			skipped = append(skipped, SkippedNote{Path: n.Path, Err: parseErr.Err})
			return nil
		}
		if loadErr != nil {
			return loadErr
		}
		notes = append(notes, n)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return notes, skipped, nil
}

// ResolveByTitle finds a note by case-insensitive filename stem. When folder
// is non-empty the search is scoped to that vault-relative folder (matched
// against the note's containing folder); otherwise the entire vault is
// searched. An ambiguous match returns an error whose message lists every
// candidate path so the CLI can echo them.
func (v *Vault) ResolveByTitle(title, folder string) (*Note, error) {
	if title == "" {
		return nil, errors.New("vault: empty title")
	}
	// Normalize to slash-relative form: convert OS separators, collapse `.`/
	// `./`/double slashes via path.Clean, then trim leading and trailing slashes
	// so common variants ("./People", "People/") all compare equal to the
	// note's noteFolder (which is also slash-relative, no leading/trailing
	// separators).
	wantFolder := cleanSlashRel(folder)
	if relEscapesRoot(wantFolder) {
		return nil, fmt.Errorf("vault: folder %q escapes vault", folder)
	}

	matches, err := v.titleMatches(title, wantFolder)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		if wantFolder != "" {
			return nil, fmt.Errorf("%w %q in %q", ErrNoteNotFound, title, wantFolder)
		}
		return nil, fmt.Errorf("%w %q", ErrNoteNotFound, title)
	case 1:
		return matches[0], nil
	}
	return nil, v.ambiguousTitleError(title, matches)
}

// titleMatches walks the vault for notes whose stem is title, limited to the
// folder wantFolder when it is non-empty.
func (v *Vault) titleMatches(title, wantFolder string) ([]*Note, error) {
	var matches []*Note
	err := v.Walk(WalkFilter{}, func(n *Note) error {
		stem := strings.TrimSuffix(filepath.Base(n.Path), filepath.Ext(n.Path))
		if !strings.EqualFold(textnorm.NFC(stem), textnorm.NFC(title)) {
			return nil
		}
		if wantFolder != "" {
			inFolder, err := v.noteInFolder(n, wantFolder)
			if err != nil || !inFolder {
				return err
			}
		}
		matches = append(matches, n)
		return nil
	})
	return matches, err
}

func (v *Vault) noteInFolder(n *Note, wantFolder string) (bool, error) {
	rel, err := filepath.Rel(v.Root, n.Path)
	if err != nil {
		return false, err
	}
	noteFolder := filepath.ToSlash(filepath.Dir(rel))
	if noteFolder == "." {
		noteFolder = ""
	}
	return strings.EqualFold(textnorm.NFC(noteFolder), textnorm.NFC(wantFolder)), nil
}

// ambiguousTitleError lists every candidate so the CLI can echo them.
func (v *Vault) ambiguousTitleError(title string, matches []*Note) error {
	paths := make([]string, len(matches))
	for i, n := range matches {
		rel, relErr := filepath.Rel(v.Root, n.Path)
		if relErr != nil {
			paths[i] = filepath.ToSlash(n.Path)
		} else {
			paths[i] = filepath.ToSlash(rel)
		}
	}
	// Sort so the error message is deterministic across filesystems with
	// non-stable directory iteration order. Without this, two callers can
	// see different orderings for the same ambiguity.
	sort.Strings(paths)
	return fmt.Errorf("vault: %q is ambiguous; candidates: %s", title, strings.Join(paths, ", "))
}

// resolveRootSymlink checks whether abs itself is a symlink; if so it reads
// the link target and resolves it relative to the parent directory, producing
// an absolute path to the real directory. Intermediate symlinks in the path
// are not resolved, keeping the prefix stable for filepath.Rel.
//
// Chained symlinks (e.g. link2 -> link1 -> realdir) are followed up to
// maxSymlinkHops times to reach the final non-symlink target. This mirrors
// the POSIX ELOOP limit and prevents infinite loops on cyclic symlinks.
func resolveRootSymlink(abs string) (string, error) {
	const maxSymlinkHops = 255
	for i := 0; i < maxSymlinkHops; i++ {
		fi, err := os.Lstat(abs)
		if err != nil {
			return abs, err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return abs, nil
		}
		target, err := os.Readlink(abs)
		if err != nil {
			return abs, err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(abs), target)
		}
		abs = filepath.Clean(target)
	}
	return "", fmt.Errorf("too many levels of symbolic links resolving %q", abs)
}

// ignoreSet returns the paths pruned by Walk, normalized to forward slashes.
// The configured templates dir is always included because template files
// contain unrendered placeholder syntax and are not valid notes. An empty
// cfg yields an empty set.
func ignoreSet(cfg *config.Config) map[string]struct{} {
	set := make(map[string]struct{})
	if cfg == nil {
		return set
	}
	add := func(p string) {
		p = cleanSlashRel(p)
		if p == "" || relEscapesRoot(p) {
			return
		}
		set[p] = struct{}{}
	}
	for _, p := range cfg.Ignore {
		add(p)
	}
	add(cfg.Templates.Dir)
	return set
}

// cleanSlashRel normalizes user/config supplied vault-relative paths to the
// slash-separated form Walk compares against. It intentionally treats a leading
// slash as vault-root-relative so common inputs like "/People" still work.
func cleanSlashRel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(filepath.ToSlash(p), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

func relEscapesRoot(p string) bool {
	if p == ".." {
		return true
	}
	return strings.HasPrefix(p, "../") || strings.HasPrefix(p, `..\`)
}
