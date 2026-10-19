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
	"github.com/sglavoie/sglavoie_brainnotes/internal/index"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const shutdownTimeout = 5 * time.Second

// Connection bounds that hold in every serving mode. Unlike the body bound they
// are not per profile: a request line and a header block are the same size
// whoever sent them, and a local server has nothing to gain from accepting a
// connection that never finishes its headers.
const (
	readHeaderTimeout = 20 * time.Second
	idleTimeout       = 2 * time.Minute
	maxHeaderBytes    = 1 << 20
)

//go:embed templates/*.html static/*
var embeddedFiles embed.FS

type Options struct {
	// ReadOnly and Hosted are independent: ReadOnly publishes a replica of a
	// vault this process does not own, Hosted publishes the vault itself over
	// the network with document actions intact and process launches denied.
	// Setting both is refused rather than resolved — see modeFor.
	ReadOnly          bool
	Hosted            bool
	StateDir          string
	ReplicaStatusFile string
	OpenBrowser       func(url string) error
	NotifyListening   func(url string)
	StaticFiles       fs.FS
	CSRFToken         string
	StartEditor       func(bin string, argv []string) error
	BriefState        brief.StateStore
	BriefNow          func() time.Time
	// RunAgent executes the optional AI daily-review command. It receives the
	// argv slice (already split, no shell) and the redacted review-context JSON
	// on stdin, and returns the command's stdout. Injected in tests so no real
	// process is launched; defaults to agent.Run.
	RunAgent agent.Runner
}

type Server struct {
	// caps is the resolved capability table for this server's mode. It replaces
	// the former readOnly field outright: nothing in the package branches on the
	// Options flags again after NewServer, so there is one source of truth for
	// what this deployment may do.
	caps capabilities
	// rootRedactor is the caps.absolutePaths answer applied to this vault: it
	// takes the root out of error messages sent to a remote reader.
	rootRedactor      vaultRootRedactor
	replicaStatusFile string
	configState       *configManager
	vault             *vault.Vault
	templates         *template.Template
	state             *vaultState
	previewStore      *bulkPreviewStore
	fixPreviews       *fixPreviewStore
	propertyPreviews  *propertyPreviewStore
	synthesisContexts *synthesisContextStore
	synthesisPreviews *synthesisPreviewStore
	cockpitState      *cockpit.StateStore
	markdown          *markdownRenderer
	handler           http.Handler
	openBrowser       func(url string) error
	notifyListen      func(url string)
	csrfToken         string
	// vaultID is an opaque, restart-stable identifier for this vault root. The
	// browser namespaces saved workbench state under it, so it must never carry
	// the path itself.
	vaultID     string
	startEditor func(bin string, argv []string) error
	briefState  brief.StateStore
	briefNow    func() time.Time
	runAgent    agent.Runner
	// beforePropertyCommit runs immediately before a property transaction
	// commits. It exists so tests can land an external edit inside the window
	// the configuration pin protects, which cannot be provoked reliably
	// otherwise; it is nil in production.
	beforePropertyCommit func()
	// beforeSynthesisCommit runs after a redeemed synthesis preview has been
	// revalidated and before its transaction commits. It exists so tests can
	// land an external write inside that window, which cannot be provoked
	// reliably otherwise; it is nil in production.
	beforeSynthesisCommit func()
	// beforePinnedNotesCommit runs after a pin plan is built and before it is
	// committed. It exists so tests can hold two requests inside the
	// validation-to-commit window the pinned-list preconditions protect, which
	// cannot be provoked reliably otherwise; it is nil in production.
	beforePinnedNotesCommit func()
	// pinnedNotesLockEscalation lowers the atomic transaction threshold for the
	// pins escalation regression. Only tests set it; zero preserves the
	// production threshold derived by vault.MaxLockedPaths.
	pinnedNotesLockEscalation int
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
	SynthesizePath     string
	EditSourcePath     string
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
		Placeholder: "Find tags, folders, properties…",
	}
}

func graphFacetControlsData() facetControlsData {
	return facetControlsData{
		IDPrefix:    "graph",
		Mode:        "graph",
		Label:       "Filter graph notes",
		Placeholder: "Find tags, folders, properties…",
	}
}

