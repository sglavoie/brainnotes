package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func newRelatedResponseBuilder(root string, snapshot vaultSnapshot, documents []recall.Document) relatedResponseBuilder {
	builder := relatedResponseBuilder{
		snapshot:  snapshot,
		documents: make(map[string]recall.Document, len(documents)),
		notes:     make(map[string]*vault.Note, len(snapshot.notes)),
	}
	for _, document := range documents {
		builder.documents[document.Path] = document
	}
	for _, note := range snapshot.notes {
		if path := noteVaultPath(root, note); path != "" {
			builder.notes[path] = note
		}
	}
	return builder
}

func (builder relatedResponseBuilder) identity(path string) (relatedNoteIdentity, error) {
	document, ok := builder.documents[path]
	if !ok {
		return relatedNoteIdentity{}, fmt.Errorf("document %q is unavailable", path)
	}
	note := builder.notes[path]
	if note == nil || builder.snapshot.graph == nil {
		return relatedNoteIdentity{}, fmt.Errorf("note %q is unavailable in the current graph", path)
	}
	key := builder.snapshot.graph.KeyForNote(note)
	if key == "" {
		return relatedNoteIdentity{}, fmt.Errorf("note %q has no route key", path)
	}
	route, err := notePathForKey(key)
	if err != nil {
		return relatedNoteIdentity{}, err
	}
	return relatedNoteIdentity{Key: key, Title: document.Title, Path: document.Path, Route: route}, nil
}

func (builder relatedResponseBuilder) feedback(entries []recall.ActiveFeedback) ([]relatedFeedbackResponse, map[string]recall.FeedbackState, error) {
	response := make([]relatedFeedbackResponse, 0, len(entries))
	activeByPath := make(map[string]recall.FeedbackState, len(entries))
	for _, entry := range entries {
		// State enumeration intentionally knows only the on-disk vault. Mapping
		// through this snapshot drops ignored and quarantined notes.
		if _, ok := builder.documents[entry.CandidateRelPath]; !ok {
			continue
		}
		candidate, err := builder.identity(entry.CandidateRelPath)
		if err != nil {
			return nil, nil, err
		}
		item := relatedFeedbackResponse{Candidate: candidate, State: entry.State}
		if entry.State == recall.FeedbackLater {
			item.Until = entry.Until.Format(time.DateOnly)
		}
		response = append(response, item)
		activeByPath[entry.CandidateRelPath] = entry.State
	}
	return response, activeByPath, nil
}

func (builder relatedResponseBuilder) results(results []recall.Result, activeByPath map[string]recall.FeedbackState) ([]relatedResultResponse, error) {
	response := make([]relatedResultResponse, 0, len(results))
	for _, result := range results {
		candidate, err := builder.identity(result.Path)
		if err != nil {
			return nil, err
		}
		document := builder.documents[result.Path]
		item := relatedResultResponse{
			Candidate:    candidate,
			Excerpt:      brief.RepresentativeExcerpt(document),
			Relationship: result.Relationship,
			Reasons:      make([]relatedReasonResponse, 0, min(3, len(result.Reasons))),
			Useful:       activeByPath[result.Path] == recall.FeedbackUseful,
		}
		if result.BridgePath != "" {
			bridge, err := builder.identity(result.BridgePath)
			if err != nil {
				return nil, err
			}
			item.Bridge = &bridge
		}
		for index, reason := range result.Reasons {
			if index == 3 {
				break
			}
			item.Reasons = append(item.Reasons, relatedReasonResponse{Kind: reason.Kind, Detail: reason.Detail})
		}
		response = append(response, item)
	}
	return response, nil
}

func writeRelatedStateError(w http.ResponseWriter, err error) {
	writeManagementErrorStatus(w, http.StatusConflict, noteops.WrapError(noteops.CodeConflict, "recall state is unreadable", err))
}

func writeRelatedStateMutationError(w http.ResponseWriter, err error) {
	writeManagementErrorStatus(w, http.StatusConflict, noteops.WrapError(noteops.CodeConflict, "recall state update failed", err))
}
