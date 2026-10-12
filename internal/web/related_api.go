package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	relatedAPIRoutePath     = "/api/related"
	relatedItemAPIRoutePath = "/api/related/item"
	defaultRelatedAPILimit  = 5
	maxRelatedAPILimit      = 20
)

type relatedNoteIdentity struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Path  string `json:"path"`
	Route string `json:"route"`
}

type relatedReasonResponse struct {
	Kind   recall.ReasonKind `json:"kind"`
	Detail string            `json:"detail"`
}

type relatedResultResponse struct {
	Candidate    relatedNoteIdentity      `json:"candidate"`
	Excerpt      string                   `json:"excerpt"`
	Relationship recall.RelationshipClass `json:"relationship"`
	Bridge       *relatedNoteIdentity     `json:"bridge,omitempty"`
	Reasons      []relatedReasonResponse  `json:"reasons"`
	Useful       bool                     `json:"useful"`
}

type relatedFeedbackResponse struct {
	Candidate relatedNoteIdentity  `json:"candidate"`
	State     recall.FeedbackState `json:"state"`
	Until     string               `json:"until,omitempty"`
}

type relatedResponse struct {
	ServerDate string                    `json:"server_date"`
	Source     relatedNoteIdentity       `json:"source"`
	Mode       recall.Mode               `json:"mode"`
	HasMore    bool                      `json:"has_more"`
	Results    []relatedResultResponse   `json:"results"`
	Feedback   []relatedFeedbackResponse `json:"feedback"`
}

