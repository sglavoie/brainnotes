package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
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

	entry, err := s.commitTemplatePlan(plan, pin)
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

// planTemplateMutation resolves the destination, reads what is there now, and
// renders the change as a line diff.
func planTemplateMutation(root string, cfg *config.Config, req templateMutationRequest) (*templatePlan, error) {
	dest, name, err := templateDestination(root, cfg, req.Name)
	if err != nil {
		return nil, err
	}
	current, exists, err := readTemplateBytes(dest.AbsPath)
	if err != nil {
		return nil, err
	}
	plan := &templatePlan{
		Op:      req.Op,
		Name:    name,
		RelPath: dest.RelPath,
		AbsPath: dest.AbsPath,
		Created: !exists,
		Expect:  vault.ExpectMissing(),
	}
	if exists {
		plan.SourceDigest = vault.HashBytes(current)
		plan.Expect = vault.ExpectHash(plan.SourceDigest)
	}
	if err := checkTemplateRevision(plan, req, exists); err != nil {
		return nil, err
	}

	if req.Op == templateOpDelete {
		plan.Removed = true
		plan.Changed = true
		plan.Diff = config.DiffText(current, nil)
		return plan, nil
	}

	data := []byte(req.Content)
	plan.Data = data
	plan.Revision = vault.HashBytes(data)
	plan.Changed = !exists || plan.Revision != plan.SourceDigest
	if plan.Changed {
		plan.Diff = config.DiffText(current, data)
	}
	return plan, nil
}

// checkTemplateRevision refuses an edit planned against bytes the file no longer
// holds, in the vocabulary noteops.ReadSource established: the empty revision is
// "this file does not exist", and any other value is the digest it held.
//
// A delete of a file that is already gone is refused rather than treated as a
// no-op, because a request naming a template says the user believes it is there,
// and reporting success would hide that somebody else removed it.
func checkTemplateRevision(plan *templatePlan, req templateMutationRequest, exists bool) error {
	revision := strings.TrimSpace(req.Revision)
	switch {
	case req.Op == templateOpDelete && !exists:
		return noteops.Errorf(noteops.CodeNotFound, "templates: %s does not exist", plan.RelPath)
	case !exists && revision != "":
		return templateRevisionError(plan.RelPath)
	case exists && revision != plan.SourceDigest:
		return templateRevisionError(plan.RelPath)
	}
	return nil
}

func templateRevisionError(relPath string) error {
	return noteops.Errorf(noteops.CodeConflict,
		"templates: %s changed since it was loaded, so nothing was written", relPath)
}

// commitTemplatePlan writes or removes the template in one journaled
// transaction, so the change is in the undo history and in the content history
// like every other vault mutation.
//
// Both configuration layers are pinned read-only. The plan wrote neither of
// them, but the destination was derived from templates.dir, so a configuration
// replaced between planning and committing would mean the file lands in a
// directory the preview never named.
func (s *Server) commitTemplatePlan(plan *templatePlan, pin configPin) (*journal.Entry, error) {
	if plan == nil || !plan.Changed {
		return nil, nil
	}
	entry, err := noteops.BeginOperation(
		s.vault.Root, "templates", templateCommand(plan.Op), []string{plan.AbsPath})
	if err != nil {
		return nil, err
	}
	defer entry.Release()
	txn, err := vault.NewFileTxn(s.vault.Root)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "templates: open transaction", err)
	}
	if err := stageTemplateChange(txn, plan); err != nil {
		return nil, err
	}
	if err := requireTemplateConfigPins(txn, pin); err != nil {
		return nil, err
	}

	if err := txn.Commit(entry); err != nil {
		return nil, classifyPropertyTxnError("templates: write", err)
	}
	return entry.Finalize()
}

// stageTemplateChange puts the planned write or removal into the transaction,
// each pinned to the expectation the plan was built on — the digest the bytes
// were read at, or "this file does not exist" for a create.
func stageTemplateChange(txn *vault.FileTxn, plan *templatePlan) error {
	if plan.Removed {
		if err := txn.Remove(plan.AbsPath, plan.Expect); err != nil {
			return classifyPropertyTxnError("templates: plan removal", err)
		}
		return nil
	}
	if err := txn.Write(plan.AbsPath, plan.Data, 0, plan.Expect); err != nil {
		return classifyPropertyTxnError("templates: plan write", err)
	}
	return nil
}

// requireTemplateConfigPins adds both configuration layers as read-only
// preconditions, including the machine-local overlay's absence: an overlay
// appearing between the plan and the commit could move templates.dir, and the
// file would land in a directory the preview never named.
func requireTemplateConfigPins(txn *vault.FileTxn, pin configPin) error {
	for _, layer := range pin.layers() {
		if err := txn.Require(layer.Path, layer.expectation()); err != nil {
			return noteops.WrapError(noteops.CodeInternal, "templates: pin "+layer.name(), err)
		}
	}
	return nil
}

