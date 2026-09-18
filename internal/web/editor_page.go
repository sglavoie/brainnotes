package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const (
	editorRoutePath = "/edit"
	editorPageTitle = "Edit source - bn serve"
)

// editorPageData is everything the raw-Markdown editor needs before any script
// runs.
//
// The note's source is *not* among these fields, and that is deliberate. The
// page fetches it from /api/source, so the editor's bytes and the revision it
// pins its save to come from one read of one file at one moment. Rendering the
// content into the template would make the page two reads — the snapshot this
// handler resolves the note through, and the file the API reads later — with no
// revision tying them together, so a save could be planned against bytes the
// editor never showed anybody.
type editorPageData struct {
	Title     string
	CSRFToken string
	Key       string
	// VaultID names the vault this page's note belongs to, so a draft kept in
	// the browser can never be offered back over a same-named note in a
	// different vault the same origin served later. See vaultIdentity.
	VaultID     string
	NoteTitle   string
	NoteRoute   string
	RelPath     string
	SourcePath  string
	PreviewPath string
	SavePath    string
	CreatePath  string
	SearchPath  string
	// MaxSourceBytes is the server's own content limit, published so the editor
	// can say a note is too large before posting four megabytes to be told.
	MaxSourceBytes int
}

// handleEditorPage serves the whole-file Markdown editor for one existing note.
//
// It resolves the note the way /synthesize resolves its anchor — exactly one
// key, through the shared snapshot — so a page only exists for a note this
// server can actually serve. It is absent from replicaReadPath, so a read-only
// replica refuses it at the guard along with the four /api/source routes it
// would call; nothing here re-derives that.
func (s *Server) handleEditorPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != editorRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	keys := r.URL.Query()["key"]
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" {
		http.Error(w, "a single note key is required", http.StatusBadRequest)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}
	note, normalizedKey, ok := noteForRoute(snapshot, strings.TrimSpace(keys[0]))
	if !ok {
		http.NotFound(w, r)
		return
	}
	route, err := notePathForKey(normalizedKey)
	if err != nil {
		http.Error(w, "path error", http.StatusInternalServerError)
		return
	}

	title := note.Title()
	if title == "" {
		title = normalizedKey
	}
	data := editorPageData{
		Title:          editorPageTitle,
		CSRFToken:      s.csrfToken,
		Key:            normalizedKey,
		VaultID:        s.vaultIdentity(),
		NoteTitle:      title,
		NoteRoute:      route,
		RelPath:        noteVaultPath(s.vault.Root, note),
		SourcePath:     sourceAPIRoutePath,
		PreviewPath:    sourcePreviewAPIRoutePath,
		SavePath:       sourceSaveAPIRoutePath,
		CreatePath:     sourceCreateAPIRoutePath,
		SearchPath:     searchAPIRoutePath,
		MaxSourceBytes: noteops.MaxSourceBytes,
	}
	if err := s.templates.ExecuteTemplate(w, "editor", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
