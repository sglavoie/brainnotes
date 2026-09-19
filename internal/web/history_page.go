package web

import (
	"net/http"
)

const (
	historyRoutePath        = "/history"
	historyPageTitle        = "History - bn serve"
	historyListAPIRoutePath = "/api/history"
	historyUndoAPIRoutePath = "/api/undo"
)

// historyPageData seeds the history and recovery workspace.
//
// It carries endpoints and a token and nothing else, for the reason
// syncPageData gives: every list on this page is a question about state that
// changes while somebody is reading it, and a page rendered with entries baked
// in would be describing a vault that has moved on. The filter vocabulary is not
// here either — the list route answers it, so the names in the filter and the
// names in the rows come from the same table.
type historyPageData struct {
	Title           string
	CSRFToken       string
	HistoryEndpoint string
	PreviewEndpoint string
	UndoEndpoint    string
	// PageSize and MaxPageSize are the server's own bounds, published so the page
	// can offer sizes the route will accept instead of discovering the ceiling by
	// being refused.
	PageSize    int
	MaxPageSize int
}

// handleHistoryPage renders the workspace for finding and recovering earlier
// content.
//
// A read-only replica does not serve it. replicaReadPath names neither this page
// nor the two routes it depends on, so both refuse, and the nav link is gated on
// the same capability the Settings and Sync links are. That is the right
// default: a replica holds somebody else's vault, and its undo journal describes
// writes it did not make and must not reverse.
func (s *Server) handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != historyRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := historyPageData{
		Title:           historyPageTitle,
		CSRFToken:       s.csrfToken,
		HistoryEndpoint: historyListAPIRoutePath,
		PreviewEndpoint: historyPreviewAPIRoutePath,
		UndoEndpoint:    historyUndoAPIRoutePath,
		PageSize:        historyEntryLimit,
		MaxPageSize:     historyEntryMaxLimit,
	}
	if err := s.templates.ExecuteTemplate(w, "history", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// registerHistoryRoutes groups the history surface — the list, the restore
// preview, the restore itself, and the page — for the reason registerSyncRoutes
// gives: routes is a flat list at the funlen ceiling, and the grouping that
// matters here is the capability, since a replica refuses every route in this
// function.
//
// The undo route is registered here rather than beside the note mutations it
// used to sit with, because what it now needs to be read next to is the preview
// that gates it. A reader who finds the force flag and has to search another
// file for the receipt it requires is a reader who might conclude there is no
// receipt.
func (srv *Server) registerHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc(historyListAPIRoutePath, srv.handleHistory)
	mux.HandleFunc(historyPreviewAPIRoutePath, srv.handleHistoryPreview)
	mux.HandleFunc(historyUndoAPIRoutePath, srv.handleUndo)
	mux.HandleFunc(historyRoutePath, srv.handleHistoryPage)
}
