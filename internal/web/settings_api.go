package web

import (
	"net/http"
	"reflect"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	settingsAPIRoutePath        = "/api/settings"
	settingsPreviewAPIRoutePath = "/api/settings/preview"
	settingsApplyAPIRoutePath   = "/api/settings/apply"
)

// Journal command labels. History and undo read these, so they name the user's
// action rather than the internal endpoint.
const (
	settingsSetCommand   = "bn serve setting set"
	settingsUnsetCommand = "bn serve setting unset"
)

// settingMutationOp names one change to a portable setting.
type settingMutationOp string

const (
	settingOpSet   settingMutationOp = "set"
	settingOpUnset settingMutationOp = "unset"
)

// settingMutationRequest is one portable-setting edit. Like a saved view it is
// always previewed before it can be applied: see the settings write class in
// docs/internals/web-write-classes.md.
type settingMutationRequest struct {
	Token string            `json:"token"`
	Op    settingMutationOp `json:"op"`
	Key   string            `json:"key"`
	Value *settingValue     `json:"value"`
}

// settingValue is the transport form of one typed setting value. Kind is carried
// rather than inferred from which field is populated, so a client that means
// "the empty string" and a client that means "false" are distinguishable, and a
// kind that disagrees with the key is a refusal rather than a guess.
type settingValue struct {
	Kind   string   `json:"kind"`
	Text   string   `json:"text"`
	Number int64    `json:"number"`
	Flag   bool     `json:"flag"`
	Items  []string `json:"items"`
}

func (v *settingValue) setting() config.SettingValue {
	if v == nil {
		return config.SettingValue{}
	}
	return config.SettingValue{
		Kind:   config.SettingKind(v.Kind),
		Text:   v.Text,
		Number: v.Number,
		Flag:   v.Flag,
		Items:  append([]string(nil), v.Items...),
	}
}

func settingValueOf(value config.SettingValue) settingValue {
	items := value.Items
	if items == nil {
		items = []string{}
	}
	return settingValue{
		Kind:   string(value.Kind),
		Text:   value.Text,
		Number: value.Number,
		Flag:   value.Flag,
		Items:  items,
	}
}

// settingSummary is one editable portable setting as it stands.
//
// Assigned separates "the file says this" from "this is the default", which is
// the difference between an edit that changes behaviour and one that only
// records a decision. Layer names the file that assigned it: a portable key
// parked in the overlay still decides what the vault does, and an editor that
// did not say so would offer a write that changes nothing visible.
type settingSummary struct {
	Key      string       `json:"key"`
	Kind     string       `json:"kind"`
	Summary  string       `json:"summary"`
	Assigned bool         `json:"assigned"`
	Layer    string       `json:"layer,omitempty"`
	Shadowed bool         `json:"shadowed"`
	Value    settingValue `json:"value"`
}

// settingsResponse is the shared shape of all three routes, in the shape
// viewsResponse established: a preview fills PreviewID and ExpiresAt, an apply
// fills JournalID, so a client reads one contract either way.
type settingsResponse struct {
	OK         bool                     `json:"ok"`
	Generation int64                    `json:"generation"`
	Action     string                   `json:"action"`
	Key        string                   `json:"key,omitempty"`
	Settings   []settingSummary         `json:"settings"`
	ConfigDiff []propertyConfigDiffLine `json:"config_diff"`
	PreviewID  string                   `json:"preview_id,omitempty"`
	ExpiresAt  string                   `json:"expires_at,omitempty"`
	JournalID  string                   `json:"journal_id,omitempty"`
	Config     ConfigStatus             `json:"config"`
}

// settingsPlan is a fully resolved, not-yet-written change to one portable
// setting. Like viewsPlan it never carries note rewrites: a setting is config
// only.
type settingsPlan struct {
	Op  settingMutationOp
	Key string
	// Settings is the editable table as the plan would leave it, read back from
	// the patched bytes so a preview lists what the loader will actually accept.
	Settings []settingSummary
	Config   *propertyConfigPlan
}

func (p *settingsPlan) fileStates() []propertyPreviewFileState {
	if p == nil || p.Config == nil {
		return []propertyPreviewFileState{}
	}
	return p.Config.fileStates()
}

