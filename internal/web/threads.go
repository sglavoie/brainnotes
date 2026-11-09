package web

import (
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/sglavoie/brainnotes/internal/threads"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

const threadsRoute = "/threads"
const threadsAPI = "/api/threads"
const threadPrepareAPI = "/api/threads/prepare"

type threadSummary struct {
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Path     string    `json:"path"`
	Modified time.Time `json:"modified"`
}

func threadSummaries(snapshot vaultSnapshot) []threadSummary {
	result := []threadSummary{}
	for _, note := range snapshot.notes {
		body, err := note.Body()
		if err != nil || !threads.IsThread(body) {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		route, err := notePathForKey(key)
		if err != nil {
			continue
		}
		item := threadSummary{Key: key, Title: note.Title(), Path: route}
		if info, err := os.Stat(note.Path); err == nil {
			item.Modified = info.ModTime()
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Modified.Equal(result[j].Modified) {
			return result[i].Key < result[j].Key
		}
		return result[i].Modified.After(result[j].Modified)
	})
	return result
}

func isThreadNote(note *vault.Note) bool {
	body, err := note.Body()
	return err == nil && threads.IsThread(body)
}

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != threadsRoute && r.URL.Path != threadsAPI {
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
		http.Error(w, "Threads are temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	items := threadSummaries(snapshot)
	if r.URL.Path == threadsAPI {
		writeManagementJSON(w, http.StatusOK, struct {
			Items   []threadSummary `json:"items"`
			Token   string          `json:"token"`
			VaultID string          `json:"vault_id"`
		}{items, s.csrfToken, s.vaultIdentity()})
		return
	}
	data := struct {
		Items                              []threadSummary
		CSRFToken, VaultID, Prefix, Suffix string
	}{
		items, s.csrfToken, s.vaultIdentity(), "## Question\n\n", "\n\n" + threads.Start + "\n\n" + threads.End + "\n\n## Draft\n\n",
	}
	if err := s.templates.ExecuteTemplate(w, "threads", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func recentThreads(snapshot vaultSnapshot) []threadSummary {
	all := threadSummaries(snapshot)
	if len(all) > 3 {
		all = all[:3]
	}
	return all
}

func threadSource(snapshot vaultSnapshot, link string) (*vault.Note, string, bool) {
	links := wikilink.Parse(link)
	if len(links) != 1 || snapshot.graph == nil {
		return nil, "", false
	}
	keys := snapshot.graph.ResolveTarget(links[0].Target)
	if len(keys) != 1 {
		return nil, "", false
	}
	return noteForRoute(snapshot, keys[0])
}

func synthesisThreadItems(note *vault.Note, snapshot vaultSnapshot) []threadItemView {
	body, err := note.Body()
	if err != nil || !threads.IsThread(body) {
		return nil
	}
	doc, err := threads.Parse(body)
	if err != nil {
		return nil
	}
	seen := map[string]bool{snapshot.graph.KeyForNote(note): true}
	result := []threadItemView{}
	for _, item := range threadItemViews(doc, snapshot) {
		if seen[item.Key] {
			continue
		}
		if item.Key != "" {
			seen[item.Key] = true
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) registerThreadRoutes(mux *http.ServeMux) {
	mux.HandleFunc(threadsRoute, s.handleThreads)
	mux.HandleFunc(threadsAPI, s.handleThreads)
	mux.HandleFunc(threadPrepareAPI, s.handleThreadPrepare)
}
