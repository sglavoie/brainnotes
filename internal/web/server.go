package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))))
	mux.HandleFunc(noteRoutePrefix, srv.handleNote)
	mux.HandleFunc("/broken", srv.handleBroken)
	mux.HandleFunc("/api/facets", srv.handleFacets)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/open", srv.handleOpen)
	mux.HandleFunc("/api/brief/view", srv.handleBriefView)
	mux.HandleFunc("/api/brief/item", srv.handleBriefItem)
	mux.HandleFunc("/api/brief/insight", srv.handleBriefInsight)
	mux.HandleFunc("/api/note/insight", srv.handleNoteInsight)
	mux.HandleFunc("/api/notes/trash", srv.handleNoteTrash)
	mux.HandleFunc("/api/notes/create", srv.handleNoteCreate)
	mux.HandleFunc("/api/notes/rename", srv.handleNoteRename)
	mux.HandleFunc("/api/notes/move", srv.handleNoteMove)
	mux.HandleFunc("/api/notes/frontmatter/set", srv.handleNoteFrontmatterSet)
	mux.HandleFunc("/api/notes/frontmatter/unset", srv.handleNoteFrontmatterUnset)
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

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key, err := noteKeyFromPath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "snapshot error", http.StatusInternalServerError)
		return
	}

	note, normalizedKey, ok := noteForRoute(snapshot, key)
	if !ok {
		http.NotFound(w, r)
		return
	}

	body, err := note.Body()
	if err != nil {
		http.Error(w, "note body error", http.StatusInternalServerError)
		return
	}
	bodyHTML, err := s.markdown.renderWithGraph(body, snapshot.graph)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	front, err := note.Front()
	if err != nil {
		http.Error(w, "frontmatter error", http.StatusInternalServerError)
		return
	}

	notePath, err := notePathForKey(normalizedKey)
	if err != nil {
		http.Error(w, "path error", http.StatusInternalServerError)
		return
	}

	data := notePageData{
		Title:              note.Title(),
		Path:               notePath,
		Key:                normalizedKey,
		CSRFToken:          s.csrfToken,
		BodyHTML:           bodyHTML,
		Breadcrumbs:        breadcrumbsForNote(s.vault.Root, note, notePath),
		TOC:                tableOfContentsForMarkdown(body, notePath),
		Copy:               copyPayloadsForNote(s.vault.Root, note, notePath),
		Frontmatter:        renderFrontmatterEntries(front, snapshot.graph, &cfg.Frontmatter),
		FrontmatterChoices: frontmatterPropertyChoices(&cfg.Frontmatter),
		Backlinks:          backlinksForNote(snapshot, note),
		Outgoing:           outgoingLinksForNote(snapshot.graph, note),
		Broken:             brokenAffordancesForNote(note, snapshot.graph),
		InsightConfigured:  len(cfg.Agent.Command) > 0,
	}
	if !data.InsightConfigured {
		data.InsightStatusMsg = "Ask AI is unavailable until [agent].command is configured."
	}
	if err := s.templates.ExecuteTemplate(w, "note", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleBroken(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/broken" {
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

	queryText := strings.TrimSpace(r.URL.Query().Get("q"))
	filter := normalizeBrokenFilter(snapshot.graph, queryText)
	data := brokenPageData{
		Query:     queryText,
		CSRFToken: s.csrfToken,
		Entries:   brokenEntries(snapshot, filter),
		HasQuery:  queryText != "",
	}
	if err := s.templates.ExecuteTemplate(w, "broken", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/graph" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := graphPageData{
		Title:         "Graph - bn serve",
		FacetControls: graphFacetControlsData(),
	}
	if err := s.templates.ExecuteTemplate(w, "graph", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/search" {
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

	filters, err := parseNoteFilters(r.URL.Query(), s.vault.Root, snapshot.graph)
	if err != nil {
		http.Error(w, "bad search filters", http.StatusBadRequest)
		return
	}

	payload, err := buildSearchResponse(snapshot, s.vault.Root, filters)
	if err != nil {
		http.Error(w, "search error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/facets" {
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

	payload, err := buildFacetsResponse(snapshot, s.vault.Root)
	if err != nil {
		http.Error(w, "facets error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

func (s *Server) handleGraphJSON(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/graph.json" {
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

	filters, err := parseNoteFilters(r.URL.Query(), s.vault.Root, snapshot.graph)
	if err != nil {
		http.Error(w, "bad graph filters", http.StatusBadRequest)
		return
	}

	payload, err := buildGraphResponse(snapshot, s.vault.Root, filters)
	if err != nil {
		http.Error(w, "graph error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mediaPath, ok, err := mediaFilePathFromRequest(s.vault.Root, r.URL.Path)
	if err != nil {
		http.Error(w, "media lookup error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(mediaPath)
	if err != nil {
		http.Error(w, "media open error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "media stat error", http.StatusInternalServerError)
		return
	}

	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(mediaPath)))
	if contentType == "" {
		sniff := make([]byte, 512)
		n, _ := io.ReadFull(file, sniff)
		contentType = http.DetectContentType(sniff[:n])
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "media seek error", http.StatusInternalServerError)
			return
		}
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	http.ServeContent(w, r, filepath.Base(mediaPath), info.ModTime(), file)
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// Run binds the listener and optionally opens a browser. Both settings are
// read once here: a later reload of brainnotes.toml reports them through
// ConfigStatus.RestartRequired rather than rebinding a live listener.
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("web: context is required")
	}
	cfg := s.config()
	ln, err := net.Listen("tcp", cfg.Serve.Addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", cfg.Serve.Addr, err)
	}
	baseURL := "http://" + ln.Addr().String()
	if s.notifyListen != nil {
		s.notifyListen(baseURL)
	}
	if cfg.Serve.BrowserEnabled() {
		go func() {
			_ = s.openBrowser(baseURL)
		}()
	}
	return s.Serve(ctx, ln)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if ctx == nil {
		return errors.New("web: context is required")
	}
	if listener == nil {
		return errors.New("web: listener is required")
	}

	httpServer := &http.Server{Handler: s.handler}
	errCh := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("web: shutdown: %w", err)
		}
		return <-errCh
	}
}
