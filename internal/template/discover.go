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

// Find resolves a template by name within <cfg.Vault>/<cfg.Templates.Dir>.
//
// Lookup is case-insensitive on the filename and tries, in order:
//  1. The literal name (e.g. "book").
//  2. The name with the configured default extension (e.g. "book.md").
//  3. The name suffixed with " Template" and the default extension
//     (e.g. "Book Template.md") — the existing vault convention.
//
// The directory listing is read once and compared against each candidate
// lower-cased; the first match wins.
func Find(cfg *config.Config, name string) (string, error) {
	if cfg == nil {
		return "", errors.New("template: nil config")
	}
	if name == "" {
		return "", errors.New("template: empty name")
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

	candidates := []string{
		strings.ToLower(name),
		strings.ToLower(name + ext),
		strings.ToLower(name + " Template" + ext),
	}

	for _, want := range candidates {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.ToLower(e.Name()) == want {
				return filepath.Join(dir, e.Name()), nil
			}
		}
	}
	return "", fmt.Errorf("%w: %q in %s", ErrTemplateNotFound, name, dir)
}
