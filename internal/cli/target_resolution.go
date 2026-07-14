package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// resolveNoteTarget turns one positional argument into a note. It first tries
// the vault's NFC-aware, case-insensitive title lookup, preserving ambiguity
// errors, then falls back to the shared path/glob resolver.
func resolveNoteTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	note, err := v.ResolveByTitle(arg, "")
	if err == nil {
		return note, nil
	}
	// "no note titled" is the only title-resolution failure that means the
	// caller may have supplied a path or glob. Ambiguity and walk errors retain
	// their original semantics.
	if !strings.Contains(err.Error(), "no note titled") {
		return nil, err
	}

	paths, pathErr := resolveNoteInputs(v.Root, []string{arg}, nil, false)
	if pathErr != nil {
		return nil, pathErr
	}
	if len(paths) != 1 {
		return nil, fmt.Errorf("expected exactly one path for %q, got %d", arg, len(paths))
	}
	if _, statErr := os.Stat(paths[0]); statErr != nil {
		return nil, statErr
	}
	return &vault.Note{Path: paths[0]}, nil
}
