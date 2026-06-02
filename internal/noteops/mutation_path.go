package noteops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func mutationTargetPath(vaultRoot, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(vaultRoot, filepath.FromSlash(p))
}

func resolveExistingMutationPath(vaultRoot, p, label string) (PathResolution, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, fmt.Sprintf("%s resolve path", label), err)
	}
	if err := checkMutationPathContained(vaultRoot, abs); err != nil {
		return PathResolution{}, err
	}
	rel, err := filepath.Rel(vaultRoot, abs)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, fmt.Sprintf("%s compute relative path", label), err)
	}
	if RelEscapesRoot(rel) || filepath.IsAbs(rel) {
		return PathResolution{}, Errorf(CodeForbidden, "%s path is outside the vault", label)
	}
	return PathResolution{AbsPath: abs, RelPath: filepath.ToSlash(rel)}, nil
}

func resolveWritableMutationPath(vaultRoot, p, label string) (PathResolution, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, fmt.Sprintf("%s resolve path", label), err)
	}
	if err := checkMutationPathContained(vaultRoot, abs); err != nil {
		return PathResolution{}, err
	}
	rel, err := filepath.Rel(vaultRoot, abs)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, fmt.Sprintf("%s compute relative path", label), err)
	}
	if RelEscapesRoot(rel) || filepath.IsAbs(rel) {
		return PathResolution{}, Errorf(CodeForbidden, "%s path is outside the vault", label)
	}
	return PathResolution{AbsPath: abs, RelPath: filepath.ToSlash(rel)}, nil
}

func checkMutationPathContained(vaultRoot, abs string) error {
	checkRoot := vaultRoot
	if resolvedRoot, evalErr := filepath.EvalSymlinks(vaultRoot); evalErr == nil {
		checkRoot = resolvedRoot
	}
	checkPath := abs
	if resolved, evalErr := resolveExistingMutationPathTarget(abs); evalErr == nil {
		checkPath = resolved
	}
	rel, err := filepath.Rel(checkRoot, checkPath)
	if err != nil {
		return WrapError(CodeInternal, "compute vault-relative path", err)
	}
	if RelEscapesRoot(rel) || filepath.IsAbs(rel) {
		return Errorf(CodeForbidden, "path is outside the vault")
	}
	return nil
}

func resolveExistingMutationPathTarget(abs string) (string, error) {
	current := abs
	var suffix []string

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if len(suffix) == 0 {
				return resolved, nil
			}
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, resolved)
			parts = append(parts, suffix...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			if len(suffix) == 0 {
				return current, nil
			}
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, current)
			parts = append(parts, suffix...)
			return filepath.Join(parts...), nil
		}

		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func ensureRegularSource(path, op string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Errorf(CodeNotFound, "%s note not found", op)
		}
		return WrapError(CodeConflict, fmt.Sprintf("%s stat source note", op), err)
	}
	if !info.Mode().IsRegular() {
		return Errorf(CodeConflict, "%s source is not a regular file", op)
	}
	return nil
}
