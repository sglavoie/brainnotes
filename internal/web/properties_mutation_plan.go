package web

import (
	"errors"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Journal command labels. History and undo read these, so they name the user's
// action rather than the internal endpoint.
const (
	propertySetCommand          = "bn serve property set"
	propertyUnsetCommand        = "bn serve property unset"
	propertyReplaceCommand      = "bn serve property replace value"
	propertyRenameCommand       = "bn serve property rename"
	propertyRepairCommand       = "bn serve property repair"
	propertyListAddCommand      = "bn serve property list add"
	propertySchemaUpsertCommand = "bn serve property schema upsert"
	propertySchemaDeleteCommand = "bn serve property schema delete"
)

// Blocker kinds. Every kind except propertyBlockerRepairSkip is fatal: it
// suppresses the receipt, so the whole selection is refused rather than partly
// written.
const (
	propertyBlockerParseError     = "parse_error"
	propertyBlockerNotFound       = "not_found"
	propertyBlockerKeyConflict    = "key_conflict"
	propertyBlockerConfigConflict = "config_conflict"
	propertyBlockerRepairSkip     = "repair_skip"
)

// propertyBlocker is one reason a planned change cannot proceed, or one issue
// the plan deliberately leaves alone.
type propertyBlocker struct {
	RelPath  string `json:"rel_path,omitempty"`
	Property string `json:"property,omitempty"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

func (b propertyBlocker) fatal() bool { return b.Kind != propertyBlockerRepairSkip }

// propertyPlanNote is one note the plan will rewrite, pinned to the bytes it
// was planned from.
type propertyPlanNote struct {
	RelPath    string
	AbsPath    string
	SourceHash string
	Data       []byte
	Front      *frontmatter.Map
	Preview    noteops.NotePreview
}

// propertyConfigPlan is the planned rewrite of brainnotes.toml.
type propertyConfigPlan struct {
	AbsPath    string
	SourceHash string
	Data       []byte
	Diff       []config.PatchLine
	Properties map[string]config.PropertyDefinition
	// Local is brainnotes.local.toml as the plan read it. Every key these plans
	// touch is portable, so the overlay is never written — but it decides the
	// value of whatever it assigns, so the patched bytes were re-decoded against
	// it and it is pinned exactly as strictly as the file that is written.
	Local configLayerPin
}

// fileStates pins both configuration layers to the bytes the plan was computed
// from. The overlay is listed whether or not it exists: an absent file digests
// to the empty string, so one appearing between preview and apply reads as the
// change it is.
func (p *propertyConfigPlan) fileStates() []propertyPreviewFileState {
	if p == nil {
		return nil
	}
	states := []propertyPreviewFileState{{
		RelPath: config.FileName,
		AbsPath: p.AbsPath,
		Digest:  p.SourceHash,
	}}
	if p.Local.Path != "" {
		states = append(states, propertyPreviewFileState{
			RelPath: config.LocalFileName,
			AbsPath: p.Local.Path,
			Digest:  p.Local.Hash,
		})
	}
	return states
}

// propertyViolationTotals counts schema violations for the properties a plan
// touches.
type propertyViolationTotals struct {
	Missing int `json:"missing"`
	Type    int `json:"type"`
	Value   int `json:"value"`
	Notes   int `json:"notes"`
}

// propertyViolationProjection contrasts the violations that exist now with the
// ones the plan would leave behind.
type propertyViolationProjection struct {
	Before propertyViolationTotals `json:"before"`
	After  propertyViolationTotals `json:"after"`
}

// propertyPlan is a fully resolved, not-yet-written property change.
type propertyPlan struct {
	Action     propertyActionKind
	Property   string
	NewName    string
	Selected   int
	Notes      []propertyPlanNote
	Blockers   []propertyBlocker
	Config     *propertyConfigPlan
	Projection propertyViolationProjection
}

func (p *propertyPlan) blocked() bool {
	if p == nil {
		return true
	}
	for _, blocker := range p.Blockers {
		if blocker.fatal() {
			return true
		}
	}
	return false
}

func (p *propertyPlan) previews() []noteops.NotePreview {
	out := make([]noteops.NotePreview, 0, len(p.Notes))
	for _, note := range p.Notes {
		out = append(out, note.Preview)
	}
	return out
}

func (p *propertyPlan) fileStates() []propertyPreviewFileState {
	states := make([]propertyPreviewFileState, 0, len(p.Notes)+1)
	for _, note := range p.Notes {
		states = append(states, propertyPreviewFileState{
			RelPath: note.RelPath,
			AbsPath: note.AbsPath,
			Digest:  note.SourceHash,
		})
	}
	return append(states, p.Config.fileStates()...)
}

// planPropertyMutation resolves a note-mutation request into the exact bytes it
// would write. It never touches the vault.
func planPropertyMutation(root string, snapshot vaultSnapshot, cfg *config.Config, configPath string, req propertyMutationRequest) (*propertyPlan, error) {
	fm := &cfg.Frontmatter
	def, defined := fm.Definition(req.Property)

	plan := &propertyPlan{
		Action:   req.Action,
		Property: req.Property,
		NewName:  req.NewName,
		Selected: len(req.Notes),
	}

	replacement, err := decodePropertyMutationValue(req, def, defined)
	if err != nil {
		return nil, err
	}
	if req.Action == propertyActionRename && !noteops.ValidFrontmatterKey(req.NewName) {
		return nil, noteops.Errorf(noteops.CodeInvalidKey, "properties: invalid property name %q (allowed: letters, digits, '_', '-')", req.NewName)
	}

	overrides, err := planPropertyNotes(root, snapshot, fm, req, replacement, plan)
	if err != nil {
		return nil, err
	}

	afterProps, err := planPropertyRenameConfig(root, configPath, req, fm, plan)
	if err != nil {
		return nil, err
	}

	keys := []string{req.Property}
	if req.NewName != "" {
		keys = append(keys, req.NewName)
	}
	plan.Projection = projectPropertyViolations(root, snapshot, fm.Properties, afterProps, keys, overrides)
	return plan, nil
}

// planPropertyNotes plans every selected note and returns the planned
// frontmatter of each one that changed, keyed by relative path, for the
// projection to be computed against.
//
// A note that cannot be planned becomes a blocker rather than an error: the
// request names a selection, and one unreadable or missing note in it is
// something to report next to the notes that are fine, not a reason to refuse
// the whole mutation.
func planPropertyNotes(
	root string,
	snapshot vaultSnapshot,
	fm *config.FrontmatterConfig,
	req propertyMutationRequest,
	replacement any,
	plan *propertyPlan,
) (map[string]*frontmatter.Map, error) {
	byRelPath := snapshotNotesByRelPath(root, snapshot)
	parseErrors := snapshotParseErrorsByRelPath(root, snapshot)
	overrides := make(map[string]*frontmatter.Map, len(req.Notes))

	for _, relPath := range req.Notes {
		if detail, ok := parseErrors[relPath]; ok {
			plan.Blockers = append(plan.Blockers, propertyBlocker{
				RelPath: relPath,
				Kind:    propertyBlockerParseError,
				Detail:  detail,
			})
			continue
		}
		note, ok := byRelPath[relPath]
		if !ok {
			plan.Blockers = append(plan.Blockers, propertyBlocker{
				RelPath: relPath,
				Kind:    propertyBlockerNotFound,
				Detail:  "note is not in the vault",
			})
			continue
		}

		planned, blockers, err := planPropertyNote(note.Path, relPath, req, fm, replacement)
		if err != nil {
			return nil, err
		}
		plan.Blockers = append(plan.Blockers, blockers...)
		if planned == nil {
			continue
		}
		plan.Notes = append(plan.Notes, *planned)
		overrides[relPath] = planned.Front
	}
	return overrides, nil
}

// planPropertyRenameConfig folds a rename's configuration half into the plan and
// returns the property set the projection has to be computed against — the
// renamed one when the configuration declared the property, and the unchanged
// one otherwise, because a rename of an undeclared property changes notes only.
func planPropertyRenameConfig(
	root string,
	configPath string,
	req propertyMutationRequest,
	fm *config.FrontmatterConfig,
	plan *propertyPlan,
) (map[string]config.PropertyDefinition, error) {
	if req.Action != propertyActionRename {
		return fm.Properties, nil
	}
	configPlan, blocker, err := planPropertyConfigRename(root, configPath, req.Property, req.NewName, fm)
	if err != nil {
		return nil, err
	}
	if blocker != nil {
		plan.Blockers = append(plan.Blockers, *blocker)
	}
	if configPlan == nil {
		return fm.Properties, nil
	}
	plan.Config = configPlan
	return configPlan.Properties, nil
}

// decodePropertyMutationValue validates the requested value once, before any
// note is read, so an invalid value fails the request outright instead of
// becoming a per-note blocker.
func decodePropertyMutationValue(req propertyMutationRequest, def config.PropertyDefinition, defined bool) (any, error) {
	switch req.Action {
	case propertyActionSet:
		return properties.ValidateTypedValue(req.Property, *req.Value, def, defined)
	case propertyActionReplaceValue:
		if defined && def.IsList() {
			return properties.ValidateTypedListItem(req.Property, *req.Value, def, defined)
		}
		return properties.ValidateTypedValue(req.Property, *req.Value, def, defined)
	default:
		return nil, nil
	}
}

// planPropertyNote reads one note and computes its planned rewrite. It returns
// a nil plan when the note needs no change.
func planPropertyNote(absPath, relPath string, req propertyMutationRequest, fm *config.FrontmatterConfig, replacement any) (*propertyPlanNote, []propertyBlocker, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "properties: read note", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, []propertyBlocker{{
			RelPath: relPath,
			Kind:    propertyBlockerParseError,
			Detail:  noteops.MessageOf(err),
		}}, nil
	}

	front := &doc.Front
	changes, blockers, changed, err := applyPropertyAction(front, req, fm, replacement)
	if err != nil {
		return nil, nil, err
	}
	for i := range blockers {
		blockers[i].RelPath = relPath
	}
	if !changed {
		return nil, blockers, nil
	}

	next, err := frontmatter.Marshal(doc, fm)
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "properties: marshal note", err)
	}
	return &propertyPlanNote{
		RelPath:    relPath,
		AbsPath:    absPath,
		SourceHash: vault.HashBytes(data),
		Data:       next,
		Front:      front,
		Preview:    noteops.NotePreview{RelPath: relPath, Changes: changes},
	}, blockers, nil
}

// applyPropertyAction mutates front in place and reports the preview changes it
// made. front belongs to a document parsed for this plan alone, never to a
// cached snapshot note.
func applyPropertyAction(front *frontmatter.Map, req propertyMutationRequest, fm *config.FrontmatterConfig, replacement any) ([]noteops.PreviewChange, []propertyBlocker, bool, error) {
	key := req.Property
	before, present := front.Get(key)

	switch req.Action {
	case propertyActionSet:
		if !properties.WriteTyped(front, key, *req.Value, replacement) {
			return nil, nil, false, nil
		}
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, present, before, true, replacement)}, nil, true, nil

	case propertyActionUnset:
		if !present {
			return nil, nil, false, nil
		}
		front.Unset(key)
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, true, before, false, nil)}, nil, true, nil

	case propertyActionReplaceValue:
		next, kinds, ok := replacePropertyValue(front, key, *req.Match, *req.Value, replacement)
		if !ok {
			return nil, nil, false, nil
		}
		front.Set(key, next)
		properties.RecordTimestampSpelling(front, key, kinds...)
		return []noteops.PreviewChange{noteops.FrontmatterChange(key, true, before, true, next)}, nil, true, nil

	case propertyActionRename:
		if front.Has(req.NewName) {
			return nil, []propertyBlocker{{
				Property: req.NewName,
				Kind:     propertyBlockerKeyConflict,
				Detail:   "note already has a " + req.NewName + " property",
			}}, false, nil
		}
		renamed, err := front.RenameKey(key, req.NewName)
		if err != nil {
			if errors.Is(err, frontmatter.ErrKeyConflict) {
				return nil, []propertyBlocker{{
					Property: req.NewName,
					Kind:     propertyBlockerKeyConflict,
					Detail:   "note already has a " + req.NewName + " property",
				}}, false, nil
			}
			return nil, nil, false, noteops.WrapError(noteops.CodeInvalidRequest, "properties: rename property", err)
		}
		if !renamed {
			return nil, nil, false, nil
		}
		return []noteops.PreviewChange{
			noteops.FrontmatterChange(key, true, before, false, nil),
			noteops.FrontmatterChange(req.NewName, false, nil, true, before),
		}, nil, true, nil

	case propertyActionRepair:
		return applyPropertyRepair(front, key, fm)
	}
	return nil, nil, false, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unsupported action %q", string(req.Action))
}

// applyPropertyRepair runs the shared schema repair planner and keeps only the
// action for the requested property. Skips are advisory: they explain what
// repair deliberately would not touch.
