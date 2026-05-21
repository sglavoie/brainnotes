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

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// Vault is a handle to a notes directory plus the configuration that controls
// walking and frontmatter behavior.
type Vault struct {
	// Root is the absolute path to the vault root directory.
	Root string
	// Cfg is the configuration used for walking and note operations. Never nil
	// once Open has returned successfully.
	Cfg *config.Config
}

// Open validates that cfg.Vault is an existing directory and returns a Vault
// pinned to its absolute path.
func Open(cfg *config.Config) (*Vault, error) {
	if cfg == nil {
		return nil, errors.New("vault: nil config")
	}
	if cfg.Vault == "" {
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
	return &Vault{Root: abs, Cfg: cfg}, nil
}

// WalkFilter is reserved for future per-walk filtering. The zero value walks
// every markdown note that survives the ignore list.
type WalkFilter struct{}

// Walk visits every `*.md` file under the vault root that is not pruned by the
// configured ignore list. The callback receives an unloaded Note; load lazily
// inside the callback if frontmatter or body is needed.
//
// Hidden directories (names beginning with `.`) below the root are pruned
// unconditionally. Directories named in cfg.Ignore (vault-relative, slash-
// separated) are pruned at any depth where their relative path matches.
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
	ignore := ignoreSet(v.Cfg)
	templatesDir := ""
	if v.Cfg != nil {
		templatesDir = strings.Trim(filepath.ToSlash(v.Cfg.Templates.Dir), "/")
		if templatesDir != "" {
			templatesDir = path.Clean(templatesDir)
			if templatesDir == "." {
				templatesDir = ""
			}
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
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		return fn(&Note{Path: p})
	})
	if walkErr != nil {
		return fmt.Errorf("vault: walk: %w", walkErr)
	}
	return nil
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

// ResolveByTitle finds a note by case-insensitive filename stem. When folder
// is non-empty the search is scoped to that vault-relative folder (matched
// against the note's containing folder); otherwise the entire vault is
// searched. An ambiguous match returns an error whose message lists every
// candidate path so the CLI can echo them.
func (v *Vault) ResolveByTitle(title, folder string) (*Note, error) {
	if title == "" {
		return nil, errors.New("vault: empty title")
	}
	wantStem := strings.ToLower(title)
	// Normalize to slash-relative form: convert OS separators, collapse `.`/
	// `./`/double slashes via path.Clean, then trim leading and trailing slashes
	// so common variants ("./People", "People/") all compare equal to the
	// note's noteFolder (which is also slash-relative, no leading/trailing
	// separators).
	wantFolder := strings.ReplaceAll(filepath.ToSlash(folder), "\\", "/")
	wantFolder = strings.Trim(wantFolder, "/")
	if wantFolder != "" {
		wantFolder = path.Clean(wantFolder)
		if wantFolder == "." {
			wantFolder = ""
		}
	}

	var matches []*Note
	err := v.Walk(WalkFilter{}, func(n *Note) error {
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(n.Path), filepath.Ext(n.Path)))
		if stem != wantStem {
			return nil
		}
		if wantFolder != "" {
			rel, relErr := filepath.Rel(v.Root, n.Path)
			if relErr != nil {
				return relErr
			}
			noteFolder := filepath.ToSlash(filepath.Dir(rel))
			if noteFolder == "." {
				noteFolder = ""
			}
			if !strings.EqualFold(noteFolder, wantFolder) {
				return nil
			}
		}
		matches = append(matches, n)
		return nil
	})
	if err != nil {
		return nil, err
	}

	switch len(matches) {
	case 0:
		if wantFolder != "" {
			return nil, fmt.Errorf("vault: no note titled %q in %q", title, wantFolder)
		}
		return nil, fmt.Errorf("vault: no note titled %q", title)
	case 1:
		return matches[0], nil
	default:
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
		return nil, fmt.Errorf("vault: %q is ambiguous; candidates: %s", title, strings.Join(paths, ", "))
	}
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
		p = strings.Trim(filepath.ToSlash(p), "/")
		if p == "" {
			return
		}
		// Normalize `./Foo`, `Foo/.`, etc. so an ignore entry written with a
		// leading or interior `./` segment still matches the slash-separated
		// relative path that Walk compares against. Without this, an entry
		// like `./Trash` would never match `Trash/...` paths and the directory
		// would be walked unexpectedly.
		p = path.Clean(p)
		if p == "" || p == "." {
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
