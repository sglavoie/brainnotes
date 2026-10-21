package noteops

import (
	"net/url"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const NoteRoutePrefix = "/n/"

// RoutePathForKey renders a slash-separated route key under prefix.
func RoutePathForKey(prefix, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", Errorf(CodeInvalidKey, "empty route key")
	}

	parts := strings.Split(key, "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", Errorf(CodeInvalidKey, "invalid route key %q: malformed segment", key)
		}
		escaped = append(escaped, url.PathEscape(part))
	}

	return prefix + strings.Join(escaped, "/"), nil
}

// RouteKeyFromPath decodes a route path produced by RoutePathForKey.
func RouteKeyFromPath(prefix, p string) (string, error) {
	if !strings.HasPrefix(p, prefix) {
		return "", Errorf(CodeInvalidKey, "path %q does not start with %s", p, prefix)
	}

	encodedKey := strings.TrimPrefix(p, prefix)
	if encodedKey == "" {
		return "", Errorf(CodeInvalidKey, "empty route key in path %q", p)
	}

	segments := strings.Split(encodedKey, "/")
	decoded := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return "", Errorf(CodeInvalidKey, "invalid route path %q: empty segment", p)
		}
		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			return "", WrapError(CodeInvalidKey, "decode route path", err)
		}
		if unescaped == "" || unescaped == "." || unescaped == ".." || strings.Contains(unescaped, "/") {
			return "", Errorf(CodeInvalidKey, "invalid route path %q: malformed segment", p)
		}
		decoded = append(decoded, unescaped)
	}

	return strings.Join(decoded, "/"), nil
}

// ResolveRouteNote resolves a route key through the current graph, matching
// web note pages without trusting a request-provided file path.
func ResolveRouteNote(notes []*vault.Note, graph *query.Graph, routeKey string) (*vault.Note, string, error) {
	if _, err := RoutePathForKey(NoteRoutePrefix, routeKey); err != nil {
		return nil, "", err
	}
	if graph == nil {
		return nil, "", Errorf(CodeInternal, "note graph is unavailable")
	}

	key := graph.UnresolvedTargetKey(routeKey)
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(routeKey))
	}
	if key == "" {
		return nil, "", Errorf(CodeInvalidKey, "empty route key")
	}

	notePath, ok := graph.PathsByTitle[key]
	if !ok {
		return nil, key, Errorf(CodeNotFound, "note not found")
	}
	for _, n := range notes {
		if n != nil && n.Path == notePath {
			return n, key, nil
		}
	}

	return &vault.Note{Path: notePath}, key, nil
}

// ResolveExistingRouteNote resolves a route key and verifies the target exists
// inside the vault root.
func ResolveExistingRouteNote(root string, notes []*vault.Note, graph *query.Graph, routeKey string) (*vault.Note, string, PathResolution, error) {
	note, key, err := ResolveRouteNote(notes, graph, routeKey)
	if err != nil {
		return nil, key, PathResolution{}, err
	}
	path, err := ResolveExistingPath(root, note.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, key, PathResolution{}, Errorf(CodeConflict, "note is missing")
		}
		return nil, key, PathResolution{}, err
	}
	if path.AbsPath != note.Path {
		note = &vault.Note{Path: path.AbsPath}
	}
	return note, key, path, nil
}
