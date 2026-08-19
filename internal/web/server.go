package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const shutdownTimeout = 5 * time.Second

//go:embed templates/*.html static/*
var embeddedFiles embed.FS

type Options struct {
	OpenBrowser     func(url string) error
	NotifyListening func(url string)
	StaticFiles     fs.FS
	CSRFToken       string
	StartEditor     func(bin string, argv []string) error
	BriefState      brief.StateStore
	BriefNow        func() time.Time
	// RunAgent executes the optional AI daily-review command. It receives the
	// argv slice (already split, no shell) and the redacted review-context JSON
	// on stdin, and returns the command's stdout. Injected in tests so no real
	// process is launched; defaults to agent.Run.
	RunAgent agent.Runner
}

type Server struct {
	configState      *configManager
	vault            *vault.Vault
	templates        *template.Template
	state            *vaultState
	previewStore     *bulkPreviewStore
	fixPreviews      *fixPreviewStore
	propertyPreviews *propertyPreviewStore
	cockpitState     *cockpit.StateStore
	markdown         *markdownRenderer
	handler          http.Handler
	openBrowser      func(url string) error
	notifyListen     func(url string)
	csrfToken        string
	startEditor      func(bin string, argv []string) error
	briefState       brief.StateStore
	briefNow         func() time.Time
	runAgent         agent.Runner
	// beforePropertyCommit runs immediately before a property transaction
	// commits. It exists so tests can land an external edit inside the window
	// the configuration pin protects, which cannot be provoked reliably
	// otherwise; it is nil in production.
	beforePropertyCommit func()
	// beforePinnedNotesCommit runs after a pin plan is built and before it is
	// committed. It exists so tests can hold two requests inside the
	// validation-to-commit window the pinned-list preconditions protect, which
	// cannot be provoked reliably otherwise; it is nil in production.
	beforePinnedNotesCommit func()
}

type notePageData struct {
	Title              string
	Path               string
	Key                string
	CSRFToken          string
	BodyHTML           template.HTML
	Breadcrumbs        []breadcrumbItem
	TOC                []tableOfContentsItem
	Copy               noteCopyPayloads
	Frontmatter        []frontmatterEntry
	FrontmatterChoices []string
	Backlinks          []noteLinkView
	Outgoing           []noteLinkView
	Broken             []brokenAffordance
	InsightConfigured  bool
	InsightStatusMsg   string
	Pinned             bool
}

type brokenPageData struct {
	Query     string
	CSRFToken string
	Entries   []brokenPageEntry
	HasQuery  bool
}

type graphPageData struct {
	Title         string
	FacetControls facetControlsData
}

type facetControlsData struct {
	IDPrefix    string
	Mode        string
	Label       string
	Placeholder string
}

func overviewFacetControlsData() facetControlsData {
	return facetControlsData{
		IDPrefix:    "overview",
		Mode:        "search",
		Label:       "Filter notes",
		Placeholder: "Find tags, folders, properties...",
	}
}

func graphFacetControlsData() facetControlsData {
	return facetControlsData{
		IDPrefix:    "graph",
		Mode:        "graph",
		Label:       "Filter graph facets",
		Placeholder: "Find tags, folders, properties...",
	}
}

