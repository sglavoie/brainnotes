package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

const csrfHeaderName = "X-BN-CSRF"

var errOpenPathOutsideVault = errors.New("web: open path resolves outside vault")

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/open" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request form", http.StatusBadRequest)
		return
	}

	key := strings.TrimSpace(r.Form.Get("key"))
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if _, err := routePathForKey(noteRoutePrefix, key); err != nil {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}

	token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if token == "" {
		token = strings.TrimSpace(r.Form.Get("token"))
	}
	if !csrfTokenMatches(s.csrfToken, token) {
		http.Error(w, "csrf token required", http.StatusForbidden)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}
	notePath, ok := snapshot.graph.PathsByTitle[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(notePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "note stat error", http.StatusInternalServerError)
		return
	}

	resolvedPath, err := resolveOpenPath(s.vault.Root, notePath)
	if err != nil {
		switch {
		case errors.Is(err, errOpenPathOutsideVault):
			http.Error(w, "forbidden note path", http.StatusForbidden)
		case errors.Is(err, os.ErrNotExist):
			http.NotFound(w, r)
		default:
			http.Error(w, "note path error", http.StatusInternalServerError)
		}
		return
	}

	spec, err := editor.Spec(s.cfg.Open.Editor, os.Getenv("EDITOR"))
	if err != nil {
		if errors.Is(err, editor.ErrNoEditorConfigured) {
			http.Error(w, "editor not configured", http.StatusPreconditionFailed)
			return
		}
		http.Error(w, "editor spec error", http.StatusInternalServerError)
		return
	}
	bin, argv, err := editor.Resolve(spec, resolvedPath, 1)
	if err != nil {
		http.Error(w, "editor resolve error", http.StatusPreconditionFailed)
		return
	}
	if err := s.startEditor(bin, argv); err != nil {
		http.Error(w, "editor start error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func csrfTokenMatches(expected, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func generateCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("web: generate csrf token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func startEditorProcess(bin string, argv []string) error {
	if bin == "" {
		return errors.New("web: empty editor binary")
	}
	if len(argv) == 0 {
		return errors.New("web: empty editor argv")
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Args = append([]string{argv[0]}, argv[1:]...)
	return cmd.Start()
}

func resolveOpenPath(root, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("web: resolve root %q: %w", root, err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("web: resolve note path %q: %w", path, err)
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return "", fmt.Errorf("web: rel %q: %w", realPath, err)
	}
	if relEscapesRoot(rel) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", errOpenPathOutsideVault, path)
	}
	return realPath, nil
}

func relEscapesRoot(rel string) bool {
	return rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		strings.HasPrefix(rel, `..\`)
}
