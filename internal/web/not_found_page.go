package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
)

type notFoundPageData struct {
	Title string
	// Wanted is the title a missing note address asked for; empty for an
	// address that is not a note at all.
	Wanted      string
	Closest     []searchResult
	LastClosest int
	SearchPath  string
	CreatePath  string
}

// writeNotFoundPage answers a browser navigation that leads nowhere with a
// page that still has the workspace around it, instead of the bare text
// net/http writes. noteKey is the note address that did not resolve, or empty.
//
// A person reaches a missing note through a stale link, a rename made in
// another editor, or a mistyped address, so the page offers the three things
// that recover from each: the closest titles, a search, and creating it. It
// reads the served snapshot only, records nothing, and still answers 404.
//
// Anything that is not a navigation — a script's fetch, an API client — keeps
// the plain text body: a client that asked for JSON has no use for a page.
func (s *Server) writeNotFoundPage(w http.ResponseWriter, r *http.Request, noteKey string) {
	if r.Method != http.MethodGet || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.NotFound(w, r)
		return
	}
	data := notFoundPageData{Title: "Not found - bn serve"}
	if wanted := missingNoteTitle(noteKey); wanted != "" {
		data.Wanted = wanted
		// A space is written %20: the template would re-escape the "+" that
		// QueryEscape writes for one.
		escaped := strings.ReplaceAll(url.QueryEscape(wanted), "+", "%20")
		data.SearchPath = libraryRoutePath + "?q=" + escaped
		data.CreatePath = "/new?title=" + escaped
		if snapshot, err := s.state.currentSnapshot(); err == nil {
			// A failed lookup costs the suggestions, never the page.
			data.Closest, _ = suggestSearchTitles(snapshot, s.vault.Root, foldSearchQuery(wanted))
			data.LastClosest = len(data.Closest) - 1
		}
	}
	var page bytes.Buffer
	if err := s.templates.ExecuteTemplate(&page, "not-found", data); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(page.Bytes())
}

// missingNoteTitle is the title a note key asks for: its last path segment.
func missingNoteTitle(noteKey string) string {
	key := strings.Trim(strings.TrimSpace(noteKey), "/")
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	return strings.TrimSpace(key)
}
