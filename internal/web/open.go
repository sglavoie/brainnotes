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

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

const csrfHeaderName = "X-BN-CSRF"

var errOpenPathOutsideVault = errors.New("web: open path resolves outside vault")

const configOpenAPIRoutePath = "/api/config/open"

type openFailureStage string

const (
	openFailureStat         openFailureStage = "stat"
	openFailureResolvePath  openFailureStage = "resolve-path"
	openFailureEditorSpec   openFailureStage = "editor-spec"
	openFailureEditorTarget openFailureStage = "editor-target"
	openFailureEditorStart  openFailureStage = "editor-start"
)

type openFailure struct {
	stage openFailureStage
	err   error
}

func (f *openFailure) Error() string {
	if f == nil {
		return "web: open failure"
	}
	return fmt.Sprintf("web: %s: %v", f.stage, f.err)
}

func (f *openFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.err
}

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

	notePath := ""
	ok := false
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		notePath, err = s.notePathForOpenRouteFallback(key)
		ok = notePath != ""
		if err == nil && !ok {
			http.Error(w, "snapshot error", http.StatusInternalServerError)
			return
		}
	} else {
		notePath, ok, err = s.notePathForOpenKey(snapshot, key)
	}
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
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.openPath(notePath); err != nil {
		switch failureStage(err) {
		case openFailureStat:
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "note stat error", http.StatusInternalServerError)
			return
		case openFailureResolvePath:
			switch {
			case errors.Is(err, errOpenPathOutsideVault):
				http.Error(w, "forbidden note path", http.StatusForbidden)
			case errors.Is(err, os.ErrNotExist):
				http.NotFound(w, r)
			default:
				http.Error(w, "note path error", http.StatusInternalServerError)
			}
			return
		case openFailureEditorSpec:
			if errors.Is(err, editor.ErrNoEditorConfigured) {
				http.Error(w, "editor not configured", http.StatusPreconditionFailed)
				return
			}
			http.Error(w, "editor spec error", http.StatusInternalServerError)
			return
		case openFailureEditorTarget:
			http.Error(w, "editor resolve error", http.StatusPreconditionFailed)
			return
		case openFailureEditorStart:
			http.Error(w, "editor start error", http.StatusInternalServerError)
			return
		default:
			http.Error(w, "editor start error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleConfigOpen opens the active vault-local brainnotes.toml through the
// same configured-editor resolution used for notes. It is a POST because
// launching a process is a side effect, and therefore carries the same CSRF
// protection as every other editor-open action.
func (s *Server) handleConfigOpen(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != configOpenAPIRoutePath {
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
	token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if token == "" {
		token = strings.TrimSpace(r.Form.Get("token"))
	}
	if !csrfTokenMatches(s.csrfToken, token) {
		http.Error(w, "csrf token required", http.StatusForbidden)
		return
	}

	configPath := strings.TrimSpace(s.config().SourcePath)
	if configPath == "" {
		// Tests and embedded callers may construct Config directly instead of
		// loading it. The format still guarantees one vault-root config file.
		configPath = filepath.Join(s.vault.Root, config.FileName)
	}
	if err := s.openPath(configPath); err != nil {
		switch failureStage(err) {
		case openFailureStat:
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "config stat error", http.StatusInternalServerError)
		case openFailureResolvePath:
			if errors.Is(err, errOpenPathOutsideVault) {
				http.Error(w, "forbidden config path", http.StatusForbidden)
				return
			}
			http.Error(w, "config path error", http.StatusInternalServerError)
		case openFailureEditorSpec:
			if errors.Is(err, editor.ErrNoEditorConfigured) {
				http.Error(w, "editor not configured", http.StatusPreconditionFailed)
				return
			}
			http.Error(w, "editor spec error", http.StatusInternalServerError)
		case openFailureEditorTarget:
			http.Error(w, "editor resolve error", http.StatusPreconditionFailed)
		default:
			http.Error(w, "editor start error", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notePathForOpenKey(snapshot vaultSnapshot, key string) (string, bool, error) {
	if snapshot.graph != nil {
		lookupKey := snapshot.graph.UnresolvedTargetKey(key)
		if lookupKey == "" {
			lookupKey = strings.ToLower(strings.TrimSpace(key))
		}
		if notePath, ok := snapshot.graph.PathsByTitle[lookupKey]; ok {
			return notePath, true, nil
		}
	}

	notePath, err := s.notePathForOpenRouteFallback(key)
	if err != nil {
		return "", false, err
	}
	return notePath, notePath != "", nil
}

func (s *Server) notePathForOpenRouteFallback(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || s == nil || s.vault == nil {
		return "", nil
	}
	rel := filepath.ToSlash(key) + ".md"
	candidate := filepath.Join(s.vault.Root, filepath.FromSlash(rel))
	resolvedPath, err := resolveOpenPath(s.vault.Root, candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return resolvedPath, nil
}

func (s *Server) openPath(path string) error {
	cfg := s.config()
	if _, err := os.Stat(path); err != nil {
		return &openFailure{stage: openFailureStat, err: err}
	}
	resolvedPath, err := resolveOpenPath(s.vault.Root, path)
	if err != nil {
		return &openFailure{stage: openFailureResolvePath, err: err}
	}
	spec, err := editor.Spec(cfg.Open.Editor, os.Getenv("EDITOR"))
	if err != nil {
		return &openFailure{stage: openFailureEditorSpec, err: err}
	}
	bin, argv, err := editor.Resolve(spec, resolvedPath, 1)
	if err != nil {
		return &openFailure{stage: openFailureEditorTarget, err: err}
	}
	if err := s.startEditor(bin, argv); err != nil {
		return &openFailure{stage: openFailureEditorStart, err: err}
	}
	return nil
}

func failureStage(err error) openFailureStage {
	var failure *openFailure
	if errors.As(err, &failure) {
		return failure.stage
	}
	return ""
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
	cmd := editorProcessCommand(bin, argv)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Editor opens are intentionally asynchronous, but a started child still
	// needs to be waited on eventually so repeated opens do not leave zombie
	// processes behind.
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func editorProcessCommand(bin string, argv []string) *exec.Cmd {
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Args = append([]string{argv[0]}, argv[1:]...)
	// Web opens run outside the CLI's exec handoff. Inherit the serving
	// process's terminal explicitly so terminal editors such as nvim remain
	// interactive instead of starting invisibly with /dev/null for stdio.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
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