// templateDestination is this surface's whole path policy, and it is deliberately
// its own rather than noteops.resolveSourceDestination's.
//
// A note destination is refused by vault.WalkExclusion precisely when it is
// inside the templates directory, because a note written there would exist and be
// invisible to every query. That refusal is correct and stays correct: the fix is
// not to weaken it for this surface but to state the narrower policy a template
// needs, which is a flat file directly inside the configured directory.
//
// Four refusals, and each of them is the reason a template editor can be trusted
// with a client-supplied name:
//
//   - The name is a bare filename. No separator, no NUL, no "." or "..", and no
//     leading dot — so it cannot climb out of the directory, cannot address a
//     nested path inside it, and cannot create a hidden file that vault.Walk and
//     template.Find would both then ignore.
//   - The extension is the configured default or ".md". template.Find looks a
//     template up by the configured extension, so a file with any other suffix is
//     one `bn new` could never resolve; writing it would be this surface
//     inventing a kind of file the vault has no use for.
//   - The resolved path is inside the vault and resolves to exactly where it was
//     asked for, so a symlink in the templates directory cannot redirect the
//     write to a target the policy never judged.
//   - The configured directory is itself checked, so a vault whose templates.dir
//     is a hidden or ignored path is refused here instead of silently writing
//     files outside the synchronization scope.
func templateDestination(root string, cfg *config.Config, name string) (noteops.PathResolution, string, error) {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	name, err = checkTemplateName(cfg, name)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	rel := dir + "/" + name
	dest, err := noteops.PathForRel(root, rel)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	if dest.RelPath != rel {
		return noteops.PathResolution{}, "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %s must not resolve through a symlink", rel)
	}
	return dest, name, nil
}

