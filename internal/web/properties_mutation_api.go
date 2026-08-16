package web

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// propertyMutationResponse is the shared shape of every property write surface.
// A preview fills PreviewID and ExpiresAt; an apply fills JournalID instead, so
// a client reads one contract either way.
type propertyMutationResponse struct {
	OK         bool                        `json:"ok"`
	Generation int64                       `json:"generation"`
	Action     string                      `json:"action"`
	Property   string                      `json:"property"`
	NewName    string                      `json:"new_name,omitempty"`
	Selected   int                         `json:"selected"`
	Affected   int                         `json:"affected"`
	Previews   []noteops.NotePreview       `json:"previews"`
	Blockers   []propertyBlocker           `json:"blockers"`
	ConfigDiff []propertyConfigDiffLine    `json:"config_diff"`
	Projected  propertyViolationProjection `json:"projected_violations"`
	PreviewID  string                      `json:"preview_id,omitempty"`
	ExpiresAt  string                      `json:"expires_at,omitempty"`
	JournalID  string                      `json:"journal_id,omitempty"`
	Config     ConfigStatus                `json:"config"`
}

// handlePropertyValue performs one immediate single-note edit. Only the two
// safe operations reach it: replacing a value the browser already rendered and
// adding one element to a list. The expected note hash makes the edit a
// no-op-or-conflict when the note moved on.
func (s *Server) handlePropertyValue(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, propertyValueAPIRoutePath) {
		return
	}
	req, err := s.parsePropertyValueRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "properties: snapshot failed", err))
		return
	}
	note, ok := snapshotNotesByRelPath(s.vault.Root, snapshot)[req.Note]
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "properties: %s is not in the vault", req.Note))
		return
	}

	cfg, _, pin := s.pinnedConfig()
	planned, err := planPropertyValueEdit(note.Path, req.Note, req, &cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	plan := &propertyPlan{
		Action:   propertyActionSet,
		Property: req.Property,
		Selected: 1,
	}
	if planned != nil {
		plan.Notes = []propertyPlanNote{*planned}
	}

	command := propertyListAddCommand
	if req.Op == propertyValueOpReplace {
		command = propertySetCommand
	}
	entry, err := s.commitPropertyPlan(plan, command, pin)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.finishPropertyWrite(len(plan.Notes) > 0)

	status := s.configState.Status()
	writeManagementJSON(w, http.StatusOK, propertyMutationResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     string(req.Op),
		Property:   req.Property,
		Selected:   1,
		Affected:   len(plan.Notes),
		Previews:   plan.previews(),
		Blockers:   []propertyBlocker{},
		ConfigDiff: []propertyConfigDiffLine{},
		JournalID:  managementJournalID(entry),
		Config:     status,
	})
}

// planPropertyValueEdit reads the note, enforces the expected hash, and returns
// its planned rewrite, or nil when the edit is already satisfied.
func planPropertyValueEdit(absPath, relPath string, req propertyValueRequest, fm *config.FrontmatterConfig) (*propertyPlanNote, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "properties: read note", err)
	}
	sourceHash := vault.HashBytes(data)
	if !vaultHashMatches(sourceHash, req.ExpectedHash) {
		return nil, noteops.Errorf(noteops.CodeConflict, "properties: %s changed since it was loaded", relPath)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest, "properties: parse note", err)
	}

	front := &doc.Front
	def, defined := fm.Definition(req.Property)
	before, present := front.Get(req.Property)

	var change noteops.PreviewChange
	switch req.Op {
	case propertyValueOpReplace:
		if !present {
			return nil, noteops.Errorf(noteops.CodeConflict, "properties: %s does not have a %s property", relPath, req.Property)
		}
		decoded, err := properties.ValidateTypedValue(req.Property, *req.Value, def, defined)
		if err != nil {
			return nil, err
		}
		if !properties.WriteTyped(front, req.Property, *req.Value, decoded) {
			return nil, nil
		}
		change = noteops.FrontmatterChange(req.Property, true, before, true, decoded)

	case propertyValueOpListAdd:
		item, err := properties.ValidateTypedListItem(req.Property, *req.Value, def, defined)
		if err != nil {
			return nil, err
		}
		added, err := front.ListAdd(req.Property, item)
		if err != nil {
			if errors.Is(err, frontmatter.ErrNotAList) {
				return nil, noteops.Errorf(noteops.CodeConflict, "properties: %s is not a list in %s", req.Property, relPath)
			}
			return nil, noteops.WrapError(noteops.CodeInternal, "properties: add list item", err)
		}
		if !added {
			return nil, nil
		}
		list, _ := front.GetList(req.Property)
		properties.RecordListItemTimestampSpelling(front, req.Property, len(list)-1, req.Value.Kind)
		after, _ := front.Get(req.Property)
		change = noteops.FrontmatterChange(req.Property, present, before, true, after)
	}

	next, err := frontmatter.Marshal(doc, fm)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "properties: marshal note", err)
	}
	return &propertyPlanNote{
		RelPath:    relPath,
		AbsPath:    absPath,
		SourceHash: sourceHash,
		Data:       next,
		Front:      front,
		Preview:    noteops.NotePreview{RelPath: relPath, Changes: []noteops.PreviewChange{change}},
	}, nil
}

