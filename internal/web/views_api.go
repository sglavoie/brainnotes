package web

import (
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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
	Search       string `json:"search,omitempty"`
	FilterParams string `json:"filter_params,omitempty"`
	Query        string `json:"query"`
	Sort         string `json:"sort"`
	RelativeTo   string `json:"relative_to"`
	Limit        int    `json:"limit"`
	Description  string `json:"description"`
}

func (d *viewDefinition) savedView(name string) config.SavedView {
	if d == nil {
		return config.SavedView{Name: name}
	}
	return config.SavedView{
		Name:        name,
		Query:       strings.TrimSpace(d.Query),
		Search:      strings.TrimSpace(d.Search),
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
	Search      string `json:"search,omitempty"`
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
	return p.Config.fileStates()
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

	entry, err := s.commitPropertyPlan(&propertyPlan{Config: plan.Config}, "views", viewsCommand(plan.Op), pin)
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
