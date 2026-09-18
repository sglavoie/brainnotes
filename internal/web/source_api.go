package web

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/attachments"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const (
	sourceAPIRoutePath        = "/api/source"
	sourcePreviewAPIRoutePath = "/api/source/preview"
	sourceSaveAPIRoutePath    = "/api/source/save"
	sourceCreateAPIRoutePath  = "/api/source/create"

	// sourceRequestBodyLimit bounds a request carrying whole-note Markdown. Form
	// encoding can turn every source byte into three, so the transport allowance
	// is three times the content limit plus room for the revision, the save id
	// and the CSRF token; noteops enforces the content limit itself on the
	// decoded value.
	sourceRequestBodyLimit = 3*noteops.MaxSourceBytes + (32 << 10)
)

// sourceDocumentResponse is a source read: the note's exact bytes and the
// revision a save has to echo back.
type sourceDocumentResponse struct {
	OK       bool   `json:"ok"`
	Key      string `json:"key"`
	Path     string `json:"path"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
}

// sourcePreviewResponse is a rendered preview of unsaved bytes.
type sourcePreviewResponse struct {
	OK           bool   `json:"ok"`
	RenderedHTML string `json:"rendered_html"`
}

// sourceSaveResponse is the answer to a save or a create that left the
// submitted bytes on disk.
//
// Revision is the field a client cannot do without: it is what the next edit is
// planned against, and it is present on a write, on a replay and on a converged
// no-op alike. Changed and Replayed are what let an editor word the outcome
// honestly instead of claiming a write it may not have performed.
type sourceSaveResponse struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	Key      string `json:"key"`
	Path     string `json:"path"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Changed  bool   `json:"changed"`
	Created  bool   `json:"created"`
	Replayed bool   `json:"replayed"`
	// Attachments names every referenced upload that is in the vault now, so the
	// editor can stop tracking them. It is present on a replay too, for the same
	// reason Revision is: the answer has to describe the vault, not this call.
	Attachments []noteops.SavedAttachment `json:"attachments,omitempty"`
	JournalID   string                    `json:"journal_id,omitempty"`
}

// sourceSaveErrorResponse is the management error envelope plus the two fields a
// failed save has to carry so the user's text is not the thing that gets lost.
type sourceSaveErrorResponse struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Revision is the note's current revision on a stale-revision refusal, and
	// the new one on a snapshot failure.
	Revision string `json:"revision,omitempty"`
	// Content is the note's current source on a stale-revision refusal, so the
	// editor can show a merge instead of telling somebody to copy their draft
	// somewhere and reload.
	Content string `json:"content,omitempty"`
	// Changed reports whether the submitted bytes are on disk despite the error.
	// It is true only for a snapshot failure.
	Changed bool `json:"changed"`
}

// registerSourceRoutes publishes the raw-Markdown surface.
//
// All four are registered unconditionally and gated by requestGuard, like every
// other note mutation route. That means read-only refuses all four with
// `read-only replica`, including the read: a replica's POST allowlist does not
// name them and its read allowlist does not either. That is deliberate rather
// than an omission — the source read exists to seed an editor, and a route that
// hands out revisions for saves the replica cannot accept is an editing
// affordance on a published copy. It stays unavailable until somebody reviews
// it, which is exactly what the allowlist is for.
func (srv *Server) registerSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc(sourceAPIRoutePath, srv.handleSource)
	mux.HandleFunc(sourcePreviewAPIRoutePath, srv.handleSourcePreview)
	mux.HandleFunc(sourceSaveAPIRoutePath, srv.handleSourceSave)
	mux.HandleFunc(sourceCreateAPIRoutePath, srv.handleSourceCreate)
}

// handleSource serves one note's Markdown exactly as it is stored, with the
// revision those bytes hash to.
func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != sourceAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed,
			noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	doc, key, err := s.readSourceForKey(strings.TrimSpace(r.URL.Query().Get("key")))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	routePath, err := notePathForKey(key)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, sourceDocumentResponse{
		OK:       true,
		Key:      key,
		Path:     routePath,
		RelPath:  doc.Destination.RelPath,
		Revision: doc.Revision,
		Content:  doc.Content,
	})
}

