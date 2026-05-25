package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// resolveRenameTarget treats arg as a path when it looks like one (contains a
// path separator, is absolute, or ends in .md), otherwise resolves it
// case-insensitively as a bare title. Relative path-like args are interpreted
// relative to the vault root rather than the process working directory.
func resolveRenameTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	looksLikePath := filepath.IsAbs(arg) ||
		strings.ContainsRune(arg, filepath.Separator) ||
		strings.HasSuffix(strings.ToLower(arg), ".md")
	if looksLikePath {
		abs, err := resolveVaultPathArg(v.Root, arg)
		if err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		return &vault.Note{Path: abs}, nil
	}
	n, err := v.ResolveByTitle(arg, "")
	if err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	return n, nil
}

func resolveVaultPathArg(vaultRoot, arg string) (string, error) {
	p := arg
	if !filepath.IsAbs(p) {
		p = filepath.Join(vaultRoot, filepath.FromSlash(p))
	}
	return absInVault(vaultRoot, p)
}

func ensureUniqueRenameSourceTitle(v *vault.Vault, targetPath, title string) error {
	duplicates, err := collectDuplicateRenameTitles(v, targetPath, title)
	if err != nil {
		return fmt.Errorf("rename: scan vault for duplicate titles: %w", err)
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return fmt.Errorf("rename: title %q is duplicated elsewhere in the vault (%s); refusing title-only rewrite", title, strings.Join(duplicates, ", "))
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
		if n.Path == targetPath || !strings.EqualFold(n.Title(), title) {
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
