package web

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	viewsAPIRoutePath        = "/api/views"
	viewsPreviewAPIRoutePath = "/api/views/preview"
	viewsApplyAPIRoutePath   = "/api/views/apply"
)

// Journal command labels. History and undo read these, so they name the user's
// action rather than the internal endpoint.
const (
	viewsUpsertCommand = "bn serve view save"
	viewsRenameCommand = "bn serve view rename"
	viewsDeleteCommand = "bn serve view delete"
)

// viewMutationOp names one change to the [[views]] section of brainnotes.toml.
type viewMutationOp string

const (
	viewOpUpsert viewMutationOp = "upsert"
	viewOpRename viewMutationOp = "rename"
	viewOpDelete viewMutationOp = "delete"
)

// viewMutationRequest is one saved-view edit. It is always previewed before it
// can be applied: see the saved-views write class in
// docs/internals/web-write-classes.md.
type viewMutationRequest struct {
	Token string         `json:"token"`
	Op    viewMutationOp `json:"op"`
	Name  string         `json:"name"`
	// NewName is the destination name for a rename.
	NewName string `json:"new_name"`
	// View carries the stored fields for an upsert. Its name comes from Name, so
	// a request cannot disagree with itself.
	View *viewDefinition `json:"view"`
}

// viewDefinition is the transport form of one saved view's stored fields.
type viewDefinition struct {
	Query       string `json:"query"`
	Sort        string `json:"sort"`
	RelativeTo  string `json:"relative_to"`
	Limit       int    `json:"limit"`
	Description string `json:"description"`
}

func (d *viewDefinition) savedView(name string) config.SavedView {
	if d == nil {
		return config.SavedView{Name: name}
	}
	return config.SavedView{
		Name:        name,
		Query:       strings.TrimSpace(d.Query),
		Sort:        strings.TrimSpace(d.Sort),
		RelativeTo:  strings.TrimSpace(d.RelativeTo),
		Limit:       d.Limit,
		Description: strings.TrimSpace(d.Description),
	}
}

