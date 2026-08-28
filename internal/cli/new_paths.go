package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func sameNewRouteDestination(vaultRoot, probePath, a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	if infoA, err := os.Stat(a); err == nil {
		if infoB, err := os.Stat(b); err == nil && os.SameFile(infoA, infoB) {
			return true
		}
	}
	if !pathUsesCaseInsensitiveLookup(probePath) && !pathUsesCaseInsensitiveLookup(vaultRoot) {
		return false
	}
	relA, err := filepath.Rel(vaultRoot, a)
	if err != nil {
		return false
	}
	relB, err := filepath.Rel(vaultRoot, b)
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.ToSlash(relA), filepath.ToSlash(relB))
}

func pathUsesCaseInsensitiveLookup(probePath string) bool {
	for current := filepath.Clean(probePath); ; {
		info, err := os.Stat(current)
		if err == nil {
			alt := toggleCaseProbePath(current)
			if alt != current {
				altInfo, altErr := os.Stat(alt)
				switch {
				case altErr == nil:
					return os.SameFile(info, altInfo)
				case os.IsNotExist(altErr):
					return false
				}
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func toggleCaseProbePath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		switch c := path[i]; {
		case 'a' <= c && c <= 'z':
			return path[:i] + string(c-'a'+'A') + path[i+1:]
		case 'A' <= c && c <= 'Z':
			return path[:i] + string(c-'A'+'a') + path[i+1:]
		}
	}
	return path
}

func describeNewResolvedTemplate(vaultRoot, templatePath string) string {
	rel, err := filepath.Rel(vaultRoot, templatePath)
	if err != nil {
		return templatePath
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return templatePath
	}
	return filepath.ToSlash(rel)
}

func normalizeNewDefaultExtension(ext string) string {
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

func resolveNewDestFolder(vaultRoot, raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s %q is whitespace-only", label, raw)
	}
	for _, r := range raw {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s %q contains a control character (U+%04X)", label, raw, r)
		}
	}
	normalized := filepath.ToSlash(trimmed)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	if isNewDestAbsLike(normalized) {
		return "", fmt.Errorf("%s %q is an absolute path (must be vault-relative)", label, raw)
	}
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "", fmt.Errorf("%s %q is whitespace-only", label, raw)
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return vaultRoot, nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s %q is outside the vault", label, raw)
	}
	return filepath.Join(vaultRoot, filepath.FromSlash(cleaned)), nil
}

func isNewDestAbsLike(p string) bool {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "/") {
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

// atomicWriteFile writes dest through a temp file in its own directory. It is
// the write behind both `bn new` and `bn init`, and `bn init` runs before a
// vault exists, so the location policy is consulted on the destination itself
// rather than on a vault root there is none of yet. That is also what stops
// `bn init` from creating a vault somewhere no later command could write to.
func atomicWriteFile(label, dest string, data []byte) error {
	if err := vault.GuardMutation("", dest); err != nil {
		return err
	}
	dir := filepath.Dir(dest)
	var destMode os.FileMode
	destExists := false
	if info, err := os.Stat(dest); err == nil {
		destMode = info.Mode().Perm()
		destExists = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%s: stat %q: %w", label, dest, err)
	}

	tmp, err := os.CreateTemp(dir, ".new-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: tempfile in %q: %w", label, dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("%s: write %q: %w", label, tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("%s: fsync %q: %w", label, tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("%s: close %q: %w", label, tmpPath, err)
	}
	wantMode := destMode
	if !destExists {
		wantMode = 0o644
	}
	if err := os.Chmod(tmpPath, wantMode); err != nil {
		cleanup()
		return fmt.Errorf("%s: chmod %q: %w", label, tmpPath, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("%s: rename %q -> %q: %w", label, tmpPath, dest, err)
	}
	return nil
}