// handlePropertiesPreview plans a multi-note property change and, when nothing
// blocks it, issues the one-use receipt that apply redeems.
func (s *Server) handlePropertiesPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, propertyPreviewAPIRoutePath) {
		return
	}
	req, err := s.parsePropertyMutationRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, status, _, err := s.planPropertyMutationRequest(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	response := propertyMutationResponseForPlan(plan, status)
	if !plan.blocked() {
		receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
			Kind:       propertyPreviewNotes,
			Generation: status.Generation,
			Mutation:   req,
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

// handlePropertiesApply redeems a receipt. It accepts no plan of its own: the
// stored request is replanned and must reproduce exactly the files the preview
// was pinned to, so an apply can never write what the preview did not show.
func (s *Server) handlePropertiesApply(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, propertyApplyAPIRoutePath) {
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
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewNotes)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPreviewFilesUnchanged(record.Files); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, status, pin, err := s.planPropertyMutationRequest(record.Mutation)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPlanMatchesReceipt(plan, record, status.Generation); err != nil {
		writeManagementError(w, err)
		return
	}

	entry, err := s.commitPropertyPlan(plan, propertyMutationCommand(plan.Action), pin)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.finishPropertyWrite(true)

	status = s.configState.Status()
	response := propertyMutationResponseForPlan(plan, status)
	response.JournalID = managementJournalID(entry)
	writeManagementJSON(w, http.StatusOK, response)
}

// planPropertyMutationRequest refreshes the vault view and plans req against it.
// The returned pin names the configuration the plan used, so the commit can
// verify it is still the one on disk.
func (s *Server) planPropertyMutationRequest(req propertyMutationRequest) (*propertyPlan, ConfigStatus, configPin, error) {
	cfg, status, pin := s.pinnedConfig()
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return nil, status, pin, noteops.WrapError(noteops.CodeInternal, "properties: snapshot failed", err)
	}
	plan, err := planPropertyMutation(s.vault.Root, snapshot, cfg, s.configState.sourcePath, req)
	if err != nil {
		return nil, status, pin, err
	}
	return plan, status, pin, nil
}

// propertyPlanMatchesReceipt refuses an apply whose world moved on: a new
// configuration generation, a different set of files, or a plan that has since
// become blocked.
func propertyPlanMatchesReceipt(plan *propertyPlan, record propertyPreviewRecord, generation int64) error {
	if generation != record.Generation {
		return noteops.Errorf(noteops.CodePreviewStale, "properties: %s changed since the preview", config.FileName)
	}
	if plan.blocked() {
		return noteops.Errorf(noteops.CodeConflict, "properties: %s", propertyBlockerSummary(plan.Blockers))
	}
	if !reflect.DeepEqual(plan.fileStates(), record.Files) {
		return noteops.Errorf(noteops.CodePreviewStale, "properties: the vault changed since the preview")
	}
	return nil
}

func propertyMutationResponseForPlan(plan *propertyPlan, status ConfigStatus) propertyMutationResponse {
	response := propertyMutationResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     string(plan.Action),
		Property:   plan.Property,
		NewName:    plan.NewName,
		Selected:   plan.Selected,
		Affected:   len(plan.Notes),
		Previews:   plan.previews(),
		Blockers:   sortedPropertyBlockers(plan.Blockers),
		ConfigDiff: []propertyConfigDiffLine{},
		Projected:  plan.Projection,
		Config:     status,
	}
	if plan.Config != nil {
		response.ConfigDiff = propertyConfigDiffLines(plan.Config.Diff)
	}
	return response
}

// finishPropertyWrite republishes the configuration and drops the cached vault
// view so the next read reflects what was just written.
func (s *Server) finishPropertyWrite(changed bool) {
	if !changed {
		return
	}
	_ = s.reloadConfig()
	s.state.invalidate()
}

func vaultHashMatches(actual, expected string) bool {
	return vault.ExpectHash(expected).SHA256 == actual
}
