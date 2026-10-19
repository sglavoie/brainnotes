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
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const csrfHeaderName = "X-BN-CSRF"

var errOpenPathOutsideVault = errors.New("web: open path resolves outside vault")

const (
	openAPIRoutePath       = "/api/open"
	configOpenAPIRoutePath = "/api/config/open"
)

// openNavigationResponse is what POST /api/open answers with when the mode has
// no process-launch capability: the browser route that displays the note, which
// the client navigates to. A 204 would leave the caller believing an editor
// opened somewhere it cannot see.
type openNavigationResponse struct {
	OK          bool   `json:"ok"`
	Destination string `json:"destination"`
}

type openFailureStage string

const (
	openFailureCapability   openFailureStage = "capability"
	openFailureStat         openFailureStage = "stat"
	openFailureResolvePath  openFailureStage = "resolve-path"
	openFailureEditorSpec   openFailureStage = "editor-spec"
	openFailureEditorTarget openFailureStage = "editor-target"
	openFailureEditorStart  openFailureStage = "editor-start"
)

// errProcessLaunchDisabled is the refusal openPath raises for a mode with no
// process-launch capability. It exists so the guarantee is stated at the single
// place an editor is started rather than only at the routing boundary: a future
// caller that forgets the guard still cannot spawn a process.
var errProcessLaunchDisabled = errors.New("web: process launch is unavailable in this serving mode")

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
	key, ok := readOpenNoteRequest(s, w, r)
	if !ok {
		return
	}

	notePath, found, err := s.openNotePath(key)
	if err != nil {
		writeOpenPathError(w, err, openSubjectNote)
		return
	}
	if !found {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "note not found"))
		return
	}
	// A mode that cannot start a process answers with the route that displays
	// the note instead. This branch returns before openPath is reachable, so the
	// only exec in this handler is the one the local profile asked for.
	if s.caps.browserOpen {
		destination, err := s.noteNavigationTarget(notePath)
		if err != nil {
			writeOpenPathError(w, err, openSubjectNote)
			return
		}
		writeManagementJSON(w, http.StatusOK, openNavigationResponse{OK: true, Destination: destination})
		return
	}
	if err := s.openPath(notePath); err != nil {
		writeOpenFailure(w, err, openSubjectNote)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// readOpenNoteRequest performs the route, method, form, key and CSRF checks POST
// /api/open shares with every other management POST, and reports whether the
// request may proceed. It has already answered, in the management envelope,
// when it reports false.
func readOpenNoteRequest(s *Server, w http.ResponseWriter, r *http.Request) (string, bool) {
	if !readOpenRequest(s, w, r, openAPIRoutePath) {
		return "", false
	}
	key := strings.TrimSpace(r.Form.Get("key"))
	if key == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "missing key"))
		return "", false
	}
	if _, err := routePathForKey(noteRoutePrefix, key); err != nil {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidKey, "invalid key"))
		return "", false
	}
	return key, true
}

// readOpenRequest is the preamble both open routes share. The CSRF token is
// compared before the key is judged so a forged request learns nothing about
// which keys are well formed; it is checked after ParseForm because the token
// may arrive as a form field.
func readOpenRequest(s *Server, w http.ResponseWriter, r *http.Request, route string) bool {
	if r.URL.Path != route {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return false
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return false
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return false
	}
	return true
}

// openNotePath resolves a route key to an absolute note path. A snapshot that
// cannot be built is not fatal on its own: the route-shaped fallback can still
// find the file, and only a key that neither path resolves is a server error.
func (s *Server) openNotePath(key string) (string, bool, error) {
	snapshot, err := s.state.currentSnapshot()
	if err == nil {
		return s.notePathForOpenKey(snapshot, key)
	}
	notePath, err := s.notePathForOpenRouteFallback(key)
	if err != nil {
		return "", false, err
	}
	if notePath == "" {
		return "", false, errOpenSnapshotUnavailable
	}
	return notePath, true, nil
}

