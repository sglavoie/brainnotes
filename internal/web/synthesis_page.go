package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const (
	synthesizeRoutePath = "/synthesize"
	synthesizePageTitle = "Synthesize - bn serve"
)

// synthesizePageData is everything the workbench needs before any script runs.
// The anchor identity and the destination default are rendered server-side so
// the page states what it is about even with scripting disabled.
type synthesizePageData struct {
	Title           string
	CSRFToken       string
	VaultID         string
	AnchorKey       string
	AnchorTitle     string
	AnchorRoute     string
	AnchorRelPath   string
	DefaultFolder   string
	DefaultPurpose  string
	Purposes        []string
	MaxSupporters   int
	AgentConfigured bool
	AgentStatusMsg  string
	ContextPath     string
	GeneratePath    string
	PreviewPath     string
	ApplyPath       string
	RelatedPath     string
	SearchPath      string
	LibraryPath     string
}

func (s *Server) handleSynthesizePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != synthesizeRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Exactly one anchor. A missing key has nothing to synthesize from, and two
	// keys mean the caller believes in an anchor the workbench does not have.
	sources := r.URL.Query()["source"]
	if len(sources) != 1 || strings.TrimSpace(sources[0]) == "" {
		http.Error(w, "a single source note key is required", http.StatusBadRequest)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}
	note, normalizedKey, ok := noteForRoute(snapshot, strings.TrimSpace(sources[0]))
	if !ok {
		http.NotFound(w, r)
		return
	}
	route, err := notePathForKey(normalizedKey)
	if err != nil {
		http.Error(w, "path error", http.StatusInternalServerError)
		return
	}

	relPath := noteVaultPath(s.vault.Root, note)
	title := note.Title()
	if title == "" {
		title = normalizedKey
	}
	cfg := s.config()
	data := synthesizePageData{
		Title:           synthesizePageTitle,
		CSRFToken:       s.csrfToken,
		VaultID:         s.vaultID,
		AnchorKey:       normalizedKey,
		AnchorTitle:     title,
		AnchorRoute:     route,
		AnchorRelPath:   relPath,
		DefaultFolder:   folderFromSlashPath(relPath),
		DefaultPurpose:  recall.PurposeSynthesis,
		Purposes:        recall.StandardPurposeNames(),
		MaxSupporters:   recall.MaxCuratedSupporters,
		AgentConfigured: len(cfg.Agent.Command) > 0,
		ContextPath:     synthesisContextAPIRoutePath,
		GeneratePath:    synthesisGenerateAPIRoutePath,
		PreviewPath:     synthesisPreviewAPIRoutePath,
		ApplyPath:       synthesisApplyAPIRoutePath,
		RelatedPath:     relatedAPIRoutePath,
		SearchPath:      searchAPIRoutePath,
		LibraryPath:     libraryRoutePath,
	}
	if !data.AgentConfigured {
		data.AgentStatusMsg = "Generation is unavailable until [agent].command is configured."
	}
	if err := s.templates.ExecuteTemplate(w, "synthesize", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
