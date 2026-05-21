package template

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// ErrTemplateNotFound is returned by Find when no matching template file
// exists under the configured templates directory.
var ErrTemplateNotFound = errors.New("template: not found")

// ErrTemplateAmbiguous is returned by Find when more than one file under the
// templates directory matches the same case-insensitive candidate (e.g.
// "book.md" and "BOOK.md" coexisting on a case-sensitive filesystem). Callers
// should surface the error so the user can disambiguate by renaming one of
// the colliding files.
var ErrTemplateAmbiguous = errors.New("template: ambiguous match")

// Find resolves a template by name within <cfg.Vault>/<cfg.Templates.Dir>.
//
// Lookup is case-insensitive on the filename and tries, in order:
//  1. The literal name (e.g. "book").
//  2. The name with the configured default extension (e.g. "book.md").
//  3. The name suffixed with " Template" and the default extension
//     (e.g. "Book Template.md") — the existing vault convention.
//
// The directory listing is read once and compared against each candidate
// lower-cased; the first candidate that matches wins. If more than one file
// matches the same candidate (only possible on case-sensitive filesystems),
// ErrTemplateAmbiguous is returned listing the colliding files.
//
// The name must be a bare filename: path separators in name are rejected so
// that callers don't accidentally escape the templates directory.
func Find(cfg *config.Config, name string) (string, error) {
	if cfg == nil {
		return "", errors.New("template: nil config")
	}
	if strings.TrimSpace(name) == "" {
		return "", errors.New("template: empty name")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("template: name %q must not contain path separators", name)
	}
	dir := filepath.Join(cfg.Vault, cfg.Templates.Dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("template: read %q: %w", dir, err)
	}
	ext := cfg.Templates.DefaultExtension
	if ext == "" {
		ext = ".md"
	}

	// stem is the user-supplied name with any trailing copy of the configured
	// extension removed, so that callers passing "Book.md" still hit the
	// "Book Template.md" candidate rather than searching for the silly
	// "Book.md Template.md".
	stem := name
	if ext != "" && strings.EqualFold(filepath.Ext(stem), ext) {
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	}

	// Use a small de-dup set so that name="Book.md" doesn't produce
	// "book.md" twice ("name" and "stem+ext" collapse to the same form).
	seen := make(map[string]struct{}, 3)
	var candidates []string
	for _, c := range []string{
		strings.ToLower(name),
		strings.ToLower(stem + ext),
		strings.ToLower(stem + " Template" + ext),
	} {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		candidates = append(candidates, c)
	}

	for _, want := range candidates {
		var matches []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.ToLower(e.Name()) == want {
				matches = append(matches, e.Name())
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return filepath.Join(dir, matches[0]), nil
		default:
			return "", fmt.Errorf("%w: %q matches %v in %s", ErrTemplateAmbiguous, name, matches, dir)
		}
	}
	return "", fmt.Errorf("%w: %q in %s (tried %v)", ErrTemplateNotFound, name, dir, candidates)
}
