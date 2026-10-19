package web

import (
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	templatesAPIRoutePath        = "/api/templates"
	templatesPreviewAPIRoutePath = "/api/templates/preview"
	templatesApplyAPIRoutePath   = "/api/templates/apply"

	// maxTemplateBytes bounds the content of one template file.
	//
	// It is far below noteops.MaxSourceBytes on purpose. A template is a skeleton
	// a note is rendered from, not a document, and the bound has to survive JSON
	// escaping inside propertyRequestBodyLimit: worst-case escaping is six bytes
	// per byte, so 128 KiB of content cannot overflow the 1 MiB body this surface
	// decodes. A larger allowance would make the refusal arrive as a truncated
	// body rather than as a message naming the limit.
	maxTemplateBytes = 128 << 10
)

// Journal command labels. History and undo read these, so they name the user's
// action rather than the internal endpoint.
const (
	templateSaveCommand   = "bn serve template save"
	templateDeleteCommand = "bn serve template delete"
)

// templateMutationOp names one change to a file in the templates directory.
type templateMutationOp string

const (
	templateOpSave   templateMutationOp = "save"
	templateOpDelete templateMutationOp = "delete"
)

// templateMutationRequest is one template-file edit. Like a saved view it is
// always previewed before it can be applied: see the templates write class in
// docs/internals/web-write-classes.md.
//
// Revision is the digest the edit was planned against, exactly as
// noteops.SourceDocument defines it — empty means "this file does not exist",
// which is how a create says so. It is required on a delete, because deleting a
// file whose contents nobody has seen is the one template action with no undo a
// person can reason about before taking it.
type templateMutationRequest struct {
	Token    string             `json:"token"`
	Op       templateMutationOp `json:"op"`
	Name     string             `json:"name"`
	Content  string             `json:"content"`
	Revision string             `json:"revision"`
}

