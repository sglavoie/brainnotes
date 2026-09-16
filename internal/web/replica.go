package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func initializeServeSafety(root string, readOnly bool) error {
	if readOnly {
		return nil
	}
	_, err := noteops.SafetyFor(root)
	return err
}

func validateReplicaOptions(root string, opts Options) error {
	if opts.ReadOnly && opts.StateDir == "" {
		return errors.New("web: --read-only requires an external --state-dir")
	}
	if opts.StateDir == "" {
		return nil
	}
	return validateExternalState(root, opts.StateDir)
}

func validateExternalState(root, stateDir string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	// Require an existing directory, so checking it never creates vault entries.
	state, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return err
	}
	state, err = filepath.Abs(state)
	if err != nil {
		return err
	}
	info, err := os.Stat(state)
	if err != nil {
		return err
	}
	inside, err := pathWithinRoot(realRoot, state)
	if err != nil {
		return err
	}
	if inside || !info.IsDir() {
		return errors.New("web: state directory must be outside the vault")
	}
	return nil
}

// Use an allowlist: a future mutating route is unavailable until reviewed here.
func replicaReadPath(path string) bool {
	switch path {
	case "/", "/library", "/pinned", "/properties", "/health", "/broken", "/graph",
		"/api/ready", "/api/facets", "/api/search", "/api/query/validate", "/api/query/completions",
		"/api/graph.json", "/api/properties", "/api/properties/detail", "/api/views",
		"/api/related", "/api/cockpit", "/api/config/status":
		return true
	}
	for _, prefix := range []string{noteRoutePrefix, mediaRoutePrefix, "/static/", propertiesRoutePrefix} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func replicaRequestAllowed(r *http.Request) bool {
	if r.Method == http.MethodPost {
		return r.URL.Path == "/api/brief/view" || r.URL.Path == "/api/brief/item"
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && replicaReadPath(r.URL.Path)
}

func (s *Server) readOnlyGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.readOnly {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !replicaRequestAllowed(r) {
				writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "read-only replica"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) openBriefTarget(notePath, rel string) (string, error) {
	if !s.readOnly {
		return "", s.openPath(notePath)
	}
	if _, ok, err := resolveContainedRegularFile(s.vault.Root, rel); err != nil || !ok {
		return "", noteops.Errorf(noteops.CodeNotFound, "brief note is unavailable")
	}
	return notePathForKey(managementRouteKeyForRelPath(rel))
}

type replicaStatus struct {
	LastSuccessfulSync time.Time `json:"last_successful_sync"`
	Stale              bool      `json:"stale"`
}

func readReplicaStatus(path string) replicaStatus {
	status := replicaStatus{Stale: true}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &status) != nil {
		return replicaStatus{Stale: true}
	}
	age := time.Since(status.LastSuccessfulSync)
	status.Stale = status.LastSuccessfulSync.IsZero() || age > 15*time.Minute || age < -time.Minute
	return status
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.state.currentSnapshot()
	ready := err == nil && len(snapshot.notes) > 0
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeManagementJSON(w, code, struct {
		Ready   bool          `json:"ready"`
		Replica replicaStatus `json:"replica"`
	}{ready, readReplicaStatus(s.replicaStatusFile)})
}
