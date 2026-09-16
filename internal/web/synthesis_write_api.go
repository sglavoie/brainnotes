package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	synthesisPreviewAPIRoutePath = "/api/synthesis/preview"
	synthesisApplyAPIRoutePath   = "/api/synthesis/apply"

	// synthesisPreviewBodyLimit bounds the encoded preview request. Form
	// encoding can turn every draft byte into three, so the transport allowance
	// is three times the draft limit plus room for the remaining fields; the
	// draft limit itself is enforced on the decoded value by noteops.
	synthesisPreviewBodyLimit = 3*noteops.MaxSynthesisDraftBytes + (32 << 10)
	// synthesisApplyBodyLimit bounds the apply request, which carries only a
	// receipt and a CSRF token.
	synthesisApplyBodyLimit = 4 << 10
)

type synthesisPreviewResponse struct {
	OK           bool   `json:"ok"`
	PreviewID    string `json:"preview_id"`
	ExpiresAt    string `json:"expires_at"`
	ContextID    string `json:"context_id"`
	Destination  string `json:"destination"`
	Markdown     string `json:"markdown"`
	RenderedHTML string `json:"rendered_html"`
}

// handleSynthesisPreview plans the note without writing it and hands back the
// one-use receipt apply redeems. The response Markdown is the plan's own bytes,
// so what the user reads here is what the receipt will create.
func (s *Server) handleSynthesisPreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireSynthesisPost(w, r, synthesisPreviewAPIRoutePath, synthesisPreviewBodyLimit) {
		return
	}

	record, err := s.synthesisContexts.lookup(r.Form.Get("context_id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.revalidateSynthesisContext(record); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, err := s.planSynthesisNote(record, r.Form.Get("title"), r.Form.Get("folder"), r.Form.Get("draft"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	rendered, err := s.renderSynthesisNote(plan.Content)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	stored, err := s.synthesisPreviews.store(synthesisPreviewRecord{
		ContextID:    record.ID,
		ConfigDigest: record.ConfigDigest,
		Plan:         plan,
	})
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, synthesisPreviewResponse{
		OK:           true,
		PreviewID:    stored.ID,
		ExpiresAt:    stored.ExpiresAt.Format(time.RFC3339),
		ContextID:    record.ID,
		Destination:  plan.Destination.RelPath,
		Markdown:     string(plan.Content),
		RenderedHTML: string(rendered),
	})
}

// handleSynthesisApply creates the previewed note. It accepts nothing but the
// receipt: the title, folder, draft, and sources all come from the stored plan,
// so no request can substitute content the user never reviewed.
func (s *Server) handleSynthesisApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireSynthesisPost(w, r, synthesisApplyAPIRoutePath, synthesisApplyBodyLimit) {
		return
	}

	record, err := s.synthesisPreviews.consume(r.Form.Get("preview_id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.verifySynthesisPreview(record); err != nil {
		writeManagementError(w, err)
		return
	}
	if s.beforeSynthesisCommit != nil {
		s.beforeSynthesisCommit()
	}

	result, err := noteops.ApplySynthesisNote(s.vault.Root, record.Plan)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	key := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(key)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		"Created note.",
		key,
		routePath,
		result.Destination.RelPath,
		managementJournalID(result.Journal),
	))
}

// planSynthesisNote turns one context packet plus the user's draft into the
// exact note bytes. Every note the packet describes becomes both an ordered
// source link and a pinned revision, so the created note cites what the draft
// was actually written against.
func (s *Server) planSynthesisNote(record synthesisContextRecord, title, folder, draft string) (*noteops.SynthesisPlan, error) {
	identities := record.notes()
	sources := make([]noteops.SynthesisSource, 0, len(identities))
	for _, identity := range identities {
		sources = append(sources, noteops.SynthesisSource{
			RelPath: identity.RelPath,
			Title:   identity.Title,
			SHA256:  identity.SHA256,
		})
	}
	return noteops.PlanSynthesisNote(s.vault.Root, s.config(), noteops.SynthesisRequest{
		Title:    strings.TrimSpace(title),
		Folder:   strings.TrimSpace(folder),
		Draft:    draft,
		Sources:  sources,
		Requires: record.ConfigRequires,
	})
}

// verifySynthesisPreview refuses a receipt whose world moved on, before
// anything is written. The transaction rechecks the same expectations at the
// commit boundary — that recheck is what makes the write safe — but failing
// here gives the browser a preview-stale answer instead of a bare conflict.
func (s *Server) verifySynthesisPreview(record synthesisPreviewRecord) error {
	digest, err := s.synthesisConfigDigest()
	if err != nil {
		return err
	}
	if digest != record.ConfigDigest {
		return noteops.Errorf(noteops.CodePreviewStale, "synthesis: this vault's configuration changed since the preview")
	}
	for _, requirement := range record.Plan.Requirements {
		if err := vault.VerifyState(requirement.Path, requirement.Expect); err != nil {
			return noteops.Errorf(noteops.CodePreviewStale,
				"synthesis: %s changed since the preview", s.vaultRelLabel(requirement.Path))
		}
	}
	if err := vault.VerifyState(record.Plan.Destination.AbsPath, record.Plan.Expect); err != nil {
		return noteops.Errorf(noteops.CodePreviewStale,
			"synthesis: %s exists since the preview", record.Plan.Destination.RelPath)
	}
	return nil
}

// renderSynthesisNote renders the note body for reading. The frontmatter is
// parsed off rather than rendered, because Markdown would show the delimiters
// as a rule and the title as prose.
func (s *Server) renderSynthesisNote(content []byte) (string, error) {
	doc, err := frontmatter.Parse(content)
	if err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "synthesis: parse planned note", err)
	}
	rendered, err := s.markdown.render(doc.Body)
	if err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "synthesis: render planned note", err)
	}
	return string(rendered), nil
}

// vaultRelLabel names an absolute path the way the rest of the vault does, so a
// refusal message never carries the server's filesystem layout.
func (s *Server) vaultRelLabel(absPath string) string {
	rel, err := filepath.Rel(s.vault.Root, absPath)
	if err != nil {
		return filepath.Base(absPath)
	}
	return filepath.ToSlash(rel)
}

// requireSynthesisPost is the shared prologue for every synthesis API route:
// exact path, POST only, a bounded body, a parsed form, and a matching CSRF
// token. Sharing it keeps one policy across the read-only pair and the two
// write routes.
func (s *Server) requireSynthesisPost(w http.ResponseWriter, r *http.Request, route string, bodyLimit int64) bool {
	return s.requireManagementPost(w, r, route, bodyLimit)
}
