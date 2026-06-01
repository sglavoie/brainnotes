package web

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	noteRoutePrefix  = "/n/"
	mediaRoutePrefix = "/media/"
)

func notePathForKey(key string) (string, error) {
	return routePathForKey(noteRoutePrefix, key)
}

func noteKeyFromPath(path string) (string, error) {
	return routeKeyFromPath(noteRoutePrefix, path)
}

func mediaPathForRel(rel string) (string, error) {
	return routePathForKey(mediaRoutePrefix, rel)
}

func mediaRelFromPath(path string) (string, error) {
	return routeKeyFromPath(mediaRoutePrefix, path)
}

func notePathForNote(graph *query.Graph, note *vault.Note) (string, error) {
	if graph == nil {
		return "", errorsf("nil graph")
	}
	if note == nil {
		return "", errorsf("nil note")
	}

	key := graph.KeyForNote(note)
	if key == "" {
		return "", errorsf("empty graph key for note %q", note.Path)
	}

	return notePathForKey(key)
}

func routePathForKey(prefix, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errorsf("empty route key")
	}

	parts := strings.Split(key, "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errorsf("invalid route key %q: malformed segment", key)
		}
		escaped = append(escaped, url.PathEscape(part))
	}

	return prefix + strings.Join(escaped, "/"), nil
}

func routeKeyFromPath(prefix, path string) (string, error) {
	if !strings.HasPrefix(path, prefix) {
		return "", errorsf("path %q does not start with %s", path, prefix)
	}

	encodedKey := strings.TrimPrefix(path, prefix)
	if encodedKey == "" {
		return "", errorsf("empty route key in path %q", path)
	}

	segments := strings.Split(encodedKey, "/")
	decoded := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return "", errorsf("invalid route path %q: empty segment", path)
		}
		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			return "", fmt.Errorf("unescape route path %q: %w", path, err)
		}
		if unescaped == "" || unescaped == "." || unescaped == ".." || strings.Contains(unescaped, "/") {
			return "", errorsf("invalid route path %q: malformed segment", path)
		}
		decoded = append(decoded, unescaped)
	}

	return strings.Join(decoded, "/"), nil
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf("web: "+format, args...)
}
