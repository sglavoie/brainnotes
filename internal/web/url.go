package web

import (
	"fmt"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	return noteops.RoutePathForKey(prefix, key)
}

func routeKeyFromPath(prefix, path string) (string, error) {
	return noteops.RouteKeyFromPath(prefix, path)
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf("web: "+format, args...)
}