// registerSettingsRoutes publishes the portable-settings surface.
//
// All three are registered unconditionally and gated by requestGuard. Read-only
// refuses all three, including the list, because its allowlist does not name
// them: a replica that hands out the settings a person can edit is an editing
// affordance on a published copy, and the route stays unavailable until somebody
// reviews it. Hosted mode may write them, which is the whole reason this surface
// is built out of config.EditableSettings and not out of the loaded
// configuration — a hosted deployment cannot serve a machine-local value it
// cannot name.
func (srv *Server) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc(settingsAPIRoutePath, srv.handleSettings)
	mux.HandleFunc(settingsPreviewAPIRoutePath, srv.handleSettingsPreview)
	mux.HandleFunc(settingsApplyAPIRoutePath, srv.handleSettingsApply)
}

// handleSettings lists every editable portable setting. It is read-only and
// takes no CSRF token, per the read-only route class: exact path, GET only, one
// config read and no vault walk.
//
// What it does not list is the point of the route. The response is built from
// config.EditableSettings alone, which holds portable keys only, so no machine
// -local value — an editor binary, a bind address, an agent command — can reach
// a browser through this surface even on a hosted deployment.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != settingsAPIRoutePath {
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
	writeManagementJSON(w, http.StatusOK, settingsResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     "list",
		Settings:   settingSummaries(cfg),
		ConfigDiff: []propertyConfigDiffLine{},
		Config:     status,
	})
}

// handleSettingsPreview plans one setting change and issues the one-use receipt
// the apply endpoint redeems.
func (s *Server) handleSettingsPreview(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, settingsPreviewAPIRoutePath) {
		return
	}
	req, err := s.parseSettingMutationRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}

	plan, status, _, err := s.planSettingsRequest(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	response := settingsResponseForPlan(plan, status)
	if plan.Config != nil {
		receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
			Kind:       propertyPreviewSetting,
			Generation: status.Generation,
			Setting:    req,
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

// handleSettingsApply redeems a settings receipt and writes brainnotes.toml. It
// accepts no plan of its own: the stored request is replanned and must reproduce
// exactly the bytes the preview was pinned to, so an apply can never write what
// the preview did not show.
func (s *Server) handleSettingsApply(w http.ResponseWriter, r *http.Request) {
	if !requirePropertyPost(w, r, settingsApplyAPIRoutePath) {
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
	record, err := s.propertyPreviews.consume(req.PreviewID, propertyPreviewSetting)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := propertyPreviewFilesUnchanged(record.Files); err != nil {
		writeManagementError(w, settingsWriteError(err))
		return
	}

	plan, status, pin, err := s.replanForSettingsReceipt(record)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	entry, err := s.commitPropertyPlan(
		&propertyPlan{Config: plan.Config}, "settings", settingsCommand(plan.Op), pin)
	if err != nil {
		writeManagementError(w, settingsWriteError(err))
		return
	}
	s.finishConfigWrite(true)

	cfg, status := s.configState.publication()
	response := settingsResponseForPlan(plan, status)
	response.Settings = settingSummaries(cfg)
	response.JournalID = managementJournalID(entry)
	writeManagementJSON(w, http.StatusOK, response)
}

// replanForSettingsReceipt rebuilds the plan the receipt was issued for and
// refuses it unless the vault still looks exactly as it did then.
//
// Three facts have to agree, and they answer different questions: the
// configuration generation says no declaration moved underneath the plan, a nil
// patch says the file already holds what the request asks for, and the file
// states say these particular bytes are still the ones the diff was computed
// from. A receipt that survived all three describes a write nobody has to
// re-read.
func (s *Server) replanForSettingsReceipt(
	record propertyPreviewRecord,
) (*settingsPlan, ConfigStatus, configPin, error) {
	plan, status, pin, err := s.planSettingsRequest(record.Setting)
	if err != nil {
		return nil, status, pin, settingsWriteError(err)
	}
	if status.Generation != record.Generation || plan.Config == nil ||
		!reflect.DeepEqual(plan.fileStates(), record.Files) {
		return nil, status, pin, settingsStaleConfigError()
	}
	return plan, status, pin, nil
}

// planSettingsRequest pins the configuration the plan was built from, so the
// commit can verify it is still the one on disk. It reads no snapshot: a setting
// is a config-only change and nothing about it is measured against the notes.
func (s *Server) planSettingsRequest(req settingMutationRequest) (*settingsPlan, ConfigStatus, configPin, error) {
	cfg, status, pin := s.pinnedConfig()
	plan, err := planSettingsMutation(cfg, s.configState.sourcePath, s.vault.Root, req)
	if err != nil {
		return nil, status, pin, err
	}
	return plan, status, pin, nil
}

// planSettingsMutation patches brainnotes.toml in memory and re-decodes the
// result, so the projected settings are the ones the loader would accept rather
// than the ones the patcher happened to emit.
func planSettingsMutation(cfg *config.Config, configPath, root string, req settingMutationRequest) (*settingsPlan, error) {
	plan := &settingsPlan{Op: req.Op, Key: req.Key, Settings: settingSummaries(cfg)}
	if err := refuseShadowedSetting(cfg, req.Key); err != nil {
		return nil, err
	}

	sources, localPin, err := planConfigSources(root)
	if err != nil {
		return nil, err
	}
	result, err := patchSettingSource(sources.Portable, req)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest, err.Error(), err)
	}
	if !result.Changed {
		return plan, nil
	}

	// Decoded as the layered pair: every editable key is portable, but the
	// projection is taken from a whole loaded configuration and the overlay is
	// part of what the loader accepts.
	patched := config.Sources{Portable: result.Patched, Local: sources.Local}
	loaded, err := config.DecodeSources(cfg.Vault, cfg.VaultOrigin, patched)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInvalidRequest,
			"settings: patched "+config.FileName+" is invalid: "+noteops.MessageOf(err), err)
	}
	plan.Settings = settingSummaries(loaded)
	plan.Config = &propertyConfigPlan{
		AbsPath:    configPath,
		SourceHash: vault.HashBytes(sources.Portable),
		Data:       result.Patched,
		Diff:       result.Diff,
		Properties: loaded.Frontmatter.Properties,
		Local:      localPin,
	}
	return plan, nil
}

