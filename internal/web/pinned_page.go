package web

import (
	"net/http"
	"strconv"

	"github.com/sglavoie/brainnotes/internal/pins"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	pinnedRoutePath  = "/pinned"
	pinnedPageTitle  = "Pinned - bn serve"
	indexPinnedLimit = 7
)

// pinnedNoteView is one row of the pinned list. Rank is empty for an unranked
// pin (a hand-written `pinned: true`), which sorts after every ranked pin and is
// given a real rank the first time it is reordered.
type pinnedNoteView struct {
	Key      string
	Title    string
	Path     string
	Folder   string
	RelPath  string
	Rank     string
	Unranked bool
}

// pinnedPageData renders the whole pinned list server-side. The reorder
// endpoint answers with the shared management result and cannot name the notes a
// renormalize moved, so the browser reloads this page rather than patching rows
// from a response.
type pinnedPageData struct {
	Title       string
	CSRFToken   string
	Pinned      []pinnedNoteView
	LibraryPath string
}

func (s *Server) handlePinnedPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != pinnedRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}

	data := pinnedPageData{
		Title:       pinnedPageTitle,
		CSRFToken:   s.csrfToken,
		Pinned:      buildPinnedNoteViews(snapshot, s.vault.Root, 0),
		LibraryPath: libraryRoutePath,
	}
	if err := s.templates.ExecuteTemplate(w, "pinned", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// buildPinnedNoteViews renders pins.List order into view rows, keeping at most
// limit of them when limit is positive. A pinned note with no route key is
// dropped rather than reported: the pinned list is a view, and one unroutable
// note must not hide the rest.
func buildPinnedNoteViews(snapshot vaultSnapshot, root string, limit int) []pinnedNoteView {
	if snapshot.graph == nil {
		return nil
	}

	byRelPath := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		byRelPath[noteVaultPath(root, note)] = note
	}

	entries := pins.List(root, snapshot.notes)
	views := make([]pinnedNoteView, 0, len(entries))
	for _, entry := range entries {
		note := byRelPath[entry.RelPath]
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}
		path, err := notePathForKey(key)
		if err != nil {
			continue
		}
		title := note.Title()
		if title == "" {
			title = key
		}
		view := pinnedNoteView{
			Key:      key,
			Title:    title,
			Path:     path,
			Folder:   folderFromSlashPath(entry.RelPath),
			RelPath:  entry.RelPath,
			Unranked: entry.Kind != pins.Ranked,
		}
		if entry.Kind == pins.Ranked {
			view.Rank = strconv.FormatFloat(entry.Rank, 'f', -1, 64)
		}
		views = append(views, view)
		if limit > 0 && len(views) == limit {
			break
		}
	}
	return views
}
