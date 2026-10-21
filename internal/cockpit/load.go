package cockpit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// loadedNote pairs a note handle with its vault-relative slash path.
type loadedNote struct {
	note *vault.Note
	rel  string
}

// fileError records a note that could not be parsed, keyed by its relative
// path. These become frontmatter-error tasks rather than fatal load failures.
type fileError struct {
	rel string
	err string
}

// vaultData is a single load of the vault: parsed notes, the link graph built
// from them, and any parse errors encountered along the way. Mirrors the
// diagnostics loader in internal/web so the two views stay behavior-compatible.
type vaultData struct {
	loaded            []loadedNote
	notes             []*vault.Note
	graph             *query.Graph
	frontmatterErrors []fileError
	warnings          []Warning
}

// loadVaultData walks the vault once, loading every note. Notes with invalid
// frontmatter are collected as frontmatterErrors and excluded from the graph,
// exactly as the web diagnostics loader does.
func loadVaultData(v *vault.Vault) (vaultData, error) {
	if v == nil {
		return vaultData{}, fmt.Errorf("cockpit: nil vault")
	}

	var loaded []loadedNote
	var parseErrors []fileError

	err := v.Walk(vault.WalkFilter{}, func(note *vault.Note) error {
		rel := relPath(note.Path, v.Root)
		if loadErr := note.Load(); loadErr != nil {
			parseErrors = append(parseErrors, fileError{rel: rel, err: loadErr.Error()})
			return nil
		}
		loaded = append(loaded, loadedNote{note: note, rel: rel})
		return nil
	})
	if err != nil {
		return vaultData{}, fmt.Errorf("cockpit: walk notes: %w", err)
	}

	sort.Slice(parseErrors, func(i, j int) bool {
		return parseErrors[i].rel < parseErrors[j].rel
	})

	notes := make([]*vault.Note, 0, len(loaded))
	for _, item := range loaded {
		notes = append(notes, item.note)
	}

	return vaultData{
		loaded:            loaded,
		notes:             notes,
		graph:             query.BuildGraphForRoot(v.Root, notes),
		frontmatterErrors: parseErrors,
	}, nil
}

// relPath returns absPath relative to vaultRoot using forward slashes, falling
// back to the absolute path when the two are not related.
func relPath(absPath, vaultRoot string) string {
	if vaultRoot == "" {
		return absPath
	}
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}

// folderForRel returns the slash-separated parent folder of a relative path, or
// "" when the note lives at the vault root.
func folderForRel(rel string) string {
	folder := filepath.ToSlash(filepath.Dir(rel))
	if folder == "." {
		return ""
	}
	return folder
}

// fileHash returns the hex SHA-256 of a file's contents, or "" when it cannot
// be read. Used to anchor task signatures to concrete on-disk state.
func fileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