// viewSummary is one resolved saved view. A view whose query stopped compiling
// is reported disabled with its detail rather than dropped — the contract
// queryexpr.ResolveViews exists to make the default for every consumer.
type viewSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Query       string `json:"query"`
	// Normalized is the canonical Expr.String() spelling, empty for a disabled
	// view. It is what `bn views show` prints under `Query:`.
	Normalized string `json:"normalized,omitempty"`
	Sort       string `json:"sort,omitempty"`
	RelativeTo string `json:"relative_to,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Enabled    bool   `json:"enabled"`
	// Detail is why a disabled view is disabled: a caret block for a query that
	// no longer parses, a one-line message for an unusable sort spec.
	Detail     string `json:"detail,omitempty"`
	NeedsGraph bool   `json:"needs_graph"`
}

// viewsResponse is the shared shape of all three routes. A preview fills
// PreviewID and ExpiresAt; an apply fills JournalID instead, so a client reads
// one contract either way.
type viewsResponse struct {
	OK         bool          `json:"ok"`
	Generation int64         `json:"generation"`
	Action     string        `json:"action"`
	Name       string        `json:"name,omitempty"`
	NewName    string        `json:"new_name,omitempty"`
	Views      []viewSummary `json:"views"`
	// Created reports that an upsert introduces a block that does not exist yet.
	// It is what lets the client auto-chain preview into apply for a creation
	// while still confirming the rendered diff for an overwrite.
	Created    bool                     `json:"created"`
	ConfigDiff []propertyConfigDiffLine `json:"config_diff"`
	PreviewID  string                   `json:"preview_id,omitempty"`
	ExpiresAt  string                   `json:"expires_at,omitempty"`
	JournalID  string                   `json:"journal_id,omitempty"`
	Config     ConfigStatus             `json:"config"`
}

// viewsPlan is a fully resolved, not-yet-written change to the [[views]]
// section. Like propertySchemaPlan it never carries note rewrites: a saved view
// is config only.
type viewsPlan struct {
	Op      viewMutationOp
	Name    string
	NewName string
	Created bool
	// Views is the section as the plan would leave it, decoded from the patched
	// bytes so the preview lists what the loader will actually accept.
	Views  []config.SavedView
	Config *propertyConfigPlan
}

func (p *viewsPlan) fileStates() []propertyPreviewFileState {
	if p == nil || p.Config == nil {
		return []propertyPreviewFileState{}
	}
	return []propertyPreviewFileState{{
		RelPath: config.FileName,
		AbsPath: p.Config.AbsPath,
		Digest:  p.Config.SourceHash,
	}}
}

// handleViews lists every saved view. It is read-only and takes no CSRF token,
// per the read-only route class: exact path, GET only, one config read and no
// vault walk — queryexpr.Compile validates against declared property names
// alone, so listing views never needs a snapshot or a graph.
func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != viewsAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed,
			noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	cfg, status := s.configState.publication()
	writeManagementJSON(w, http.StatusOK, viewsResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     "list",
		Views:      s.viewSummaries(cfg, cfg.Views),
		ConfigDiff: []propertyConfigDiffLine{},
		Config:     status,
	})
}

// handleViewsPreview plans a saved-view change and issues the one-use receipt
// the apply endpoint redeems.
func (s *Server) handleViewsPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, viewsPreviewAPIRoutePath) {
		return
	}
	req, err := s.parseViewMutationRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	cfg, plan, status, _, err := s.planViewsRequest(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	response := viewsResponseForPlan(s, cfg, plan, plan.Views, status)
	if plan.Config != nil {
		receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
			Kind:       propertyPreviewView,
			Generation: status.Generation,
			View:       req,
			Files:      plan.fileStates(),
		})
		if err != nil {
			writeManagementError(w, err)
			return
		}
		response.PreviewID = receipt.ID
		response.ExpiresAt = receipt.ExpiresAt.Format(time.RFC3339)
	}
	writeManagementJSON(w, http.StatusOK, response)
}

// handleViewsApply redeems a view receipt and writes brainnotes.toml. It
// accepts no plan of its own: the stored request is replanned and must reproduce
// exactly the bytes the preview was pinned to, so an apply can never write what
// the preview did not show.
func (s *Server) handleViewsApply(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, viewsApplyAPIRoutePath) {
		return
	}
	req, err := s.parsePropertyApplyRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewView)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPreviewFilesUnchanged(record.Files); err != nil {
		writeManagementError(w, viewsWriteError(err))
		return
	}

	_, plan, status, pin, err := s.planViewsRequest(record.View)
	if err != nil {
		writeManagementError(w, viewsWriteError(err))
		return
	}
	if status.Generation != record.Generation || plan.Config == nil ||
		!reflect.DeepEqual(plan.fileStates(), record.Files) {
		writeManagementError(w, viewsStaleConfigError())
		return
	}

	entry, err := s.commitPropertyPlan(&propertyPlan{Config: plan.Config}, viewsCommand(plan.Op), pin)
	if err != nil {
		writeManagementError(w, viewsWriteError(err))
		return
	}
	s.finishConfigWrite(true)

	cfg, status := s.configState.publication()
	response := viewsResponseForPlan(s, cfg, plan, cfg.Views, status)
	response.JournalID = managementJournalID(entry)
	writeManagementJSON(w, http.StatusOK, response)
}

// planViewsRequest pins the configuration the plan was built from, so the commit
// can verify it is still the one on disk. It reads no snapshot: a saved view is
// a config-only change and nothing about it is measured against the notes.
func (s *Server) planViewsRequest(req viewMutationRequest) (*config.Config, *viewsPlan, ConfigStatus, configPin, error) {
	cfg, status, pin := s.pinnedConfig()
	plan, err := planViewsMutation(cfg, s.configState.sourcePath, s.vault.Root, req)
	if err != nil {
		return cfg, nil, status, pin, err
	}
	return cfg, plan, status, pin, nil
}

// planViewsMutation patches the [[views]] section in memory and re-decodes the
// result, so the projected list is the one the loader would accept rather than
// the one the patcher happened to emit.
func planViewsMutation(cfg *config.Config, configPath, root string, req viewMutationRequest) (*viewsPlan, error) {
	_, exists := cfg.View(req.Name)
	plan := &viewsPlan{Op: req.Op, Name: req.Name, NewName: req.NewName, Views: cfg.Views}

	patch := config.ViewPatch{Name: req.Name}
	switch req.Op {
	case viewOpUpsert:
		patch.Op = config.PatchUpsert
		patch.View = req.View.savedView(req.Name)
		plan.Created = !exists
		if err := checkViewResolves(patch.View, cfg, root); err != nil {
			return nil, err
		}
	case viewOpRename:
		if !exists {
			return nil, viewNotDeclaredError(req.Name)
		}
		patch.Op = config.PatchRename
		patch.NewName = req.NewName
	case viewOpDelete:
		if !exists {
			return nil, viewNotDeclaredError(req.Name)
		}
		patch.Op = config.PatchDelete
	}

	source, err := os.ReadFile(configPath)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "views: read "+config.FileName, err)
	}
	result, err := config.PatchViews(source, []config.ViewPatch{patch})
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest, err.Error(), err)
	}
	if !result.Changed {
		return plan, nil
	}

	loaded, err := config.Decode(cfg.Vault, cfg.VaultOrigin, result.Patched)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest, "views: patched "+config.FileName+" is invalid", err)
	}
	plan.Views = loaded.Views
	plan.Config = &propertyConfigPlan{
		AbsPath:    configPath,
		SourceHash: vault.HashBytes(source),
		Data:       result.Patched,
		Diff:       result.Diff,
		Properties: loaded.Frontmatter.Properties,
	}
	return plan, nil
}

// checkViewResolves refuses to save a view that would land disabled. config
// validates a saved view's shape only — it is the bottom of the layering and
// cannot compile a query — so this is the one place the API can tell the user
// their expression is broken while they can still fix it.
func checkViewResolves(saved config.SavedView, cfg *config.Config, root string) error {
	resolved := queryexpr.ResolveView(saved, queryexpr.Env{Root: root, Frontmatter: &cfg.Frontmatter})
	if resolved.Err == nil {
		return nil
	}
	return noteops.WrapError(noteops.CodeInvalidRequest, "views: "+resolved.Err.Error(), resolved.Err)
}

func viewsResponseForPlan(s *Server, cfg *config.Config, plan *viewsPlan, views []config.SavedView, status ConfigStatus) viewsResponse {
	response := viewsResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     string(plan.Op),
		Name:       plan.Name,
		NewName:    plan.NewName,
		Views:      s.viewSummaries(cfg, views),
		Created:    plan.Created,
		ConfigDiff: []propertyConfigDiffLine{},
		Config:     status,
	}
	if plan.Config != nil {
		response.ConfigDiff = propertyConfigDiffLines(plan.Config.Diff)
	}
	return response
}

// viewSummaries resolves views against the declared properties in cfg. cfg is
// the configuration the frontmatter table is read from; views may be a projected
// list that does not belong to it yet, which is exactly what a preview renders.
func (s *Server) viewSummaries(cfg *config.Config, views []config.SavedView) []viewSummary {
	resolved := queryexpr.ResolveViews(views, queryexpr.Env{
		Root:        s.vault.Root,
		Frontmatter: &cfg.Frontmatter,
	})
	out := make([]viewSummary, 0, len(resolved))
	for _, view := range resolved {
		summary := viewSummary{
			Name:        view.Saved.Name,
			Description: view.Saved.Description,
			Query:       view.Saved.Query,
			Sort:        view.Saved.Sort,
			RelativeTo:  view.Saved.RelativeTo,
			Limit:       view.Saved.Limit,
			Enabled:     view.Enabled(),
			Detail:      view.Detail(),
		}
		if view.Plan != nil {
			summary.NeedsGraph = view.Plan.NeedsGraph()
			if view.Plan.Expr != nil {
				summary.Normalized = view.Plan.Expr.String()
			}
		}
		out = append(out, summary)
	}
	return out
}

func (s *Server) parseViewMutationRequest(r *http.Request) (viewMutationRequest, error) {
	var req viewMutationRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return viewMutationRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return viewMutationRequest{}, err
	}
	req.Token = ""
	req.Name = strings.TrimSpace(req.Name)
	req.NewName = strings.TrimSpace(req.NewName)
	if req.Name == "" {
		return viewMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "views: name is required")
	}

	switch req.Op {
	case viewOpUpsert:
		if req.View == nil {
			return viewMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "views: upsert requires a view")
		}
		normalized := req.View.savedView(req.Name)
		if normalized.Query == "" {
			return viewMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "views: query is required")
		}
		req.View = &viewDefinition{
			Query:       normalized.Query,
			Sort:        normalized.Sort,
			RelativeTo:  normalized.RelativeTo,
			Limit:       normalized.Limit,
			Description: normalized.Description,
		}
		req.NewName = ""
	case viewOpRename:
		if req.NewName == "" {
			return viewMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "views: rename requires new_name")
		}
		req.View = nil
	case viewOpDelete:
		req.NewName = ""
		req.View = nil
	default:
		return viewMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "views: unsupported op %q", string(req.Op))
	}
	return req, nil
}

func viewsCommand(op viewMutationOp) string {
	switch op {
	case viewOpRename:
		return viewsRenameCommand
	case viewOpDelete:
		return viewsDeleteCommand
	}
	return viewsUpsertCommand
}

func viewNotDeclaredError(name string) error {
	return noteops.Errorf(noteops.CodeNotFound, "views: %q is not declared in %s", name, config.FileName)
}

// viewsStaleConfigError is the one message every stale-preview path returns.
// brainnotes.toml has three independent writers — /api/properties/apply,
// /api/properties/schema/apply, and /api/views/apply — and they cannot corrupt
// each other, because configPin's ExpectHash makes the loser a 409. Naming the
// remedy is what keeps that correct outcome from reading as a generic failure.
func viewsStaleConfigError() error {
	return noteops.Errorf(noteops.CodePreviewStale,
		"views: %s changed, re-check the diff", config.FileName)
}

// viewsWriteError rewrites the losing side of that race into the same message,
// wherever it surfaced: the receipt's file digests, the replan, or the
// transaction precondition at the commit boundary.
func viewsWriteError(err error) error {
	switch noteops.CodeOf(err) {
	case noteops.CodePreviewStale, noteops.CodeConflict:
		return noteops.WrapError(noteops.CodePreviewStale,
			"views: "+config.FileName+" changed, re-check the diff", err)
	}
	return err
}
