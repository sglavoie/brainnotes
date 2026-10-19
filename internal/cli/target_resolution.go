package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// resolveNoteTarget turns one positional argument into a note. It first tries
// the vault's NFC-aware, case-insensitive title lookup, preserving ambiguity
// errors, then falls back to the shared path/glob resolver.
func resolveNoteTarget(name string, v *vault.Vault, arg string) (*vault.Note, error) {
	note, err := v.ResolveByTitle(arg, "")
	if err == nil {
		return note, nil
	}
	// Not-found is the only title-resolution failure that means the caller may
	// have supplied a path or glob. Ambiguity and walk errors retain their
	// original semantics.
	if !errors.Is(err, vault.ErrNoteNotFound) {
		return nil, err
	}

	paths, pathErr := resolveNoteInputs(v.Root, []string{arg}, nil, false)
	if pathErr != nil {
		return nil, pathErr
	}
	if len(paths) != 1 {
		return nil, errors.New(prefixed(name, fmt.Sprintf("expected exactly one path for %q, got %d", arg, len(paths))))
	}
	if _, statErr := os.Stat(paths[0]); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			// A bare stat error would report an absolute path the user never
			// typed when what they typed was a title.
			return nil, &noteTargetNotFoundError{name: name, arg: arg}
		}
		return nil, statErr
	}
	return &vault.Note{Path: paths[0]}, nil
}

// noteTargetNotFoundError is a target that matched neither a title nor a file.
// It names what the user typed, in the form they typed it, under the command
// that was looking.
type noteTargetNotFoundError struct {
	name string
	arg  string
}

func (e *noteTargetNotFoundError) Error() string {
	if looksLikePath(e.arg) {
		return prefixed(e.name, fmt.Sprintf("no note at %s", e.arg))
	}
	return prefixed(e.name, fmt.Sprintf("no note titled %q, and no note at that path", e.arg))
}

func (e *noteTargetNotFoundError) Is(target error) bool {
	return target == vault.ErrNoteNotFound || target == fs.ErrNotExist
}

// looksLikePath reports whether arg reads as a path rather than a title: it
// names a folder, a Markdown file, or a glob.
func looksLikePath(arg string) bool {
	return strings.ContainsAny(arg, "/\\*?[") || strings.EqualFold(filepath.Ext(arg), ".md")
}
