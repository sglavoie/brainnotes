package template

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// ErrTemplateNotFound is returned by Find when no matching template file
// exists under the configured templates directory.
var ErrTemplateNotFound = errors.New("template: not found")

// ErrTemplateAmbiguous is returned by Find when more than one template path
// matches the same case-insensitive candidate (e.g. "book.md" and "BOOK.md",
// or "Templates" and "templates", coexisting on a case-sensitive filesystem).
// Callers should surface the error so the user can disambiguate by renaming
// one of the colliding paths.
var ErrTemplateAmbiguous = errors.New("template: ambiguous match")

// Find resolves a template by name within <cfg.Vault>/<cfg.Templates.Dir>.
//
// Lookup is case-insensitive on the filename and tries, in order:
//  1. The literal name (e.g. "book").
//  2. The name with the configured default extension (e.g. "book.md").
//  3. The name suffixed with " Template" and the default extension
//     (e.g. "Book Template.md") — the existing vault convention.
//
// The templates directory is resolved case-insensitively when the exact
// configured path does not exist. Its listing is compared against each
// candidate with Unicode case-folding; the first candidate that matches wins.
// If more than one file matches the same candidate (only possible on
// case-sensitive filesystems), ErrTemplateAmbiguous is returned listing the
// colliding files.
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
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("template: name %q must not contain NUL bytes", name)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("template: name %q is not a valid template name", name)
	}
	dir, entries, err := readTemplatesDir(cfg)
	if err != nil {
		return "", err
	}
	ext := normalizeDefaultExtension(cfg.Templates.DefaultExtension)

	// stem is the user-supplied name with any trailing copy of the configured
	// extension removed, so that callers passing "Book.md" still hit the
	// "Book Template.md" candidate rather than searching for the silly
	// "Book.md Template.md".
	stem := name
	if ext != "" && strings.EqualFold(filepath.Ext(stem), ext) {
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	}
	if strings.TrimSpace(stem) == "" {
		return "", fmt.Errorf("template: name %q reduces to empty stem after extension strip", name)
	}

	// Preserve the caller's casing in each candidate: lower-casing here breaks
	// exact lookups for some Unicode names (for example Turkish dotted İ) because
	// strings.ToLower and strings.EqualFold are not interchangeable. Deduplicate
	// with EqualFold instead so "Book.md" and "book.md" still collapse.
	candidates := uniqueFold([]string{
		name,
		stem + ext,
		stem + " Template" + ext,
	})

	for _, want := range candidates {
		var matches []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.EqualFold(e.Name(), want) {
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

func readTemplatesDir(cfg *config.Config) (string, []os.DirEntry, error) {
	rel, err := normalizeTemplatesDir(cfg.Templates.Dir)
	if err != nil {
		return "", nil, err
	}

	dir := filepath.Join(cfg.Vault, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err == nil {
		if err := checkDirCaseAmbiguity(cfg.Vault, rel); err != nil {
			return "", nil, err
		}
		return dir, entries, nil
	}
	if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("template: read %q: %w", dir, err)
	}

	resolved, resolveErr := resolveDirCaseInsensitive(cfg.Vault, rel)
	if resolveErr != nil {
		return "", nil, resolveErr
	}
	if resolved == dir {
		return "", nil, fmt.Errorf("template: read %q: %w", dir, err)
	}

	entries, err = os.ReadDir(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("template: read %q: %w", resolved, err)
	}
	return resolved, entries, nil
}

func checkDirCaseAmbiguity(root, rel string) error {
	clean := path.Clean(strings.ReplaceAll(filepath.ToSlash(rel), `\`, `/`))
	if clean == "." {
		return nil
	}

	cur := root
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == "" || part == "." {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return nil
		}
		var matches []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if strings.EqualFold(e.Name(), part) {
				matches = append(matches, e.Name())
			}
		}
		switch len(matches) {
		case 0:
			return nil
		case 1:
			cur = filepath.Join(cur, matches[0])
		default:
			return fmt.Errorf("%w: templates dir component %q matches %v in %s", ErrTemplateAmbiguous, part, matches, cur)
		}
	}
	return nil
}

func normalizeDefaultExtension(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" || ext == "." {
		return ".md"
	}
	if strings.ContainsAny(ext, "/\\\x00") {
		return ".md"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

func normalizeTemplatesDir(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("template: templates dir %q must not contain NUL bytes", rel)
	}
	normalized := strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(rel)), `\`, `/`)
	if isPathAbsoluteLike(normalized) {
		return "", fmt.Errorf("template: templates dir %q must be relative", rel)
	}
	normalized = strings.TrimRight(normalized, "/")
	if normalized != "" {
		normalized = path.Clean(normalized)
	}
	if normalized == "" || normalized == "." {
		return "Templates", nil
	}
	if normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("template: templates dir %q must not escape the vault", rel)
	}
	return normalized, nil
}

func isPathAbsoluteLike(p string) bool {
	p = strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(p)), `\`, `/`)
	if filepath.IsAbs(p) || path.IsAbs(p) {
		return true
	}
	if len(p) >= 2 && isASCIIAlpha(p[0]) && p[1] == ':' {
		return len(p) == 2 || p[2] == '/'
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

func uniqueFold(candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		duplicate := false
		for _, existing := range out {
			if strings.EqualFold(existing, candidate) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, candidate)
		}
	}
	return out
}

func resolveDirCaseInsensitive(root, rel string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(filepath.ToSlash(rel), `\`, `/`))
	if clean == "." {
		return root, nil
	}

	cur := root
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == "" || part == "." {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", fmt.Errorf("template: read %q: %w", cur, err)
		}
		var matches []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if strings.EqualFold(e.Name(), part) {
				matches = append(matches, e.Name())
			}
		}
		switch len(matches) {
		case 0:
			return filepath.Join(cur, part), nil
		case 1:
			cur = filepath.Join(cur, matches[0])
		default:
			return "", fmt.Errorf("%w: templates dir component %q matches %v in %s", ErrTemplateAmbiguous, part, matches, cur)
		}
	}
	return cur, nil
}
