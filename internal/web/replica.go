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

// initializeServeSafety resolves the Git safety engine for a mode that can
// write. A mode with no document writes never reaches a mutation, so requiring
// the engine there would refuse to serve a replica of a vault whose history
// lives on another machine.
func initializeServeSafety(root string, caps capabilities) error {
	if !caps.documentWrites {
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
		return r.URL.Path == briefViewAPIRoutePath || r.URL.Path == briefItemAPIRoutePath
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && replicaReadPath(r.URL.Path)
}

// openBriefTarget is the Brief's Open action. It keys on the browserOpen
// capability rather than on read-only, because hosted mode is equally unable to
// start an editor: the navigating branch resolves a route and provably never
// reaches openPath.
func (s *Server) openBriefTarget(notePath, rel string) (string, error) {
	if !s.caps.browserOpen {
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