func NewServer(cfg *config.Config, v *vault.Vault, opts Options) (*Server, error) {
	if err := validateServerOptions(cfg, v, opts); err != nil {
		return nil, err
	}
	mode, err := modeFor(opts)
	if err != nil {
		return nil, err
	}
	caps := capabilitiesFor(mode)

	// The two template predicates are capabilities, not modes: a template asks
	// what it may render, so a fourth profile never needs a template edit.
	tpl, err := template.New("").Funcs(template.FuncMap{
		"readOnly":      func() bool { return !caps.documentWrites },
		"processLaunch": func() bool { return caps.processLaunch },
		"serveMode":     func() string { return string(caps.mode) },
	}).ParseFS(embeddedFiles, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	state, err := newServeVaultState(v, caps)
	if err != nil {
		return nil, err
	}
	markdown, err := newMarkdownRenderer(state)
	if err != nil {
		return nil, err
	}
	staticRoot, err := serveStaticRoot(opts.StaticFiles)
	if err != nil {
		return nil, err
	}

	configState, err := newConfigManager(cfg, v)
	if err != nil {
		return nil, err
	}
	rootRedactor := vaultRootRedactorFor(caps, v.Root)
	configState.redactor = rootRedactor

	// Resolve the vault's Git safety engine once, here, so a vault the engine
	// cannot open at all is a startup failure rather than a surprise on the first
	// write an hour into a session. The result is deliberately not stored on the
	// Server: every mutating route reaches the vault through internal/noteops,
	// which resolves the same process-shared engine itself, and handing handlers
	// a second reference would let one of them be wired to a different engine
	// than the operation it calls. A vault with no content history resolves to a
	// nil engine and is normal — the server serves it unversioned.
	if err := initializeServeSafety(v.Root, caps); err != nil {
		return nil, fmt.Errorf("web: vault git safety: %w", err)
	}

	srv := &Server{
		caps:              caps,
		rootRedactor:      rootRedactor,
		replicaStatusFile: opts.ReplicaStatusFile,
		configState:       configState,
		vault:             v,
		templates:         tpl,
		state:             state,
		previewStore:      newBulkPreviewStore(bulkPreviewTTL),
		fixPreviews:       newFixPreviewStore(fixPreviewTTL),
		propertyPreviews:  newPropertyPreviewStore(propertyPreviewTTL),
		synthesisContexts: newSynthesisContextStore(synthesisContextTTL),
		synthesisPreviews: newSynthesisPreviewStore(synthesisPreviewTTL),
		cockpitState:      cockpit.NewStateStore(v.Root),
		markdown:          markdown,
		openBrowser:       opts.OpenBrowser,
		notifyListen:      opts.NotifyListening,
		csrfToken:         opts.CSRFToken,
		vaultID:           synthesisVaultID(v.Root),
		startEditor:       opts.StartEditor,
		runAgent:          opts.RunAgent,
	}
	if err := srv.configureRuntime(opts); err != nil {
		return nil, err
	}
	srv.handler = srv.routes(staticRoot)
	srv.sweepStagedUploadsAtStartup()

	return srv, nil
}

// noCacheStatic keeps the local web UI in sync with the assets that belong to
// the running binary or source checkout. The browser otherwise reuses an older
// app.css URL across rebuilds, which can leave newly rendered markup completely
// unstyled even though the server is serving the current stylesheet.
//
// It relaxes the guard's blanket no-store for this one prefix: the bundle holds
// no vault content, so it may be stored and revalidated. `private` is repeated
// here rather than inherited, because Set replaces the guard's header outright
// and a shared cache upstream of a hosted deployment must not treat these as
// public even though they are the same bytes for everyone.
func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-cache")
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
	s.configureCapturePage(&data, snapshot)
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
	data.VaultID = s.vaultIdentity()
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

func validateServerOptions(cfg *config.Config, v *vault.Vault, opts Options) error {
	if cfg == nil {
		return errors.New("web: config is required")
	}
	if v == nil {
		return errors.New("web: vault is required")
	}
	if strings.TrimSpace(cfg.Serve.Addr) == "" {
		return errors.New("web: serve address is required")
	}

	if err := validateReplicaOptions(v.Root, opts); err != nil {
		return err
	}
	return nil
}

func serveStaticRoot(staticRoot fs.FS) (fs.FS, error) {
	if staticRoot != nil {
		return staticRoot, nil
	}
	return fs.Sub(embeddedFiles, "static")
}

// newServeVaultState keeps the note index in memory for a mode that cannot write
// the vault directory: a replica must not create <vault>/.bnotes/index/ in a
// tree it does not own. A hosted server owns its vault and keeps the persistent
// index, which is why this keys on documentWrites rather than on the mode.
func newServeVaultState(v *vault.Vault, caps capabilities) (*vaultState, error) {
	state, err := newVaultState(v, time.Second)
	if err != nil {
		return nil, err
	}
	if !caps.documentWrites {
		state.source, err = newIndexNoteSource(v, index.WithMemoryOnly())
	}
	return state, err
}

func (srv *Server) registerNoteMutationRoutes(mux *http.ServeMux) {
	mux.HandleFunc(briefInsightAPIRoutePath, srv.handleBriefInsight)
	mux.HandleFunc("/api/capture", srv.handleCapture)
	mux.HandleFunc("/api/notes/trash", srv.handleNoteTrash)
	mux.HandleFunc("/api/notes/create", srv.handleNoteCreate)
	mux.HandleFunc(newNoteAPIRoutePath, srv.handleNewNote)
	mux.HandleFunc("/api/notes/rename", srv.handleNoteRename)
	mux.HandleFunc("/api/notes/move", srv.handleNoteMove)
	mux.HandleFunc("/api/notes/frontmatter/set", srv.handleNoteFrontmatterSet)
	mux.HandleFunc("/api/notes/frontmatter/unset", srv.handleNoteFrontmatterUnset)
	mux.HandleFunc(notePinSetAPIRoutePath, srv.handleNotePinSet)
	mux.HandleFunc(notePinRemoveAPIRoutePath, srv.handleNotePinRemove)
	mux.HandleFunc(notePinReorderAPIRoutePath, srv.handleNotePinReorder)
	mux.HandleFunc("/api/notes/bulk/preview", srv.handleNotesBulkPreview)
	mux.HandleFunc("/api/notes/bulk", srv.handleNotesBulk)
}

// registerSynthesisRoutes groups the four synthesis APIs out of routes.
//
// The grouping is the same one registerNoteMutationRoutes makes and exists for
// the same reason: routes is a flat list at the funlen ceiling, so a session
// adding a surface has to give a few statements back. Only the APIs move —
// /synthesize stays beside the other pages, because grouping a page with the
// APIs it happens to call would put the two kinds of route in one bucket and
// make the next reader look for a page where the write classes live.
func (srv *Server) registerSynthesisRoutes(mux *http.ServeMux) {
	mux.HandleFunc(synthesisContextAPIRoutePath, srv.handleSynthesisContext)
	mux.HandleFunc(synthesisGenerateAPIRoutePath, srv.handleSynthesisGenerate)
	mux.HandleFunc(synthesisPreviewAPIRoutePath, srv.handleSynthesisPreview)
	mux.HandleFunc(synthesisApplyAPIRoutePath, srv.handleSynthesisApply)
}
