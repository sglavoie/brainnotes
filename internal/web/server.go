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
	// RunAgent executes the optional AI daily-review command. It receives the
	// argv slice (already split, no shell) and the redacted review-context JSON
	// on stdin, and returns the command's stdout. Injected in tests so no real
	// process is launched; defaults to runAgentCommand.
	RunAgent func(ctx context.Context, argv []string, stdin []byte) ([]byte, error)
}

type Server struct {
	cfg          *config.Config
	vault        *vault.Vault
	templates    *template.Template
	state        *vaultState
	previewStore *bulkPreviewStore
	fixPreviews  *fixPreviewStore
	cockpitState *cockpit.StateStore
	markdown     *markdownRenderer
	handler      http.Handler
	openBrowser  func(url string) error
	notifyListen func(url string)
	csrfToken    string
	startEditor  func(bin string, argv []string) error
	runAgent     func(ctx context.Context, argv []string, stdin []byte) ([]byte, error)
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

	srv := &Server{
		cfg:          cfg,
		vault:        v,
		templates:    tpl,
		state:        state,
		previewStore: newBulkPreviewStore(bulkPreviewTTL),
		fixPreviews:  newFixPreviewStore(fixPreviewTTL),
		cockpitState: cockpit.NewStateStore(v.Root),
		markdown:     markdown,
		openBrowser:  opts.OpenBrowser,
		notifyListen: opts.NotifyListening,
		csrfToken:    opts.CSRFToken,
		startEditor:  opts.StartEditor,
		runAgent:     opts.RunAgent,
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
	if srv.runAgent == nil {
		srv.runAgent = runAgentCommand
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))))
	mux.HandleFunc(noteRoutePrefix, srv.handleNote)
	mux.HandleFunc("/broken", srv.handleBroken)
	mux.HandleFunc("/api/facets", srv.handleFacets)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/open", srv.handleOpen)
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

	diagnosticsData, err := loadDiagnosticsData(s.vault)
	if err != nil {
		http.Error(w, "diagnostics error", http.StatusInternalServerError)
		return
	}
	snapshot := vaultSnapshot{notes: diagnosticsData.notes, graph: diagnosticsData.graph}
	data, err := buildIndexPageData(snapshot, s.vault.Root)
	if err != nil {
		http.Error(w, "index error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = s.csrfToken
	// Reuse the diagnostics load for the cockpit fixes panel instead of walking
	// the vault a second time via cockpit.BuildQueue.
	fixesQueue := cockpit.LimitDaily(s.cockpitQueueFilteredFrom(diagnosticsData), s.cfg.Cockpit.DailyLimit)
	data.Fixes = buildIndexFixesPanelData(fixesQueue)
	data.Diagnostics = buildDiagnosticsSummaryFromData(diagnosticsData, s.cfg)
	if err := s.templates.ExecuteTemplate(w, "index", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
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
		Frontmatter:        renderFrontmatterEntries(front, snapshot.graph, &s.cfg.Frontmatter),
		FrontmatterChoices: frontmatterPropertyChoices(&s.cfg.Frontmatter),
		Backlinks:          backlinksForNote(snapshot, note),
		Outgoing:           outgoingLinksForNote(snapshot.graph, note),
		Broken:             brokenAffordancesForNote(note, snapshot.graph),
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

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("web: context is required")
	}
	ln, err := net.Listen("tcp", s.cfg.Serve.Addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.cfg.Serve.Addr, err)
	}
	baseURL := "http://" + ln.Addr().String()
	if s.notifyListen != nil {
		s.notifyListen(baseURL)
	}
	if s.cfg.Serve.BrowserEnabled() {
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
