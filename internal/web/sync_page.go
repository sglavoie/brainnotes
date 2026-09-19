package web

import (
	"net/http"
)

const (
	syncRoutePath = "/sync"
	syncPageTitle = "Synchronization - bn serve"
)

// syncPageData seeds the synchronization page.
//
// Like the settings page it carries endpoints and a token and nothing else: the
// status and the conflicts are both fetched from their own APIs, so the page and
// the APIs cannot report different states. It matters more here than anywhere
// else, because what this page reports is how far behind the vault is — a state
// rendered once into HTML would be wrong by the time somebody read it.
type syncPageData struct {
	Title     string
	CSRFToken string
	// VaultID names the vault, so a merge somebody is composing here is kept in
	// the browser under a key no other vault served by this origin can read. It is
	// the same identity the editor page publishes, for the same reason — see
	// vaultIdentity — and a merge is the one text on this page that exists nowhere
	// else, since only its author has it.
	VaultID           string
	StatusEndpoint    string
	ConflictsEndpoint string
	PreviewEndpoint   string
	ResolveEndpoint   string
}

// handleSyncPage renders the synchronization status and conflict review surface.
//
// A read-only replica does not serve it: replicaReadPath names neither this page
// nor the conflict APIs, so they answer 403 and the nav link is gated, and the
// only synchronization route a replica answers is the status one — which tells it
// about its own freshness rather than about somebody else's vault. See
// replicaSyncStatus for why those are different questions.
func (s *Server) handleSyncPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != syncRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := syncPageData{
		Title:             syncPageTitle,
		CSRFToken:         s.csrfToken,
		VaultID:           s.vaultIdentity(),
		StatusEndpoint:    syncStatusAPIRoutePath,
		ConflictsEndpoint: syncConflictsAPIRoutePath,
		PreviewEndpoint:   syncPreviewAPIRoutePath,
		ResolveEndpoint:   syncResolveAPIRoutePath,
	}
	if err := s.templates.ExecuteTemplate(w, "sync", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// registerSyncRoutes groups the synchronization surface, for the reason
// registerPageRoutes gives: routes is a flat list at the funlen ceiling, so a
// session adding a surface gives statements back rather than taking them.
//
// The page is here with its APIs rather than in registerPageRoutes because the
// grouping that matters for this surface is the capability: every route in this
// function is refused wholesale on a replica except the status one, which is in
// replicaReadPath.
func (srv *Server) registerSyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc(syncStatusAPIRoutePath, srv.handleSyncStatus)
	mux.HandleFunc(syncConflictsAPIRoutePath, srv.handleSyncConflicts)
	mux.HandleFunc(syncPreviewAPIRoutePath, srv.handleSyncPreview)
	mux.HandleFunc(syncResolveAPIRoutePath, srv.handleSyncResolve)
	mux.HandleFunc(syncRoutePath, srv.handleSyncPage)
}
