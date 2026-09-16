package web

import (
	"os"
	"path/filepath"
	"strings"
)

func mediaPathForTarget(root, target string) (string, bool, error) {
	rel, err := normalizeVaultRelativePath(target)
	if err != nil {
		return "", false, nil
	}

	_, ok, err := resolveContainedRegularFile(root, rel)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}

	route, err := mediaPathForRel(rel)
	if err != nil {
		return "", false, err
	}
	return route, true, nil
}

func mediaFilePathFromRequest(root, requestPath string) (string, bool, error) {
	rel, err := mediaRelFromPath(requestPath)
	if err != nil {
		return "", false, nil
	}
	rel, err = normalizeVaultRelativePath(rel)
	if err != nil {
		return "", false, nil
	}
	return resolveContainedRegularFile(root, rel)
}

func normalizeVaultRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return "", errorsf("empty vault-relative path")
	}

	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errorsf("invalid vault-relative path %q", raw)
		}
	}
	return strings.Join(parts, "/"), nil
}

func resolveMediaFile(root, rel string) (string, bool, error) {
	if root == "" || rel == "" {
		return "", false, nil
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, errorsf("resolve vault root %q: %v", root, err)
	}

	candidate := filepath.Join(root, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errorsf("resolve media path %q: %v", candidate, err)
	}

	contained, err := pathWithinRoot(realRoot, resolved)
	if err != nil {
		return "", false, errorsf("resolve media containment %q: %v", resolved, err)
	}
	if !contained {
		return "", false, nil
	}

	return regularMediaFile(resolved)
}

func regularMediaFile(resolved string) (string, bool, error) {
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errorsf("stat media path %q: %v", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}

	return resolved, true, nil
}

func pathWithinRoot(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	if filepath.IsAbs(rel) {
		return false, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

// Configuration and runtime internals are never attachments, including symlink aliases.
func privateMediaPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") || strings.EqualFold(part, "brainnotes.toml") {
			return true
		}
	}
	return false
}

func resolveContainedRegularFile(root, rel string) (string, bool, error) {
	if privateMediaPath(rel) {
		return "", false, nil
	}
	resolved, ok, err := resolveMediaFile(root, rel)
	if err != nil || !ok {
		return resolved, ok, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, err
	}
	resolvedRel, err := filepath.Rel(realRoot, resolved)
	if err != nil || privateMediaPath(filepath.ToSlash(resolvedRel)) {
		return "", false, err
	}
	return resolved, true, nil
}