func patchSettingSource(source []byte, req settingMutationRequest) (config.PatchResult, error) {
	if req.Op == settingOpUnset {
		return config.UnsetSetting(source, req.Key)
	}
	return config.PatchSetting(source, req.Key, req.Value.setting())
}

// refuseShadowedSetting stops a write to the portable file that the overlay
// would immediately override.
//
// Both files can assign a portable key in a vault that is not enrolled in
// synchronization, where ownership is not enforced, and there the overlay wins.
// Writing the portable file anyway would report a successful save for a value
// the vault never adopts, which is the one outcome an editing surface must not
// produce. `bn config set` refuses the same write for the same reason.
func refuseShadowedSetting(cfg *config.Config, key string) error {
	if cfg == nil {
		return nil
	}
	if layer, ok := cfg.SourceOf(key); !ok || layer != config.LayerLocal {
		return nil
	}
	return noteops.Errorf(noteops.CodeConflict,
		"settings: %s assigns %s and the overlay wins, so writing %s would not change what this vault does; "+
			"remove it from %s or run `bn config migrate`",
		config.LocalFileName, key, config.FileName, config.LocalFileName)
}

// settingSummaries renders every editable portable setting out of a loaded
// configuration. cfg may be a projection that is not published yet, which is
// exactly what a preview renders.
func settingSummaries(cfg *config.Config) []settingSummary {
	specs := config.EditableSettings()
	out := make([]settingSummary, 0, len(specs))
	for _, spec := range specs {
		summary := settingSummary{Key: spec.Key, Kind: string(spec.Kind), Summary: spec.Summary}
		if cfg == nil {
			out = append(out, summary)
			continue
		}
		value, err := config.SettingValueOf(cfg, spec.Key)
		if err != nil {
			out = append(out, summary)
			continue
		}
		summary.Value = settingValueOf(value)
		if layer, assigned := cfg.SourceOf(spec.Key); assigned {
			summary.Assigned = true
			summary.Layer = string(layer)
			summary.Shadowed = layer == config.LayerLocal
		}
		out = append(out, summary)
	}
	return out
}

func settingsResponseForPlan(plan *settingsPlan, status ConfigStatus) settingsResponse {
	response := settingsResponse{
		OK:         true,
		Generation: status.Generation,
		Action:     string(plan.Op),
		Key:        plan.Key,
		Settings:   plan.Settings,
		ConfigDiff: []propertyConfigDiffLine{},
		Config:     status,
	}
	if plan.Config != nil {
		response.ConfigDiff = propertyConfigDiffLines(plan.Config.Diff)
	}
	return response
}
