package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

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

	insightConfigured, insightStatusMsg := s.agentAvailability(cfg, "Ask AI is unavailable until [agent].command is configured.")
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
		InsightConfigured:  insightConfigured,
		InsightStatusMsg:   insightStatusMsg,
		Pinned:             query.Pinned()(note),
		SynthesizePath:     synthesizeRoutePath + "?source=" + url.QueryEscape(normalizedKey),
		EditSourcePath:     editorRoutePath + "?key=" + url.QueryEscape(normalizedKey),
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

const searchAPIRoutePath = "/api/search"

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != searchAPIRoutePath {
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

	cfg := s.config()
	filters, err := parseNoteFilters(r.URL.Query(), s.vault.Root, snapshot.graph, &cfg.Frontmatter)
	if err != nil {
		writeFilterError(w, err)
		return
	}

	shape, err := parseResultShape(r.URL.Query())
	if err != nil {
		writeFilterError(w, err)
		return
	}
	var rank searchRelevanceFunc
	if shape.Sort.Key == queryexpr.SortRelevance {
		if rank, err = s.searchRelevanceRanker(snapshot, shape.Sort.RelativeTo); err != nil {
			writeSearchOrderError(w, err)
			return
		}
	}

	payload, err := buildSearchResponse(snapshot, s.vault.Root, filters, shape, rank)
	if err != nil {
		http.Error(w, "search error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("X-BN-Search-Total", strconv.Itoa(payload.Total))
	w.Header().Set("X-BN-Search-Limit", strconv.Itoa(shape.Limit))
	w.Header().Set("X-BN-Search-Cap", strconv.Itoa(searchResultLimit))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload.Results); err != nil {
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

	cfg := s.config()
	filters, err := parseNoteFilters(r.URL.Query(), s.vault.Root, snapshot.graph, &cfg.Frontmatter)
	if err != nil {
		writeFilterError(w, err)
		return
	}

	// q_mode is a display hint, not a filter: unknown or absent values mean
	// filter mode rather than a bad request.
	focusQuery := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("q_mode")), "focus")

	payload, err := buildGraphResponse(snapshot, s.vault.Root, filters, focusQuery)
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
	declared := contentType != ""
	if !declared {
		sniff := make([]byte, 512)
		n, _ := io.ReadFull(file, sniff)
		contentType = http.DetectContentType(sniff[:n])
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "media seek error", http.StatusInternalServerError)
			return
		}
	}
	applyMediaSafetyHeaders(w.Header(), filepath.Base(mediaPath), contentType, declared)

	http.ServeContent(w, r, filepath.Base(mediaPath), info.ModTime(), file)
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// Run binds the listener and optionally opens a browser. Both settings are read
// once here: a later reload reports them through ConfigStatus.RestartRequired
// rather than rebinding a live listener. The reload that matters is usually the
// overlay's — serve.addr and serve.open_browser are the machine-local layer's
// keys — but RestartRequired is computed from the merged configuration, so it
// does not care which file moved.
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
	// Launching the browser is a process launch like any other, so it answers to
	// the capability rather than to the configuration alone. `bn serve` already
	// forces --no-open for both remote profiles; this is the same refusal stated
	// where the process is started, so a library caller that builds Options by
	// hand over a vault whose [serve] section enables the browser cannot spawn
	// one on a hosted machine nobody is sitting at.
	if cfg.Serve.BrowserEnabled() && s.caps.processLaunch {
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

	httpServer := &http.Server{
		Handler: s.handler,
		// A connection that opens and then dribbles its headers costs a
		// goroutine until it is closed, which matters the moment the listener is
		// reachable from anything but loopback.
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// ReadTimeout and WriteTimeout are deliberately unset. They bound the
		// whole request and the whole response, and the agent routes legitimately
		// take minutes: a bound loose enough for those is too loose to be a
		// defence, so the slow-client risk is answered by the header and idle
		// timeouts above and the per-profile body bound in requestGuard.
	}
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