type relatedItemResponse struct {
	OK        bool                 `json:"ok"`
	Action    string               `json:"action"`
	Source    relatedNoteIdentity  `json:"source"`
	Candidate relatedNoteIdentity  `json:"candidate"`
	State     recall.FeedbackState `json:"state,omitempty"`
	Until     string               `json:"until,omitempty"`
	Opened    bool                 `json:"opened,omitempty"`
	// Destination is the browser route an Open answered with when this mode does
	// not start editors. Empty means a process was started, exactly as it does
	// for the Brief's Open.
	Destination string `json:"destination,omitempty"`
	Recorded    *bool  `json:"recorded,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

type relatedItemRequest struct {
	action        string
	requestNow    time.Time
	sourcePath    string
	candidatePath string
	source        relatedNoteIdentity
	candidate     relatedNoteIdentity
	stateStore    *recall.StateStore
}

type relatedResponseBuilder struct {
	snapshot  vaultSnapshot
	documents map[string]recall.Document
	notes     map[string]*vault.Note
}

func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != relatedAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	mode, limit, err := relatedQueryOptions(r)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid related query", err))
		return
	}
	sourceKey := strings.TrimSpace(r.URL.Query().Get("source"))
	if sourceKey == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "related source note not found or stale"))
		return
	}

	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "reload related configuration", err))
		return
	}
	cfg, status := s.configState.publication()
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "load related snapshot", err))
		return
	}
	sourceNote, _, ok := noteForRoute(snapshot, sourceKey)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "related source note not found or stale"))
		return
	}

	corpus, err := s.state.relatedCorpusFor(snapshot, cfg.Related.MetadataProperties, status.Generation)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build related corpus", err))
		return
	}
	requestNow := s.briefNow()
	stateStore := recall.NewStateStore(
		s.vault.Root,
		recall.WithClock(func() time.Time { return requestNow }),
		recall.WithNotePaths(corpus.notePaths),
	)
	if err := stateStore.Validate(); err != nil {
		writeRelatedStateError(w, err)
		return
	}

	sourcePath := noteVaultPath(s.vault.Root, sourceNote)
	sourceDocument, ok := documentByPath(corpus.documents, sourcePath)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "related source note not found or stale"))
		return
	}

	ranker := corpus.ranker.WithOptions(recall.RankOptions{
		Now:    func() time.Time { return requestNow },
		Recall: stateStore,
	})
	results, err := ranker.Rank(sourceDocument.Path, mode)
	if err != nil {
		// After mode and source validation, Rank can fail only while reading the
		// injected recall signal source. Keep that failure in the 409 boundary.
		writeRelatedStateError(w, err)
		return
	}
	feedback, err := stateStore.ActiveFeedbackForSource(sourceDocument.Path)
	if err != nil {
		writeRelatedStateError(w, err)
		return
	}

	builder := newRelatedResponseBuilder(s.vault.Root, snapshot, corpus.documents)
	sourceIdentity, err := builder.identity(sourceDocument.Path)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "map related source", err))
		return
	}
	feedbackResponse, activeByPath, err := builder.feedback(feedback)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "map related feedback", err))
		return
	}
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	resultResponse, err := builder.results(results, activeByPath)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "map related results", err))
		return
	}

	writeManagementJSON(w, http.StatusOK, relatedResponse{
		ServerDate: requestNow.Format(time.DateOnly),
		Source:     sourceIdentity,
		Mode:       mode,
		HasMore:    hasMore,
		Results:    resultResponse,
		Feedback:   feedbackResponse,
	})
}

func (s *Server) handleRelatedItem(w http.ResponseWriter, r *http.Request) {
	request, ok := s.relatedItemRequest(w, r)
	if !ok {
		return
	}

	response := relatedItemResponse{
		OK:        true,
		Action:    request.action,
		Source:    request.source,
		Candidate: request.candidate,
	}
	switch request.action {
	case "view":
		if err := request.stateStore.RecordOpened(request.sourcePath, request.candidatePath); err != nil {
			writeRelatedStateMutationError(w, err)
			return
		}
		response.Recorded = boolPointer(true)
	case "open_editor":
		if !s.applyRelatedOpen(w, request, &response) {
			return
		}
	case "useful", "dismissed", "clear":
		feedback := recall.FeedbackState(request.action)
		if err := request.stateStore.SetFeedback(request.sourcePath, request.candidatePath, feedback, time.Time{}); err != nil {
			writeRelatedStateMutationError(w, err)
			return
		}
		response.State = feedback
	case "later":
		if !applyRelatedLater(w, r, request, &response) {
			return
		}
	}

	writeManagementJSON(w, http.StatusOK, response)
}

// applyRelatedLater is the one feedback state with a deadline to validate. It
// reports whether the caller still owes a response, like applyRelatedOpen.
func applyRelatedLater(w http.ResponseWriter, r *http.Request, request relatedItemRequest, response *relatedItemResponse) bool {
	until, err := relatedLaterDeadline(r.Form.Get("until"), request.requestNow)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid later date", err))
		return false
	}
	if err := request.stateStore.SetFeedback(request.sourcePath, request.candidatePath, recall.FeedbackLater, until); err != nil {
		writeRelatedStateMutationError(w, err)
		return false
	}
	response.State = recall.FeedbackLater
	response.Until = until.Format(time.DateOnly)
	return true
}

// applyRelatedOpen is the Related panel's Open: the third Open action in the
// server, and it goes through the same door the Brief's does — a mode without
// processLaunch answers with the route that displays the note rather than
// refusing an action whose whole point is to put the reader in front of it.
//
// It reports whether the caller still owes a response. A recall-history failure
// is answered here, because the note is reachable either way and that makes it
// a warning rather than an error.
func (s *Server) applyRelatedOpen(w http.ResponseWriter, request relatedItemRequest, response *relatedItemResponse) bool {
	candidatePath := filepath.Join(s.vault.Root, filepath.FromSlash(request.candidatePath))
	destination, err := s.openNoteTarget(candidatePath, request.candidatePath)
	if err != nil {
		writeBriefOpenError(w, err)
		return false
	}
	response.Destination = destination
	response.Opened = true
	if err := request.stateStore.RecordOpened(request.sourcePath, request.candidatePath); err != nil {
		response.Recorded = boolPointer(false)
		// The warning names only what failed. It is deliberately free of any claim
		// about an editor or a page, because the same response goes to a client
		// that just started one and to a client that is about to navigate, and
		// each composes its own sentence around this one.
		response.Warning = "Recall history could not be recorded."
		writeManagementJSON(w, http.StatusOK, *response)
		return false
	}
	response.Recorded = boolPointer(true)
	return true
}

func (s *Server) relatedItemRequest(w http.ResponseWriter, r *http.Request) (relatedItemRequest, bool) {
	if r.URL.Path != relatedItemAPIRoutePath {
		http.NotFound(w, r)
		return relatedItemRequest{}, false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return relatedItemRequest{}, false
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return relatedItemRequest{}, false
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return relatedItemRequest{}, false
	}

	action := strings.ToLower(strings.TrimSpace(r.Form.Get("action")))
	switch action {
	case "view", "open_editor", "useful", "dismissed", "later", "clear":
	default:
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported related action %q", action))
		return relatedItemRequest{}, false
	}
	sourceKey := strings.TrimSpace(r.Form.Get("source"))
	candidateKey := strings.TrimSpace(r.Form.Get("candidate"))
	if sourceKey == "" || candidateKey == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "related source and candidate keys are required"))
		return relatedItemRequest{}, false
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "load related snapshot", err))
		return relatedItemRequest{}, false
	}
	sourceNote, _, sourceOK := noteForRoute(snapshot, sourceKey)
	candidateNote, _, candidateOK := noteForRoute(snapshot, candidateKey)
	if !sourceOK || !candidateOK {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "related source or candidate note not found or stale"))
		return relatedItemRequest{}, false
	}
	sourcePath := noteVaultPath(s.vault.Root, sourceNote)
	candidatePath := noteVaultPath(s.vault.Root, candidateNote)
	if sourcePath == "" || candidatePath == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "related source or candidate note not found or stale"))
		return relatedItemRequest{}, false
	}
	if sourcePath == candidatePath {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "related source and candidate must be different notes"))
		return relatedItemRequest{}, false
	}

	requestNow := s.briefNow()
	stateStore := recall.NewStateStore(
		s.vault.Root,
		recall.WithClock(func() time.Time { return requestNow }),
	)
	if err := stateStore.Validate(); err != nil {
		writeRelatedStateError(w, err)
		return relatedItemRequest{}, false
	}

	builder := newRelatedResponseBuilder(s.vault.Root, snapshot, nil)
	builder.documents[sourcePath] = recall.Document{Path: sourcePath, Title: sourceNote.Title()}
	builder.documents[candidatePath] = recall.Document{Path: candidatePath, Title: candidateNote.Title()}
	source, err := builder.identity(sourcePath)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "map related source", err))
		return relatedItemRequest{}, false
	}
	candidate, err := builder.identity(candidatePath)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "map related candidate", err))
		return relatedItemRequest{}, false
	}
	return relatedItemRequest{
		action:        action,
		requestNow:    requestNow,
		sourcePath:    sourcePath,
		candidatePath: candidatePath,
		source:        source,
		candidate:     candidate,
		stateStore:    stateStore,
	}, true
}

func relatedLaterDeadline(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("until is required")
	}
	until, err := time.ParseInLocation(time.DateOnly, value, now.Location())
	if err != nil || until.Format(time.DateOnly) != value {
		return time.Time{}, fmt.Errorf("until must be a calendar date in YYYY-MM-DD form")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !until.After(today) {
		return time.Time{}, fmt.Errorf("until must be after the server date %s", today.Format(time.DateOnly))
	}
	return until, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func relatedQueryOptions(r *http.Request) (recall.Mode, int, error) {
	mode := recall.ModeBalanced
	if values, present := r.URL.Query()["mode"]; present {
		value := ""
		if len(values) > 0 {
			value = strings.TrimSpace(values[0])
		}
		parsed, err := recall.ParseMode(value)
		if err != nil {
			return "", 0, err
		}
		mode = parsed
	}

	limit := defaultRelatedAPILimit
	if values, present := r.URL.Query()["limit"]; present {
		value := ""
		if len(values) > 0 {
			value = strings.TrimSpace(values[0])
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maxRelatedAPILimit {
			return "", 0, fmt.Errorf("limit must be an integer from 1 through %d", maxRelatedAPILimit)
		}
		limit = parsed
	}
	return mode, limit, nil
}
