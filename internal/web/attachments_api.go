package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/attachments"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const (
	attachmentUploadAPIRoutePath = "/api/attachments/upload"
	attachmentCancelAPIRoutePath = "/api/attachments/cancel"

	// attachmentUploadBodyLimit is the transport allowance for one upload.
	//
	// It is the *ceiling* on what any vault may configure, not the limit that is
	// enforced: [attachments].max_file_bytes is configurable downward from
	// config.MaxAttachmentFileBytes and never upward, and the store applies
	// whatever this vault settled on while it receives the bytes. Two bounds in
	// series is the point — the transport refuses a body no configuration could
	// accept before a single byte is written, and the store refuses one this
	// vault does not accept while writing to a private file it then deletes.
	//
	// The body is the file, raw, so the allowance is the ceiling plus nothing:
	// there is no form encoding to pay for, which is exactly why this route
	// streams instead of posting a form.
	attachmentUploadBodyLimit = config.MaxAttachmentFileBytes

	// attachmentCancelBodyLimit bounds a form carrying an owner, an upload id and
	// a CSRF token, none of which is longer than a line.
	attachmentCancelBodyLimit = 8 << 10
)

// attachmentUploadResponse describes a staged upload.
//
// RelPath is the destination staging reserved, which is what the editor writes
// into the note's link text — so the link is correct before the save that makes
// it true. The upload id is the only handle to the bytes themselves; nothing in
// this payload can be turned into a request for staged content, because staging
// is not served at all.
type attachmentUploadResponse struct {
	OK       bool   `json:"ok"`
	UploadID string `json:"upload_id"`
	Filename string `json:"filename"`
	RelPath  string `json:"rel_path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type attachmentCancelResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// registerAttachmentRoutes publishes the staging surface.
//
// Both routes are registered unconditionally and gated by requestGuard like
// every other mutation. Read-only refuses both — its POST allowlist names
// neither — which is the right default twice over: a replica accepts no writes,
// and staging that no save could ever commit is disk somebody else pays for.
func (srv *Server) registerAttachmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc(attachmentUploadAPIRoutePath, srv.handleAttachmentUpload)
	mux.HandleFunc(attachmentCancelAPIRoutePath, srv.handleAttachmentCancel)
}

// attachmentStore builds the staging store from the configuration in force.
//
// It is built per request rather than held on the Server because both numbers it
// needs are configuration: a vault that changes [attachments].dir or its size
// limit and reloads must have the next upload obey the new value, and a store
// captured at startup would quietly keep filing uploads by the old rule.
func (s *Server) attachmentStore() (*attachments.Store, error) {
	cfg := s.config()
	store, err := attachments.NewStore(s.vault.Root, cfg.Attachments.Dir, cfg.Attachments.MaxFileBytes)
	if err != nil {
		return nil, attachmentError("attachments: open staging", err)
	}
	return store, nil
}

// sweepAbandonedUploads collects staging nobody is going to claim, and reports
// nothing.
//
// The failure is deliberately dropped rather than returned. A sweep is
// housekeeping over files that belong to *other* requests, so an artifact it
// cannot remove — a leftover from a server that ran as another user, a blob a
// second process still holds open — says nothing about the upload in hand.
// Returning it would refuse that upload, and since this is the only place a
// sweep runs on the request path, the first un-removable file would refuse every
// upload the vault ever receives again: the one collector, latched shut by its
// own failure. Store.Sweep already treats a concurrent sweeper as Skipped rather
// than as an error, so what is left here is genuinely only the case nobody in
// this request can act on.
func sweepAbandonedUploads(store *attachments.Store) {
	_, _ = store.Sweep(attachments.DefaultTTL, time.Time{})
}

// sweepStagedUploadsAtStartup collects what the previous run left behind.
//
// The upload route sweeps too, but that is the only other caller, and it is the
// wrong one to rely on alone: the person whose browser died mid-upload is
// precisely the person who may never upload again, and until somebody does their
// staging sits there — outside vaultgit.InScope and outside vault.Walk, so no bn
// command reports it. Running once per server start bounds the arrears at one
// session instead of at forever.
//
// A read-only server does not run it. Sweeping is a write, and this profile's
// whole contract is that serving a vault never changes it.
func (s *Server) sweepStagedUploadsAtStartup() {
	if !s.caps.documentWrites {
		return
	}
	store, err := s.attachmentStore()
	if err != nil {
		return
	}
	sweepAbandonedUploads(store)
}

// attachmentError translates the store's sentinels into this API's vocabulary.
//
// ErrNotFound covers both "no such upload" and "not yours", deliberately: the
// store does not distinguish them and neither may this, or the difference
// between the two answers would be a way to ask whether an id somebody else
// holds exists.
//
// A *http.MaxBytesError is classified alongside the store's own ErrTooLarge
// because the two are one refusal reached by two routes: the transport bound
// trips first when a vault configured its limit at the ceiling, the store's
// trips first when it configured one below. Leaving the transport's to the
// default arm would answer "your file is too large" with a 500 for exactly the
// vaults that changed nothing.
func attachmentError(what string, err error) error {
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &tooLarge):
		return noteops.WrapError(noteops.CodeInvalidRequest, what, err)
	case errors.Is(err, attachments.ErrTooLarge),
		errors.Is(err, attachments.ErrInvalidFilename),
		errors.Is(err, attachments.ErrInvalidOwner),
		errors.Is(err, attachments.ErrInvalidID):
		return noteops.WrapError(noteops.CodeInvalidRequest, what, err)
	case errors.Is(err, attachments.ErrNotFound):
		return noteops.WrapError(noteops.CodeNotFound, what, err)
	default:
		return noteops.WrapError(noteops.CodeInternal, what, err)
	}
}

// handleAttachmentUpload receives one file into private staging.
//
// The body is the file and nothing else, and the metadata rides in the query
// string, because a multipart form would mean buffering or re-spooling megabytes
// this process has no reason to hold. That is also why this route cannot use
// requireManagementPost: ParseForm would consume the body it is trying to
// stream. The CSRF token therefore has to come from the X-BN-CSRF header, which
// is the form requestCSRFToken already prefers and the only one available to a
// request whose body is not a form.
func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireStreamedAttachmentPost(w, r) {
		return
	}
	store, err := s.attachmentStore()
	if err != nil {
		writeManagementError(w, err)
		return
	}
	sweepAbandonedUploads(store)
	query := r.URL.Query()
	staged, err := store.Stage(attachments.StageRequest{
		Owner:    query.Get("owner"),
		Filename: query.Get("name"),
	}, r.Body, time.Time{})
	if err != nil {
		writeManagementError(w, attachmentError("attachments: receive upload", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, attachmentUploadResponse{
		OK:       true,
		UploadID: staged.ID,
		Filename: staged.Filename,
		RelPath:  staged.RelPath,
		Size:     staged.Size,
		SHA256:   staged.SHA256,
	})
}

// requireStreamedAttachmentPost is requireManagementPost for a request whose
// body must not be parsed: the exact route, POST only, a bounded body, and a
// CSRF token from the header.
func (s *Server) requireStreamedAttachmentPost(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != attachmentUploadAPIRoutePath {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed,
			noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachmentUploadBodyLimit)
	if !csrfTokenMatches(s.csrfToken, strings.TrimSpace(r.Header.Get(csrfHeaderName))) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return false
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return false
	}
	return true
}

// handleAttachmentCancel drops a staged upload the editor no longer wants.
//
// A cancel is a courtesy rather than a guarantee: an upload nobody cancels is
// collected by the sweep, and one that is already gone answers the same way a
// successful cancel does, because "it is not in staging" is what the caller
// asked for either way.
func (s *Server) handleAttachmentCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, attachmentCancelAPIRoutePath, attachmentCancelBodyLimit) {
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}
	store, err := s.attachmentStore()
	if err != nil {
		writeManagementError(w, err)
		return
	}
	owner := r.Form.Get("owner")
	uploadID := strings.TrimSpace(r.Form.Get("upload_id"))
	if err := store.Discard(owner, uploadID); err != nil && !errors.Is(err, attachments.ErrNotFound) {
		writeManagementError(w, attachmentError("attachments: cancel upload", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, attachmentCancelResponse{
		OK:      true,
		Message: "Upload discarded.",
	})
}

// saveAttachments is what a save's `attachment` fields resolved to: the staged
// uploads that are there, the commits noteops plans from them, and the ids that
// are not there at all.
type saveAttachments struct {
	staged     []attachments.Staged
	commits    []noteops.AttachmentCommit
	unresolved []string
}

// resolveSaveAttachments turns the upload ids a save names into the commits
// noteops needs.
//
// The owner is checked here by the store, before anything is planned: an id is
// unguessable, but an id that leaked is still only usable by the draft it was
// staged for. An id the store has no upload for — swept, cancelled, never
// staged, or belonging to another draft — is *carried* rather than refused here,
// because the save whose staging is gone is very often the save that already
// succeeded: the first attempt committed the attachment and dropped the staged
// copy, and the retry that never saw the answer names the same ids again.
// noteops refuses it after the replay, which is the only place that can tell the
// two apart.
func (s *Server) resolveSaveAttachments(r *http.Request) (saveAttachments, error) {
	ids := r.Form["attachment"]
	if len(ids) == 0 {
		return saveAttachments{}, nil
	}
	store, err := s.attachmentStore()
	if err != nil {
		return saveAttachments{}, err
	}
	owner := r.Form.Get("owner")
	resolved := saveAttachments{
		staged:  make([]attachments.Staged, 0, len(ids)),
		commits: make([]noteops.AttachmentCommit, 0, len(ids)),
	}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		item, err := store.Lookup(owner, id)
		switch {
		case errors.Is(err, attachments.ErrNotFound), errors.Is(err, attachments.ErrInvalidID):
			resolved.unresolved = append(resolved.unresolved, id)
			continue
		case err != nil:
			return saveAttachments{}, attachmentError("attachments: resolve upload", err)
		}
		resolved.staged = append(resolved.staged, item)
		resolved.commits = append(resolved.commits, noteops.AttachmentCommit{
			UploadID:   item.ID,
			RelPath:    item.RelPath,
			SourcePath: item.BlobPath,
			Size:       item.Size,
			Digest:     item.SHA256,
		})
	}
	return resolved, nil
}

// forgetCommittedAttachments drops the staged copies of uploads that are now in
// the vault.
//
// Only the ones the *result* names are dropped, not every id the request
// carried. The two lists are normally the same, but a replayed save answers from
// a record written by an earlier call, and an upload this request named that the
// replay does not account for has not been committed by anything — discarding it
// on the strength of somebody else's outcome would delete bytes no note holds.
//
// Its failures are not reported, which is the whole difference between this and
// a cancel: the bytes are in the vault, the save is a success, and a staging
// file left behind is disk the sweep collects rather than an error worth telling
// somebody about.
func (s *Server) forgetCommittedAttachments(staged []attachments.Staged, result *noteops.SaveSourceResult) {
	if len(staged) == 0 || result == nil || len(result.Attachments) == 0 {
		return
	}
	committed := make(map[string]bool, len(result.Attachments))
	for _, saved := range result.Attachments {
		committed[saved.UploadID] = true
	}
	done := make([]attachments.Staged, 0, len(staged))
	for _, item := range staged {
		if committed[item.ID] {
			done = append(done, item)
		}
	}
	store, err := s.attachmentStore()
	if err != nil {
		return
	}
	store.Forget(done)
}