func NewServer(cfg *config.Config, v *vault.Vault, opts Options) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("web: config is required")
	}
	if v == nil {
		return nil, errors.New("web: vault is required")
	}
	if strings.TrimSpace(cfg.Serve.Addr) == "" {
		return nil, errors.New("web: serve address is required")
	}

	tpl, err := template.ParseFS(embeddedFiles, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	state, err := newVaultState(v, time.Second)
	if err != nil {
		return nil, err
	}
	markdown, err := newMarkdownRenderer(state)
	if err != nil {
		return nil, err
	}
	staticRoot := opts.StaticFiles
	if staticRoot == nil {
		staticRoot, err = fs.Sub(embeddedFiles, "static")
		if err != nil {
			return nil, fmt.Errorf("web: static assets: %w", err)
		}
	}

	configState, err := newConfigManager(cfg, v)
	if err != nil {
		return nil, err
	}

	srv := &Server{
		configState:      configState,
		vault:            v,
		templates:        tpl,
		state:            state,
		previewStore:     newBulkPreviewStore(bulkPreviewTTL),
		fixPreviews:      newFixPreviewStore(fixPreviewTTL),
		propertyPreviews: newPropertyPreviewStore(propertyPreviewTTL),
		cockpitState:     cockpit.NewStateStore(v.Root),
		markdown:         markdown,
		openBrowser:      opts.OpenBrowser,
		notifyListen:     opts.NotifyListening,
		csrfToken:        opts.CSRFToken,
		startEditor:      opts.StartEditor,
		runAgent:         opts.RunAgent,
	}
	if srv.csrfToken == "" {
		srv.csrfToken, err = generateCSRFToken()
		if err != nil {
			return nil, err
		}
	}
	if srv.startEditor == nil {
		srv.startEditor = startEditorProcess
	}
	srv.briefNow = opts.BriefNow
	if srv.briefNow == nil {
		srv.briefNow = time.Now
	}
	srv.briefState = opts.BriefState
	if srv.briefState == nil {
		srv.briefState = brief.NewFileStateStore(v.Root, brief.WithClock(srv.briefNow))
	}
	if srv.runAgent == nil {
		srv.runAgent = agent.Run
	}

	mux := http.NewServeMux()
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot)))
	mux.Handle("/static/", noCacheStatic(staticHandler))
	mux.HandleFunc(noteRoutePrefix, srv.handleNote)
	mux.HandleFunc("/broken", srv.handleBroken)
	mux.HandleFunc("/api/facets", srv.handleFacets)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/open", srv.handleOpen)
	mux.HandleFunc(configOpenAPIRoutePath, srv.handleConfigOpen)
	mux.HandleFunc("/api/brief/view", srv.handleBriefView)
	mux.HandleFunc("/api/brief/item", srv.handleBriefItem)
	mux.HandleFunc("/api/brief/insight", srv.handleBriefInsight)
	mux.HandleFunc(relatedAPIRoutePath, srv.handleRelated)
	mux.HandleFunc("/api/note/insight", srv.handleNoteInsight)
	mux.HandleFunc("/api/capture", srv.handleCapture)
	mux.HandleFunc("/api/notes/trash", srv.handleNoteTrash)
	mux.HandleFunc("/api/notes/create", srv.handleNoteCreate)
	mux.HandleFunc("/api/notes/rename", srv.handleNoteRename)
	mux.HandleFunc("/api/notes/move", srv.handleNoteMove)
	mux.HandleFunc("/api/notes/frontmatter/set", srv.handleNoteFrontmatterSet)
	mux.HandleFunc("/api/notes/frontmatter/unset", srv.handleNoteFrontmatterUnset)
	mux.HandleFunc(notePinSetAPIRoutePath, srv.handleNotePinSet)
	mux.HandleFunc(notePinRemoveAPIRoutePath, srv.handleNotePinRemove)
	mux.HandleFunc(notePinReorderAPIRoutePath, srv.handleNotePinReorder)
	mux.HandleFunc("/api/notes/bulk/preview", srv.handleNotesBulkPreview)
	mux.HandleFunc("/api/notes/bulk", srv.handleNotesBulk)
	mux.HandleFunc("/api/history", srv.handleHistory)
	mux.HandleFunc("/api/undo", srv.handleUndo)
	mux.HandleFunc("/api/graph.json", srv.handleGraphJSON)
	mux.HandleFunc("/api/cockpit", srv.handleCockpitQueue)
	mux.HandleFunc("/api/cockpit/item", srv.handleCockpitItem)
	mux.HandleFunc("/api/daily-review/context", srv.handleDailyReviewContext)
	mux.HandleFunc("/api/daily-review/agent", srv.handleDailyReviewAgent)
	mux.HandleFunc("/api/fixes/preview", srv.handleFixesPreview)
	mux.HandleFunc("/api/fixes/apply", srv.handleFixesApply)
	mux.HandleFunc(propertiesAPIRoutePath, srv.handleProperties)
	mux.HandleFunc(propertiesDetailAPIRoutePath, srv.handlePropertyDetail)
	mux.HandleFunc(propertyValueAPIRoutePath, srv.handlePropertyValue)
	mux.HandleFunc(propertyPreviewAPIRoutePath, srv.handlePropertiesPreview)
	mux.HandleFunc(propertyApplyAPIRoutePath, srv.handlePropertiesApply)
	mux.HandleFunc(propertySchemaPreviewAPIRoutePath, srv.handlePropertySchemaPreview)
	mux.HandleFunc(propertySchemaApplyAPIRoutePath, srv.handlePropertySchemaApply)
	mux.HandleFunc(propertiesRoutePath, srv.handlePropertiesPage)
	mux.HandleFunc(propertiesRoutePrefix, srv.handlePropertyPage)
	mux.HandleFunc(pinnedRoutePath, srv.handlePinnedPage)
	mux.HandleFunc(libraryRoutePath, srv.handleLibrary)
	mux.HandleFunc(healthRoutePath, srv.handleHealth)
	mux.HandleFunc(configStatusRoutePath, srv.handleConfigStatus)
	mux.HandleFunc("/graph", srv.handleGraph)
	mux.HandleFunc(mediaRoutePrefix, srv.handleMedia)
	mux.HandleFunc("/", srv.handleIndex)

	if srv.openBrowser == nil {
		srv.openBrowser = openBrowserDefault
	}
	srv.handler = mux

	return srv, nil
}

// noCacheStatic keeps the local web UI in sync with the assets that belong to
// the running binary or source checkout. The browser otherwise reuses an older
// app.css URL across rebuilds, which can leave newly rendered markup completely
// unstyled even though the server is serving the current stylesheet.
func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
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
	data, err := s.buildRootPageData(snapshot)
	if err != nil {
		http.Error(w, "index error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = s.csrfToken
	cfg := s.config()
	data.CaptureRelPath, data.CaptureHeading, data.CaptureConfigured = captureDestination(cfg.Capture.InboxNote, cfg.Capture.Heading)
	if err := s.templates.ExecuteTemplate(w, "index", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != libraryRoutePath {
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
	data, err := buildLibraryPageData(snapshot, s.vault.Root)
	if err != nil {
		http.Error(w, "library error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = s.csrfToken
	if err := s.templates.ExecuteTemplate(w, "library", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if r.URL.Path != healthRoutePath {
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
	data, err := buildHealthPageData(snapshot, s.vault.Root)
	if err != nil {
		http.Error(w, "health error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = s.csrfToken

	// Derive diagnostics, Daily Fixes counts, and skipped-note reporting from
	// the one resilient shared snapshot instead of walking the vault again via
	// loadDiagnosticsData or cockpit.BuildQueue. Skipped notes are absent from
	// the snapshot's loaded set, so they never leak into recent notes, graph
	// data, search/facets, or the health counts below.
	diagnosticsData := diagnosticsDataFromSnapshot(snapshot, s.vault.Root)
	fixesQueue := cockpit.LimitDaily(s.cockpitQueueFilteredFrom(diagnosticsData), cfg.Cockpit.DailyLimit)
	data.Fixes = buildIndexFixesPanelData(fixesQueue)
	data.Diagnostics = buildDiagnosticsSummaryFromData(diagnosticsData, cfg)
	if err := s.templates.ExecuteTemplate(w, "health", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