// templateSummary is one file in the configured templates directory.
type templateSummary struct {
	Name     string `json:"name"`
	RelPath  string `json:"rel_path"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	// Content is filled only by a single-template read, so listing the directory
	// never serves every template's bytes.
	Content string `json:"content,omitempty"`
}

// templatesResponse is the shared shape of all three routes, in the shape
// viewsResponse established.
type templatesResponse struct {
	OK         bool   `json:"ok"`
	Generation int64  `json:"generation"`
	Action     string `json:"action"`
	Name       string `json:"name,omitempty"`
	RelPath    string `json:"rel_path,omitempty"`
	// Dir is the configured templates directory these names are relative to, so a
	// client can say where a file will land without re-deriving the default.
	Dir string `json:"dir"`
	// Extension is the configured default extension. It is what `bn new` appends
	// when it looks a template up, and it is one of the two suffixes a name may
	// carry here.
	Extension string            `json:"extension"`
	Templates []templateSummary `json:"templates"`
	// Created reports that a save introduces a file that does not exist yet.
	Created bool `json:"created"`
	// Removed reports that the plan deletes the file.
	Removed bool `json:"removed"`
	// Revision is the digest the file holds after the plan is applied, and empty
	// for a delete. It is what the next edit is planned against.
	Revision   string                   `json:"revision,omitempty"`
	Diff       []propertyConfigDiffLine `json:"diff"`
	PreviewID  string                   `json:"preview_id,omitempty"`
	ExpiresAt  string                   `json:"expires_at,omitempty"`
	JournalID  string                   `json:"journal_id,omitempty"`
	Config     ConfigStatus             `json:"config"`
	Templating templateDirStatus        `json:"templating"`
}

// templateDirStatus reports the two facts a person needs to trust this surface:
// a template file stays out of every note query, and it still travels to an
// enrolled peer. Both are derived from the configured directory rather than
// asserted, so a vault that configures an unusual one sees the truth about it.
type templateDirStatus struct {
	// ExcludedFromNotes is true while the configured directory is one vault.Walk
	// prunes, which is what keeps unrendered placeholders out of the note index.
	ExcludedFromNotes bool `json:"excluded_from_notes"`
	// Portable is true while a file under it is inside both the content history
	// and the synchronization scope, so editing one here is undoable and travels.
	Portable bool `json:"portable"`
}

// templatePlan is a fully resolved, not-yet-written change to one template file.
// It never touches brainnotes.toml: the configured directory decides where the
// file goes, and that is a read.
type templatePlan struct {
	Op      templateMutationOp
	Name    string
	RelPath string
	AbsPath string
	// Expect is the precondition the commit pins the file to — the bytes the plan
	// was built from, or absence for a create.
	Expect  vault.FileExpectation
	Created bool
	Removed bool
	Changed bool
	// Data is the content a save writes, nil for a delete.
	Data []byte
	// Revision is the digest of Data, empty for a delete.
	Revision string
	// SourceDigest is the digest the file held when the plan was built, used to
	// pin the preview to the exact bytes the diff was computed from.
	SourceDigest string
	Diff         []config.PatchLine
}

func (p *templatePlan) fileStates() []propertyPreviewFileState {
	if p == nil {
		return []propertyPreviewFileState{}
	}
	return []propertyPreviewFileState{{
		RelPath: p.RelPath,
		AbsPath: p.AbsPath,
		Digest:  p.SourceDigest,
	}}
}

// registerTemplateRoutes publishes the template-editing surface. Like the
// settings routes it is absent from the replica allowlist, so read-only refuses
// all three including the read.
func (srv *Server) registerTemplateRoutes(mux *http.ServeMux) {
	mux.HandleFunc(templatesAPIRoutePath, srv.handleTemplates)
	mux.HandleFunc(templatesPreviewAPIRoutePath, srv.handleTemplatesPreview)
	mux.HandleFunc(templatesApplyAPIRoutePath, srv.handleTemplatesApply)
}

// handleTemplates lists the configured templates directory, or serves one
// template's exact bytes when a name is given.
//
// The bytes are served verbatim and are never parsed. A template holds
// unrendered placeholders that are deliberately not valid YAML — that is the
// reason vault.Walk prunes this directory in the first place — so reading one
// through the note pipeline would fail on files that are perfectly correct.
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != templatesAPIRoutePath {
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
	response := templatesResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     "list",
		Dir:        cfg.Templates.Dir,
		Extension:  cfg.Templates.DefaultExtension,
		Diff:       []propertyConfigDiffLine{},
		Config:     status,
		Templating: templateDirStatusOf(cfg),
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		templates, err := listTemplates(s.vault.Root, cfg)
		if err != nil {
			writeManagementError(w, err)
			return
		}
		response.Templates = templates
		writeManagementJSON(w, http.StatusOK, response)
		return
	}

	summary, err := readTemplate(s.vault.Root, cfg, name)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	response.Action = "read"
	response.Name = summary.Name
	response.RelPath = summary.RelPath
	response.Revision = summary.Revision
	response.Templates = []templateSummary{summary}
	writeManagementJSON(w, http.StatusOK, response)
}

// handleTemplatesPreview plans one template change and issues the one-use
// receipt the apply endpoint redeems.
func (s *Server) handleTemplatesPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, templatesPreviewAPIRoutePath) {
		return
	}
	req, err := s.parseTemplateMutationRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	cfg, plan, status, _, err := s.planTemplateRequest(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	response, err := s.templatesResponseForPlan(cfg, plan, status)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if plan.Changed {
		receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
			Kind:       propertyPreviewTemplate,
			Generation: status.Generation,
			Template:   req,
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

// handleTemplatesApply redeems a template receipt and writes the file. It
// accepts no plan of its own: the stored request is replanned and must reproduce
// exactly the bytes the preview was pinned to.
func (s *Server) handleTemplatesApply(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, templatesApplyAPIRoutePath) {
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
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewTemplate)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPreviewFilesUnchanged(record.Files); err != nil {
		writeManagementError(w, templatesWriteError(err))
		return
	}

	cfg, plan, status, pin, err := s.replanForTemplateReceipt(record)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	entry, err := s.commitTemplatePlan(plan, pin, journalOptionsForOperation(req.OperationID)...)
	if err != nil {
		writeManagementError(w, templatesWriteError(err))
		return
	}
	// A template is not a note, so no snapshot derived from the notes is stale.
	// The templates directory is pruned by vault.Walk, which is exactly why this
	// write cannot have changed anything the cached snapshot holds.
	response, err := s.templatesResponseForPlan(cfg, plan, status)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	response.JournalID = managementJournalID(entry)
	writeManagementJSON(w, http.StatusOK, response)
}

// replanForTemplateReceipt rebuilds the plan the receipt was issued for and
// refuses it unless the vault still looks exactly as it did then.
//
// The three facts are the settings surface's three, read for a file: the
// generation says templates.dir has not moved the destination, `Changed` says
// the file does not already hold these bytes, and the file states — one entry,
// the template itself — say these are still the bytes the diff was computed
// against. The configuration layers are covered by the generation rather than by
// a digest here, and they are pinned again as preconditions at the commit.
func (s *Server) replanForTemplateReceipt(
	record propertyPreviewRecord,
) (*config.Config, *templatePlan, ConfigStatus, configPin, error) {
	cfg, plan, status, pin, err := s.planTemplateRequest(record.Template)
	if err != nil {
		return cfg, nil, status, pin, templatesWriteError(err)
	}
	if status.Generation != record.Generation || !plan.Changed ||
		!reflect.DeepEqual(plan.fileStates(), record.Files) {
		return cfg, nil, status, pin, templatesStalePreviewError(plan.RelPath)
	}
	return cfg, plan, status, pin, nil
}

// planTemplateRequest pins the configuration the plan was built from. The
// configuration is a read here rather than a write, but it decides *where* the
// file goes, so a commit under a different templates directory would put the
// bytes somewhere nobody previewed.
func (s *Server) planTemplateRequest(req templateMutationRequest) (*config.Config, *templatePlan, ConfigStatus, configPin, error) {
	cfg, status, pin := s.pinnedConfig()
	plan, err := planTemplateMutation(s.vault.Root, cfg, req)
	if err != nil {
		return cfg, nil, status, pin, err
	}
	return cfg, plan, status, pin, nil
}

func (s *Server) templatesResponseForPlan(cfg *config.Config, plan *templatePlan, status ConfigStatus) (templatesResponse, error) {
	templates, err := listTemplates(s.vault.Root, cfg)
	if err != nil {
		return templatesResponse{}, err
	}
	return templatesResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     string(plan.Op),
		Name:       plan.Name,
		RelPath:    plan.RelPath,
		Dir:        cfg.Templates.Dir,
		Extension:  cfg.Templates.DefaultExtension,
		Templates:  templates,
		Created:    plan.Created,
		Removed:    plan.Removed,
		Revision:   plan.Revision,
		Diff:       propertyConfigDiffLines(plan.Diff),
		Config:     status,
		Templating: templateDirStatusOf(cfg),
	}, nil
}

func (s *Server) parseTemplateMutationRequest(r *http.Request) (templateMutationRequest, error) {
	var req templateMutationRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return templateMutationRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return templateMutationRequest{}, err
	}
	req.Token = ""
	req.Name = strings.TrimSpace(req.Name)
	req.Revision = strings.TrimSpace(req.Revision)

	switch req.Op {
	case templateOpSave:
		if err := checkTemplateContent(req.Content); err != nil {
			return templateMutationRequest{}, err
		}
	case templateOpDelete:
		req.Content = ""
		if req.Revision == "" {
			return templateMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
				"templates: delete requires the revision of the file being removed")
		}
	default:
		return templateMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: unsupported op %q", string(req.Op))
	}
	return req, nil
}

// checkTemplateContent is the whole content policy, and it is deliberately two
// rules rather than noteops.ValidateSource's three.
//
// A template's frontmatter is *not* parsed. A template holds unrendered
// placeholders — `{{.Title}}` in a property value is the ordinary case — so a
// skeleton that is perfectly correct is routinely not valid YAML, which is the
// reason vault.Walk prunes this directory at all. Validating it the way a note is
// validated would refuse exactly the files this surface exists to edit.
func checkTemplateContent(content string) error {
	if len(content) > maxTemplateBytes {
		return noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: content exceeds the %d byte maximum", maxTemplateBytes)
	}
	if strings.IndexByte(content, 0) >= 0 {
		return noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: content holds a NUL byte, so it is not a text template")
	}
	return nil
}

func templateCommand(op templateMutationOp) string {
	if op == templateOpDelete {
		return templateDeleteCommand
	}
	return templateSaveCommand
}

func templatesStalePreviewError(relPath string) error {
	return noteops.Errorf(noteops.CodePreviewStale,
		"templates: %s changed, re-check the diff", relPath)
}

// templatesWriteError is the settings surface's rewrite, on the same terms: a
// transaction precondition that failed is this file having changed, and every
// other conflict — a busy safety lock, a revision the request planned against, a
// templates.dir this surface cannot write into, a rollback that left the file
// mixed, content preserved in a recovery file — already says what it is and
// what to do about it. See settingsWriteError for why the distinction is drawn
// on vault.IsTxnConflict rather than on the error code, and why a failed
// rollback or a needed recovery is asked about first.
func templatesWriteError(err error) error {
	if vault.IsTxnRollbackFailure(err) || vault.IsTxnRecoveryNeeded(err) {
		return err
	}
	if noteops.CodeOf(err) == noteops.CodePreviewStale || vault.IsTxnConflict(err) {
		return noteops.WrapError(noteops.CodePreviewStale,
			"templates: the template changed, re-check the diff", err)
	}
	return err
}
