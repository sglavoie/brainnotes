package web

import (
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// propertySchemaPlan is a definition-only change to brainnotes.toml. It never
// carries note rewrites: repairing the notes a new definition now flags is a
// separate action the user launches after reviewing its own preview.
type propertySchemaPlan struct {
	Op         propertySchemaOp
	Property   string
	Config     *propertyConfigPlan
	Affected   int
	Projection propertyViolationProjection
}

func (p *propertySchemaPlan) fileStates() []propertyPreviewFileState {
	if p == nil || p.Config == nil {
		return []propertyPreviewFileState{}
	}
	return []propertyPreviewFileState{{
		RelPath: config.FileName,
		AbsPath: p.Config.AbsPath,
		Digest:  p.Config.SourceHash,
	}}
}

// handlePropertySchemaPreview plans a definition change and issues the one-use
// receipt that the schema apply endpoint redeems.
func (s *Server) handlePropertySchemaPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, propertySchemaPreviewAPIRoutePath) {
		return
	}
	req, err := s.parsePropertySchemaRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, status, _, err := s.planPropertySchemaRequest(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	response := propertySchemaResponseForPlan(plan, status)
	if plan.Config != nil {
		receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
			Kind:       propertyPreviewSchema,
			Generation: status.Generation,
			Schema:     req,
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

// handlePropertySchemaApply redeems a schema receipt and writes brainnotes.toml.
func (s *Server) handlePropertySchemaApply(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, propertySchemaApplyAPIRoutePath) {
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
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewSchema)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPreviewFilesUnchanged(record.Files); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, status, pin, err := s.planPropertySchemaRequest(record.Schema)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if status.Generation != record.Generation {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewStale, "properties: %s changed since the preview", config.FileName))
		return
	}
	if plan.Config == nil {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewStale, "properties: %s changed since the preview", config.FileName))
		return
	}
	if !reflect.DeepEqual(plan.fileStates(), record.Files) {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewStale, "properties: %s changed since the preview", config.FileName))
		return
	}

	entry, err := s.commitPropertyPlan(&propertyPlan{Config: plan.Config}, propertySchemaCommand(plan.Op), pin)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.finishPropertyWrite(true)

	status = s.configState.Status()
	response := propertySchemaResponseForPlan(plan, status)
	response.JournalID = managementJournalID(entry)
	writeManagementJSON(w, http.StatusOK, response)
}

func (s *Server) planPropertySchemaRequest(req propertySchemaRequest) (*propertySchemaPlan, ConfigStatus, configPin, error) {
	cfg, status, pin := s.pinnedConfig()
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return nil, status, pin, noteops.WrapError(noteops.CodeInternal, "properties: snapshot failed", err)
	}
	plan, err := planPropertySchema(s.vault.Root, snapshot, cfg, s.configState.sourcePath, req)
	if err != nil {
		return nil, status, pin, err
	}
	return plan, status, pin, nil
}

// planPropertySchema patches the configuration in memory, re-decodes it so the
// projected violations are measured against definitions the loader accepted,
// and counts the notes the change would govern.
func planPropertySchema(root string, snapshot vaultSnapshot, cfg *config.Config, configPath string, req propertySchemaRequest) (*propertySchemaPlan, error) {
	if !noteops.ValidFrontmatterKey(req.Property) {
		return nil, noteops.Errorf(noteops.CodeInvalidKey, "properties: invalid property name %q (allowed: letters, digits, '_', '-')", req.Property)
	}
	_, declared := cfg.Frontmatter.Definition(req.Property)
	if req.Op == propertySchemaOpDelete && !declared {
		return nil, noteops.Errorf(noteops.CodeNotFound, "properties: %s is not declared in %s", req.Property, config.FileName)
	}

	source, err := os.ReadFile(configPath)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "properties: read "+config.FileName, err)
	}

	patch := config.PropertyPatch{Key: req.Property}
	switch req.Op {
	case propertySchemaOpUpsert:
		patch.Op = config.PatchUpsert
		patch.Definition = req.Definition.definition()
	case propertySchemaOpDelete:
		patch.Op = config.PatchDelete
	}

	result, err := config.PatchProperties(source, []config.PropertyPatch{patch})
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest, err.Error(), err)
	}

	plan := &propertySchemaPlan{
		Op:       req.Op,
		Property: req.Property,
		Affected: countNotesWithProperty(snapshot, req.Property),
	}
	afterProps := cfg.Frontmatter.Properties
	if result.Changed {
		loaded, err := config.Decode(cfg.Vault, cfg.VaultOrigin, result.Patched)
		if err != nil {
			return nil, noteops.WrapError(noteops.CodeInvalidRequest, "properties: patched "+config.FileName+" is invalid", err)
		}
		afterProps = loaded.Frontmatter.Properties
		plan.Config = &propertyConfigPlan{
			AbsPath:    configPath,
			SourceHash: vault.HashBytes(source),
			Data:       result.Patched,
			Diff:       result.Diff,
			Properties: afterProps,
		}
	}

	plan.Projection = projectPropertyViolations(root, snapshot, cfg.Frontmatter.Properties, afterProps, []string{req.Property}, nil)
	return plan, nil
}

func countNotesWithProperty(snapshot vaultSnapshot, property string) int {
	count := 0
	for _, note := range snapshot.notes {
		front, err := note.Front()
		if err != nil || front == nil {
			continue
		}
		if front.Has(property) {
			count++
		}
	}
	return count
}

func propertySchemaResponseForPlan(plan *propertySchemaPlan, status ConfigStatus) propertyMutationResponse {
	response := propertyMutationResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     "schema_" + string(plan.Op),
		Property:   plan.Property,
		Affected:   plan.Affected,
		Previews:   []noteops.NotePreview{},
		Blockers:   []propertyBlocker{},
		ConfigDiff: []propertyConfigDiffLine{},
		Projected:  plan.Projection,
		Config:     status,
	}
	if plan.Config != nil {
		response.ConfigDiff = propertyConfigDiffLines(plan.Config.Diff)
	}
	return response
}

func propertySchemaCommand(op propertySchemaOp) string {
	if op == propertySchemaOpDelete {
		return propertySchemaDeleteCommand
	}
	return propertySchemaUpsertCommand
}