// handleSourcePreview renders unsaved Markdown without writing anything.
//
// It validates the submission the way a save does, so the editor learns about a
// broken properties block from the preview it is already running rather than
// from the save it was about to make.
func (s *Server) handleSourcePreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, sourcePreviewAPIRoutePath, sourceRequestBodyLimit) {
		return
	}
	content := r.Form.Get("content")
	if err := noteops.ValidateSource(content); err != nil {
		writeManagementError(w, err)
		return
	}
	rendered, err := s.renderSourcePreview(content)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeManagementJSON(w, http.StatusOK, sourcePreviewResponse{OK: true, RenderedHTML: string(rendered)})
}

// handleSourceSave replaces an existing note's whole source.
func (s *Server) handleSourceSave(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, sourceSaveAPIRoutePath, sourceRequestBodyLimit) {
		return
	}
	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	uploads, err := s.resolveSaveAttachments(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.applySourceSave(w, noteops.SaveSourceRequest{
		RelPath:               source.RelPath,
		Content:               r.Form.Get("content"),
		BaseRevision:          strings.TrimSpace(r.Form.Get("revision")),
		SaveID:                strings.TrimSpace(r.Form.Get("save_id")),
		Attachments:           uploads.commits,
		UnresolvedAttachments: uploads.unresolved,
	}, uploads.staged)
}

// handleSourceCreate writes a note that does not exist yet.
//
// The path comes from the request rather than from a route key, because there is
// no note to resolve one from yet — so this is the one field on this surface that
// names a location nothing has vetted. noteops is where it is vetted: it
// contains it, de-symlinks it, refuses anything that is not a Markdown note, and
// refuses every location the vault walk would prune, because a note written
// there would be on disk and in no query.
func (s *Server) handleSourceCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, sourceCreateAPIRoutePath, sourceRequestBodyLimit) {
		return
	}
	uploads, err := s.resolveSaveAttachments(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.applySourceSave(w, noteops.SaveSourceRequest{
		RelPath:               strings.TrimSpace(r.Form.Get("rel_path")),
		Content:               r.Form.Get("content"),
		SaveID:                strings.TrimSpace(r.Form.Get("save_id")),
		Create:                true,
		Attachments:           uploads.commits,
		UnresolvedAttachments: uploads.unresolved,
	}, uploads.staged)
}

// applySourceSave is the body both write routes share.
//
// The configuration is republished and pinned before the save is planned, so the
// frontmatter this request submitted is judged against the declarations that are
// actually in force and the transaction refuses the save if another revision of
// either configuration layer replaces it before the commit.
func (s *Server) applySourceSave(w http.ResponseWriter, req noteops.SaveSourceRequest, staged []attachments.Staged) {
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}
	cfg, _, pin := s.pinnedConfig()
	opts := append(sourceConfigPinOptions(pin), noteops.WithSourceCommand("bn web source save"))

	// The configuration and the pin come from one call on purpose: the same bytes
	// that decide whether the destination is a note location at all are the bytes
	// the transaction verifies at the commit boundary.
	result, err := noteops.SaveSource(s.vault.Root, cfg, req, opts...)
	// A non-nil result means the bytes are on disk, whether or not err is nil, so
	// the cached snapshot is stale either way.
	if result != nil {
		s.state.invalidate()
		// The staged copies are redundant the moment the transaction commits, and
		// that is true of a snapshot failure too: the bytes are in the vault, the
		// save just is not in the history. Keeping staging alive there would leave
		// files a client has already been told about.
		s.forgetCommittedAttachments(staged, result)
	}
	if err != nil {
		s.writeSourceSaveError(w, req, result, err)
		return
	}
	writeSourceSaveResult(w, result)
}

func writeSourceSaveResult(w http.ResponseWriter, result *noteops.SaveSourceResult) {
	key := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(key)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, sourceSaveResponse{
		OK:          true,
		Message:     sourceSaveMessage(result),
		Key:         key,
		Path:        routePath,
		RelPath:     result.Destination.RelPath,
		Revision:    result.Revision,
		Changed:     result.Changed,
		Created:     result.Created,
		Replayed:    result.Replayed,
		Attachments: result.Attachments,
		JournalID:   managementJournalID(result.Journal),
	})
}

