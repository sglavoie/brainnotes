package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// resolveRenameTarget resolves the note `bn rename` acts on through the shared
// resolver, so a missing note is refused in the same words as every other
// note-targeting command (`rename: no note at Notes/Nope.md`) rather than with
// a stat of an absolute path the user never typed.
func resolveRenameTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	return resolveNoteTarget("rename", v, arg)
}

func ensureUniqueRenameDestinationTitle(v *vault.Vault, targetPath, newPath, title string, force bool) error {
	duplicates, err := collectDuplicateRenameTitles(v, targetPath, title)
	if err != nil {
		return fmt.Errorf("rename: scan vault for duplicate titles: %w", err)
	}
	if force && len(duplicates) > 0 {
		filtered := duplicates[:0]
		for _, rel := range duplicates {
			p := filepath.Join(v.Root, filepath.FromSlash(rel))
			if samePath(p, newPath) {
				continue
			}
			filtered = append(filtered, rel)
		}
		duplicates = filtered
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return fmt.Errorf("rename: new title %q already exists elsewhere in the vault (%s); refusing ambiguous rewrite", title, strings.Join(duplicates, ", "))
}

func collectDuplicateRenameTitles(v *vault.Vault, targetPath, title string) ([]string, error) {
	var duplicates []string
	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if n.Path == targetPath || !strings.EqualFold(textnorm.NFC(n.Title()), textnorm.NFC(title)) {
			return nil
		}
		rel, err := filepath.Rel(v.Root, n.Path)
		if err != nil {
			rel = n.Path
		}
		duplicates = append(duplicates, rel)
		return nil
	})
	return duplicates, err
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