// handleConfigOpen opens the active vault-local brainnotes.toml through the
// same configured-editor resolution used for notes. It is a POST because
// launching a process is a side effect, and therefore carries the same CSRF
// protection as every other editor-open action.
func (s *Server) handleConfigOpen(w http.ResponseWriter, r *http.Request) {
	if !readOpenRequest(s, w, r, configOpenAPIRoutePath) {
		return
	}

	configPath := strings.TrimSpace(s.config().SourcePath)
	if configPath == "" {
		// Tests and embedded callers may construct Config directly instead of
		// loading it. The format still guarantees one vault-root config file.
		configPath = filepath.Join(s.vault.Root, config.FileName)
	}
	if err := s.openPath(configPath); err != nil {
		writeOpenFailure(w, err, openSubjectConfig)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// The two things this server will open on request. They exist so the shared
// failure mapping below names the right noun without either handler keeping its
// own copy of the switch — which is how the two came to disagree about which
// resolution failures were a 404.
const (
	openSubjectNote   = "note"
	openSubjectConfig = "config"
)

// errOpenSnapshotUnavailable is the one failure that is neither containment nor
// an absent file: the vault snapshot could not be built and the route-shaped
// fallback found nothing either, so the server cannot say whether the key names
// a real note.
var errOpenSnapshotUnavailable = errors.New("web: vault snapshot unavailable")

// writeOpenPathError maps a path-resolution failure onto a status. Containment
// is a 403 and an absent target a 404, so a caller cannot distinguish "outside
// the vault" from "not there" by anything but a path it already supplied.
func writeOpenPathError(w http.ResponseWriter, err error, subject string) {
	switch {
	case errors.Is(err, errOpenPathOutsideVault):
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "forbidden %s path", subject))
	case errors.Is(err, os.ErrNotExist):
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "%s not found", subject))
	case errors.Is(err, errOpenSnapshotUnavailable):
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "snapshot error", err))
	default:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, subject+" path error", err))
	}
}

// writeOpenFailure maps an openPath failure onto a status. The stage says which
// step failed and the subject says what was being opened; every editor stage is
// about this machine's configuration rather than about the target, so those
// messages are the same for a note and for the configuration file. The panel
// open actions answer through it too, so every Open speaks one envelope.
//
// An editor that is not configured, or whose target cannot be resolved, is a
// 412 rather than the 400 its code would otherwise give: the request was well
// formed and only this machine's configuration stands in the way.
func writeOpenFailure(w http.ResponseWriter, err error, subject string) {
	switch failureStage(err) {
	case openFailureCapability:
		// The sentinel's own text carries the package prefix every internal Go
		// error here carries; what reaches the browser is written for a reader.
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "process launch is unavailable in this serving mode"))
	case openFailureStat:
		if errors.Is(err, os.ErrNotExist) {
			writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "%s not found", subject))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, subject+" stat error", err))
	case openFailureResolvePath:
		writeOpenPathError(w, err, subject)
	case openFailureEditorSpec:
		if errors.Is(err, editor.ErrNoEditorConfigured) {
			writeManagementErrorStatus(w, http.StatusPreconditionFailed, noteops.Errorf(noteops.CodeInvalidRequest, "editor not configured"))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "editor spec error", err))
	case openFailureEditorTarget:
		writeManagementErrorStatus(w, http.StatusPreconditionFailed, noteops.Errorf(noteops.CodeInvalidRequest, "editor resolve error"))
	default:
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "editor start error", err))
	}
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

// noteNavigationTarget turns a resolved absolute note path into the /n/ route
// that displays it. The path has already been through resolveOpenPath, so the
// containment check here is a second statement of an invariant rather than the
// only one: a path that escapes the vault must never become a route.
func (s *Server) noteNavigationTarget(notePath string) (string, error) {
	// resolveOpenPath is the containment check the launching branch runs, so
	// the navigating branch is held to the same standard rather than to a
	// second copy of it: a path that would not be handed to an editor is not
	// turned into a route either. It also resolves symlinks, which matters
	// because the path reaching here comes from the snapshot graph and is not
	// necessarily in the same form as the resolved vault root.
	resolved, err := resolveOpenPath(s.vault.Root, notePath)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(s.vault.Root)
	if err != nil {
		return "", fmt.Errorf("web: resolve root %q: %w", s.vault.Root, err)
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("web: rel %q: %w", resolved, err)
	}
	return notePathForKey(managementRouteKeyForRelPath(filepath.ToSlash(rel)))
}

func (s *Server) openPath(path string) error {
	cfg := s.config()
	if !s.caps.processLaunch {
		return &openFailure{stage: openFailureCapability, err: errProcessLaunchDisabled}
	}
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
