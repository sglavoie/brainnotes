package web

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/threads"
)

type threadItemView struct {
	threads.Item
	Key     string `json:"key"`
	Title   string `json:"title"`
	Path    string `json:"path"`
	Warning string `json:"warning"`
}

type threadPrepared struct {
	OK      bool             `json:"ok"`
	Content string           `json:"content"`
	Items   []threadItemView `json:"items"`
}

// Preparing a collection edit never writes. The browser reviews the resulting
// Markdown and saves it through the ordinary revision-pinned source API.
func (s *Server) handleThreadPrepare(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, threadPrepareAPI, sourceRequestBodyLimit) {
		return
	}
	content := r.Form.Get("content")
	if err := noteops.ValidateSource(content); err != nil {
		writeManagementError(w, err)
		return
	}
	doc, err := threads.Parse(content)
	if err != nil {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "%s", err))
		return
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "load thread sources", err))
		return
	}
	if err := s.prepareThreadAction(r, &doc, snapshot); err != nil {
		writeManagementError(w, err)
		return
	}
	content = doc.String()
	if err := noteops.ValidateSource(content); err != nil {
		writeManagementError(w, err)
		return
	}
	writeManagementJSON(w, http.StatusOK, threadPrepared{true, content, threadItemViews(doc, snapshot)})
}

func (s *Server) prepareThreadAction(r *http.Request, doc *threads.Document, snapshot vaultSnapshot) error {
	switch r.Form.Get("action") {
	case "inspect":
		return nil
	case "add":
		return s.addThreadItem(r, doc, snapshot)
	}
	index, err := strconv.Atoi(r.Form.Get("index"))
	if err != nil {
		return noteops.Errorf(noteops.CodeInvalidRequest, "a passage position is required")
	}
	if err := changeThreadItem(r, doc, index); err != nil {
		return noteops.Errorf(noteops.CodeInvalidRequest, "%s", err)
	}
	return nil
}

func changeThreadItem(r *http.Request, doc *threads.Document, index int) error {
	switch r.Form.Get("action") {
	case "up":
		return doc.Move(index, -1)
	case "down":
		return doc.Move(index, 1)
	case "remove":
		return doc.Remove(index)
	case "group":
		return doc.Group(index, r.Form.Get("group"))
	case "comment":
		if utf8.RuneCountInString(r.Form.Get("commentary")) > 8000 {
			return noteops.Errorf(noteops.CodeInvalidRequest, "commentary is limited to 8,000 characters")
		}
		return doc.Comment(index, r.Form.Get("commentary"))
	default:
		return noteops.Errorf(noteops.CodeInvalidRequest, "unknown thread action")
	}
}

func (s *Server) addThreadItem(r *http.Request, doc *threads.Document, snapshot vaultSnapshot) error {
	note, _, ok := noteForRoute(snapshot, r.Form.Get("source"))
	if !ok {
		return noteops.Errorf(noteops.CodeNotFound, "the source note is no longer available")
	}
	quote, commentary := r.Form.Get("quote"), r.Form.Get("commentary")
	if utf8.RuneCountInString(quote) > 8000 || utf8.RuneCountInString(commentary) > 8000 {
		return noteops.Errorf(noteops.CodeInvalidRequest, "quotation and commentary are each limited to 8,000 characters")
	}
	// Pin provenance to what the reader actually saw. The reader supplies its
	// source revision; if unavailable, an empty revision gets a conservative notice.
	revision := r.Form.Get("source_revision")
	if revision == "" {
		revision = strings.Repeat("0", 64)
	}
	item := threads.Item{Group: r.Form.Get("group"), Link: "[[" + wikilinkTargetForNote(s.vault.Root, note) + "]]", Quote: quote, Commentary: commentary, Revision: revision}
	if err := doc.Add(item); err != nil {
		return noteops.Errorf(noteops.CodeInvalidRequest, "%s", err)
	}
	return nil
}

func threadItemViews(doc threads.Document, snapshot vaultSnapshot) []threadItemView {
	items := make([]threadItemView, 0, len(doc.Items))
	for _, item := range doc.Items {
		view := threadItemView{Item: item, Title: item.Link, Warning: "Source is missing or ambiguous. The collected quotation is kept."}
		if note, key, ok := threadSource(snapshot, item.Link); ok {
			view.Key, view.Title = key, note.Title()
			view.Path, _ = notePathForKey(key)
			view.Warning = ""
			if item.Quote != "" && item.Revision != note.SourceHash() {
				view.Warning = "Source changed or its original revision is unknown. Check the quotation against the source."
			}
		}
		items = append(items, view)
	}
	return items
}
