package noteops

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrOutsideVault = errors.New("noteops: path resolves outside vault")

// PathResolution is a vault-contained path in absolute and slash-relative form.
type PathResolution struct {
	AbsPath string
	RelPath string
}

// CleanRelPath normalizes a vault-relative path and rejects escaping paths.
func CleanRelPath(rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, `\`, "/"))
	if rel == "" {
		return "", Errorf(CodeInvalidRequest, "empty relative path")
	}
	if path.IsAbs(rel) || filepath.IsAbs(rel) {
		return "", Errorf(CodeForbidden, "path must be relative")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == "" {
		return "", Errorf(CodeInvalidRequest, "empty relative path")
	}
	if RelEscapesRoot(clean) {
		return "", WrapError(CodeForbidden, "path escapes vault", ErrOutsideVault)
	}
	return clean, nil
}

// RelEscapesRoot reports whether rel points outside its containing root.
func RelEscapesRoot(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	return rel == ".." || strings.HasPrefix(rel, "../")
}

// ResolveExistingPath resolves an existing path and verifies it remains in root.
func ResolveExistingPath(root, p string) (PathResolution, error) {
	rootResolved, rootAbs, err := resolveRoot(root)
	if err != nil {
		return PathResolution{}, err
	}
	targetAbs, err := absoluteUnderRoot(rootAbs, p)
	if err != nil {
		return PathResolution{}, err
	}
	targetResolved, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return PathResolution{}, WrapError(CodeConflict, "resolve existing path", err)
	}
	return containResolved(rootResolved, targetResolved)
}

// ResolveWritablePath resolves the deepest existing ancestor of p and verifies
// a write to p would remain in root. The final path does not need to exist.
func ResolveWritablePath(root, p string) (PathResolution, error) {
	rootResolved, rootAbs, err := resolveRoot(root)
	if err != nil {
		return PathResolution{}, err
	}
	targetAbs, err := absoluteUnderRoot(rootAbs, p)
	if err != nil {
		return PathResolution{}, err
	}
	targetResolved, err := resolveDeepestExistingPath(targetAbs)
	if err != nil {
		return PathResolution{}, WrapError(CodeConflict, "resolve writable path", err)
	}
	return containResolved(rootResolved, targetResolved)
}

// PathForRel resolves a vault-relative path for writing inside root.
func PathForRel(root, rel string) (PathResolution, error) {
	clean, err := CleanRelPath(rel)
	if err != nil {
		return PathResolution{}, err
	}
	return ResolveWritablePath(root, filepath.FromSlash(clean))
}

func resolveRoot(root string) (resolved, abs string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", Errorf(CodeInvalidRequest, "empty vault root")
	}
	abs, err = filepath.Abs(root)
	if err != nil {
		return "", "", WrapError(CodeInternal, "resolve vault root", err)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", WrapError(CodeInternal, "resolve vault root symlinks", err)
	}
	return resolved, abs, nil
}

func absoluteUnderRoot(rootAbs, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", Errorf(CodeInvalidRequest, "empty path")
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", WrapError(CodeInternal, "resolve path", err)
	}
	return targetAbs, nil
}

func containResolved(rootResolved, targetResolved string) (PathResolution, error) {
	rel, err := filepath.Rel(rootResolved, targetResolved)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, "compute vault-relative path", err)
	}
	if RelEscapesRoot(rel) || filepath.IsAbs(rel) {
		return PathResolution{}, WrapError(CodeForbidden, "path escapes vault", ErrOutsideVault)
	}
	return PathResolution{
		AbsPath: targetResolved,
		RelPath: filepath.ToSlash(rel),
	}, nil
}

func resolveDeepestExistingPath(p string) (string, error) {
	p = filepath.Clean(p)
	missing := make([]string, 0, 4)
	current := p
	for {
		_, err := os.Lstat(current)
		switch {
		case err == nil:
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("eval symlinks %q: %w", current, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		case os.IsNotExist(err):
			parent := filepath.Dir(current)
			if parent == current {
				return "", fmt.Errorf("no existing ancestor for %q", p)
			}
			missing = append(missing, filepath.Base(current))
			current = parent
		default:
			return "", fmt.Errorf("lstat %q: %w", current, err)
		}
	}
}