// sourceSaveMessage words the outcome without claiming a write that did not
// happen. A replay and a converged no-op are both successes, and saying "Saved"
// for either would be the sentence that hides a duplicate submission.
func sourceSaveMessage(result *noteops.SaveSourceResult) string {
	// Replayed is tested before Created because a replayed create carries both: the
	// record says the note was created, and this call is the resubmission that did
	// not create it. Answering "Created" there is the same lie as "Saved".
	switch {
	case result.Replayed:
		return result.Destination.RelPath + " was already saved with this save id."
	case result.Created:
		return "Created " + result.Destination.RelPath + "."
	case !result.Changed:
		return result.Destination.RelPath + " already holds this content."
	default:
		return "Saved " + result.Destination.RelPath + "."
	}
}

// writeSourceSaveError answers a failed save with everything the editor needs so
// that the user's text is not what gets lost.
//
// Two failures get more than a message. A stale-revision refusal carries the
// note's current bytes and revision, so the editor can offer a merge — and a
// create whose destination is already taken is one of those, because "it already
// exists" without the bytes in the way leaves nothing to merge against either. A
// snapshot failure carries the new revision and Changed true: the save landed,
// the history does not cover it, and a client told only "500" would resubmit
// against a revision its own save replaced.
func (s *Server) writeSourceSaveError(w http.ResponseWriter, req noteops.SaveSourceRequest, result *noteops.SaveSourceResult, err error) {
	payload := sourceSaveErrorResponse{
		Code:    string(noteops.CodeOf(err)),
		Message: noteops.MessageOf(err),
	}
	switch {
	case result != nil:
		payload.Revision = result.Revision
		payload.Changed = result.Changed
	case noteops.IsSourceRevisionConflict(err):
		if current, readErr := noteops.ReadSource(s.vault.Root, s.config(), req.RelPath); readErr == nil && current.Exists {
			payload.Revision = current.Revision
			payload.Content = current.Content
		}
	}
	writeManagementJSON(w, noteops.HTTPStatusForError(err), payload)
}

// readSourceForKey resolves a route key to an existing note and reads it.
func (s *Server) readSourceForKey(key string) (*noteops.SourceDocument, string, error) {
	if key == "" {
		return nil, "", noteops.Errorf(noteops.CodeInvalidRequest, "source: note key is required")
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return nil, "", noteops.WrapError(noteops.CodeInternal, "source: load vault snapshot", err)
	}
	_, _, resolved, err := noteops.ResolveExistingRouteNote(s.vault.Root, snapshot.notes, snapshot.graph, key)
	if err != nil {
		return nil, "", err
	}
	doc, err := noteops.ReadSource(s.vault.Root, s.config(), resolved.RelPath)
	if err != nil {
		return nil, "", err
	}
	if !doc.Exists {
		return nil, "", noteops.Errorf(noteops.CodeNotFound, "source: %s no longer exists", resolved.RelPath)
	}
	return doc, managementRouteKeyForRelPath(resolved.RelPath), nil
}

// renderSourcePreview renders submitted Markdown through the same Goldmark
// pipeline, the same wikilink resolver and the same graph a note page uses, so a
// preview resolves and breaks links exactly as the saved note will.
//
// The frontmatter is parsed off rather than rendered, for the reason
// renderSynthesisNote parses it off: Markdown would show the delimiters as a
// horizontal rule and the properties as prose.
func (s *Server) renderSourcePreview(content string) (template.HTML, error) {
	doc, err := frontmatter.Parse([]byte(content))
	if err != nil {
		return "", noteops.WrapError(noteops.CodeInvalidRequest,
			"source: the note's frontmatter is not valid YAML", err)
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "source: load vault snapshot", err)
	}
	rendered, err := s.markdown.renderWithGraph(doc.Body, snapshot.graph)
	if err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "source: render preview", err)
	}
	return rendered, nil
}