// checkTemplateName refuses every name that is not a flat file this vault could
// render a note from.
func checkTemplateName(cfg *config.Config, name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "templates: name is required")
	case strings.ContainsAny(name, `/\`):
		return "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %q must not contain path separators", name)
	case strings.ContainsRune(name, 0):
		return "", noteops.Errorf(noteops.CodeForbidden, "templates: name must not contain NUL bytes")
	case strings.HasPrefix(name, "."):
		// A hidden file is pruned by vault.Walk and skipped by template.Find, so it
		// would be a template nothing in the vault can use.
		return "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %q must not start with a dot", name)
	case name != path.Clean(name):
		return "", noteops.Errorf(noteops.CodeForbidden, "templates: %q is not a plain file name", name)
	case !templateExtensionAllowed(cfg, name):
		return "", noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: %q must end in %s", name, templateExtensionList(cfg))
	}
	return name, nil
}

// templateExtensionAllowed reports whether name carries a suffix `bn new` could
// resolve. ".md" is accepted beside the configured extension because a vault that
// configures something else still holds the Markdown templates it had before.
func templateExtensionAllowed(cfg *config.Config, name string) bool {
	ext := path.Ext(name)
	if ext == "" || ext == name {
		return false
	}
	for _, allowed := range templateExtensions(cfg) {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}
	return false
}

func templateExtensions(cfg *config.Config) []string {
	configured := ".md"
	if cfg != nil && strings.TrimSpace(cfg.Templates.DefaultExtension) != "" {
		configured = cfg.Templates.DefaultExtension
	}
	if strings.EqualFold(configured, ".md") {
		return []string{".md"}
	}
	return []string{configured, ".md"}
}

func templateExtensionList(cfg *config.Config) string {
	return strings.Join(templateExtensions(cfg), " or ")
}

// templateDirRel returns the configured templates directory as a clean
// vault-relative slash path, refusing one this surface must not write into.
//
// config.applyDefaults already guarantees it is relative, cleaned and non-empty,
// so what is left to check is the pair of properties a template file depends on:
// it must not be hidden or ignored, because vault.Walk would prune it — which is
// what the caller *wants* for a template — while vaultgit and vaultsync would
// judge a hidden component out of scope, which would leave the file with no undo
// history and no way to reach a peer.
func templateDirRel(cfg *config.Config) (string, error) {
	dir := ""
	if cfg != nil {
		dir = strings.Trim(strings.TrimSpace(filepath.ToSlash(cfg.Templates.Dir)), "/")
	}
	if dir == "" {
		dir = "Templates"
	}
	if dir != path.Clean(dir) || noteops.RelEscapesRoot(dir) {
		return "", templateDirUnusableError(cfg, "it is not a vault-relative directory")
	}
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, ".") {
			return "", templateDirUnusableError(cfg, "a hidden path is outside the undo and synchronization scopes")
		}
	}
	return dir, nil
}

func templateDirUnusableError(cfg *config.Config, reason string) error {
	dir := ""
	if cfg != nil {
		dir = cfg.Templates.Dir
	}
	return noteops.Errorf(noteops.CodeConflict,
		"templates: this vault's templates.dir (%q) cannot be edited here because %s", dir, reason)
}

// templateDirStatusOf answers the two questions this surface is accountable for,
// by asking the same predicates the rest of the vault does.
func templateDirStatusOf(cfg *config.Config) templateDirStatus {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return templateDirStatus{}
	}
	probe := dir + "/probe.md"
	_, excluded := vault.WalkExclusion(cfg, probe)
	return templateDirStatus{
		ExcludedFromNotes: excluded,
		Portable:          templatePathTravels(probe),
	}
}

// templatePathTravels reports whether a file at rel is covered by both the
// content history and the synchronization scope — undoable here, and portable to
// an enrolled peer.
//
// It asks the two packages that own those policies rather than restating them.
// They are independent — vaultgit keeps an undo journal this machine alone
// reads, vaultsync decides what may leave it — and a template has to be in both
// for "edited, undone, and synchronized" to be true of it.
func templatePathTravels(rel string) bool {
	return vaultgit.InScope(rel) && vaultsync.Portable(rel)
}

// listTemplates lists the files in the configured directory that this surface
// can edit. A directory that does not exist yet is an empty list rather than an
// error: the first save creates it.
func listTemplates(root string, cfg *config.Config) ([]templateSummary, error) {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		if os.IsNotExist(err) {
			return []templateSummary{}, nil
		}
		return nil, noteops.WrapError(noteops.CodeInternal, "templates: read "+dir, err)
	}

	out := make([]templateSummary, 0, len(entries))
	for _, entry := range entries {
		if summary, listed := templateRow(root, dir, cfg, entry); listed {
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// templateRow describes one directory entry as a row, or reports that it is not
// one.
//
// Three entries are skipped rather than refused, because the listing describes
// what this surface can edit and none of them can be: a name the policy resolves
// to something other than itself — every read and write here addresses the
// resolved name, so such a row's size and revision would describe somebody
// else's bytes — a symlink, and a directory.
func templateRow(root, dir string, cfg *config.Config, entry os.DirEntry) (templateSummary, bool) {
	name := entry.Name()
	if resolved, err := checkTemplateName(cfg, name); err != nil || resolved != name {
		return templateSummary{}, false
	}
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() {
		return templateSummary{}, false
	}
	summary := templateSummary{Name: name, RelPath: dir + "/" + name, Size: info.Size()}
	if data, exists, err := readTemplateBytes(filepath.Join(root, filepath.FromSlash(summary.RelPath))); err == nil && exists {
		summary.Revision = vault.HashBytes(data)
	}
	return summary, true
}

// readTemplate serves one template's exact bytes and the revision an edit has to
// echo back.
func readTemplate(root string, cfg *config.Config, name string) (templateSummary, error) {
	dest, resolved, err := templateDestination(root, cfg, name)
	if err != nil {
		return templateSummary{}, err
	}
	data, exists, err := readTemplateBytes(dest.AbsPath)
	if err != nil {
		return templateSummary{}, err
	}
	if !exists {
		return templateSummary{}, noteops.Errorf(noteops.CodeNotFound, "templates: %s does not exist", dest.RelPath)
	}
	return templateSummary{
		Name:     resolved,
		RelPath:  dest.RelPath,
		Size:     int64(len(data)),
		Revision: vault.HashBytes(data),
		Content:  string(data),
	}, nil
}

// readTemplateBytes reads a template verbatim, reporting absence rather than
// failing on it. Anything that is not a regular file is refused: a directory or a
// symlink under the templates directory is not something this surface may edit,
// and treating one as content would hand back bytes from wherever it points.
func readTemplateBytes(absPath string) ([]byte, bool, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, noteops.WrapError(noteops.CodeInternal, "templates: stat template", err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, noteops.Errorf(noteops.CodeForbidden, "templates: %s is not a regular file", filepath.Base(absPath))
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, false, noteops.WrapError(noteops.CodeInternal, "templates: read template", err)
	}
	if len(data) > maxTemplateBytes {
		return nil, false, noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: %s is larger than the %d byte maximum this API serves", filepath.Base(absPath), maxTemplateBytes)
	}
	return data, true, nil
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
// mixed — already says what it is and what to do about it. See
// settingsWriteError for why the distinction is drawn on vault.IsTxnConflict
// rather than on the error code, and why a failed rollback is asked about first.
func templatesWriteError(err error) error {
	if vault.IsTxnRollbackFailure(err) {
		return err
	}
	if noteops.CodeOf(err) == noteops.CodePreviewStale || vault.IsTxnConflict(err) {
		return noteops.WrapError(noteops.CodePreviewStale,
			"templates: the template changed, re-check the diff", err)
	}
	return err
}
