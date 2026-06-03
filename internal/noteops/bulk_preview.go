package noteops

import (
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	BulkAffectedSource      = "source"
	BulkAffectedDestination = "destination"
	BulkAffectedRewrite     = "rewrite"
)

// BulkPreviewValue preserves the distinction between an absent frontmatter key
// and a present key whose YAML value is null or empty.
type BulkPreviewValue struct {
	Present bool `json:"present"`
	Value   any  `json:"value,omitempty"`
}

// BulkFrontmatterPreview describes the before/after state for one property.
type BulkFrontmatterPreview struct {
	Key     string           `json:"key"`
	Before  BulkPreviewValue `json:"before"`
	After   BulkPreviewValue `json:"after"`
	Changed bool             `json:"changed"`
}

// BulkMovePreview summarizes backlink/frontmatter rewrites planned by a move.
type BulkMovePreview struct {
	RewriteCount            int `json:"rewrite_count"`
	RewriteFileCount        int `json:"rewrite_file_count"`
	BodyRewriteCount        int `json:"body_rewrite_count"`
	FrontmatterRewriteCount int `json:"frontmatter_rewrite_count"`
}

// BulkAffectedPath identifies one path whose current state matters to a
// preview. Existing paths are candidates for stale-file checks; missing paths
// are planned destinations that must remain available.
type BulkAffectedPath struct {
	Role   string         `json:"role"`
	Path   PathResolution `json:"path"`
	Exists bool           `json:"exists"`
}

// BulkPreviewItem describes one selected note that can be applied later.
type BulkPreviewItem struct {
	Key         string                   `json:"key"`
	Source      PathResolution           `json:"source"`
	Destination PathResolution           `json:"destination,omitempty"`
	Changed     bool                     `json:"changed"`
	Trash       *TrashDestinationPlan    `json:"trash,omitempty"`
	Move        *BulkMovePreview         `json:"move,omitempty"`
	Frontmatter []BulkFrontmatterPreview `json:"frontmatter,omitempty"`
	Affected    []BulkAffectedPath       `json:"affected,omitempty"`
}

// BulkPreviewResult describes a read-only preview of a bulk operation.
type BulkPreviewResult struct {
	Action    string            `json:"action"`
	Results   []BulkPreviewItem `json:"results,omitempty"`
	Failures  []BulkFailure     `json:"failures,omitempty"`
	Changed   int               `json:"changed"`
	Unchanged int               `json:"unchanged,omitempty"`
}

// BulkPreviewTrashNotes plans moving every selected note into Trash without writing.
func BulkPreviewTrashNotes(vaultRoot string, sources []BulkSource, opts ...BulkOption) (*BulkPreviewResult, error) {
	options := defaultBulkOptions("bn web bulk trash preview")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	result := &BulkPreviewResult{Action: "trash"}
	trashCfg := trashOptions{
		now:     options.now,
		shortID: options.shortID,
	}
	for _, item := range sources {
		source, err := resolveTrashSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		dest, plan, err := resolveBulkTrashDestination(vaultRoot, source.RelPath, path.Join("Trash", source.RelPath), trashCfg, options.trashPlans)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		previewPlan := plan
		addBulkPreviewItem(result, BulkPreviewItem{
			Key:         item.Key,
			Source:      source,
			Destination: dest,
			Changed:     true,
			Trash:       &previewPlan,
			Affected: []BulkAffectedPath{
				{Role: BulkAffectedSource, Path: source, Exists: true},
				{Role: BulkAffectedDestination, Path: dest, Exists: false},
			},
		})
	}
	return result, nil
}

// BulkPreviewMoveNotes plans moving every selected note without creating
// folders, rewriting links, renaming files, or journaling.
func BulkPreviewMoveNotes(v *vault.Vault, cfg *config.Config, sources []BulkSource, newFolderArg string, opts ...BulkOption) (*BulkPreviewResult, error) {
	if v == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil vault")
	}
	if cfg == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil config")
	}
	options := defaultBulkOptions("bn web bulk move preview")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	newFolderRel, newFolderAbs, err := ResolveNewFolder(v.Root, newFolderArg)
	if err != nil {
		return nil, err
	}
	if err := ValidateMoveTargetPath(newFolderArg, newFolderRel); err != nil {
		return nil, err
	}

	result := &BulkPreviewResult{Action: "move"}
	for _, item := range sources {
		itemResult, err := bulkPreviewMoveOne(v, cfg, item, newFolderRel, newFolderAbs, options)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		addBulkPreviewItem(result, itemResult)
	}
	return result, nil
}

// BulkPreviewSetFrontmatter previews frontmatter mutations without saving files.
func BulkPreviewSetFrontmatter(vaultRoot string, sources []BulkSource, mutations []FrontmatterMutation, fm *config.FrontmatterConfig) (*BulkPreviewResult, error) {
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	result := &BulkPreviewResult{Action: "frontmatter_set"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		frontmatterChanges, changed, err := previewFrontmatterMutations(source.AbsPath, mutations, fm)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		addBulkPreviewItem(result, BulkPreviewItem{
			Key:         item.Key,
			Source:      source,
			Changed:     changed,
			Frontmatter: frontmatterChanges,
			Affected:    []BulkAffectedPath{{Role: BulkAffectedSource, Path: source, Exists: true}},
		})
	}
	return result, nil
}

// BulkPreviewUnsetFrontmatter previews frontmatter property removals without saving files.
func BulkPreviewUnsetFrontmatter(vaultRoot string, sources []BulkSource, props []string, fm *config.FrontmatterConfig) (*BulkPreviewResult, error) {
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	result := &BulkPreviewResult{Action: "frontmatter_unset"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		frontmatterChanges, changed, err := previewFrontmatterUnset(source.AbsPath, props, fm)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		addBulkPreviewItem(result, BulkPreviewItem{
			Key:         item.Key,
			Source:      source,
			Changed:     changed,
			Frontmatter: frontmatterChanges,
			Affected:    []BulkAffectedPath{{Role: BulkAffectedSource, Path: source, Exists: true}},
		})
	}
	return result, nil
}

func resolveBulkTrashDestination(vaultRoot, sourceRel, baseRel string, cfg trashOptions, plans map[string]TrashDestinationPlan) (PathResolution, TrashDestinationPlan, error) {
	sourceRel = strings.TrimSpace(filepath.ToSlash(sourceRel))
	if plan, ok := plans[sourceRel]; ok {
		dest, err := trashDestinationFromPlan(vaultRoot, sourceRel, baseRel, plan)
		if err != nil {
			return PathResolution{}, TrashDestinationPlan{}, err
		}
		plan.SourceRelPath = sourceRel
		plan.BaseRelPath = strings.TrimSpace(filepath.ToSlash(plan.BaseRelPath))
		plan.DestinationRelPath = dest.RelPath
		plan.CollisionIDs = append([]string(nil), plan.CollisionIDs...)
		return dest, plan, nil
	}

	dest, plan, err := resolveTrashDestinationPlan(vaultRoot, baseRel, cfg)
	if err != nil {
		return PathResolution{}, TrashDestinationPlan{}, err
	}
	plan.SourceRelPath = sourceRel
	return dest, plan, nil
}

func trashDestinationFromPlan(vaultRoot, sourceRel, baseRel string, plan TrashDestinationPlan) (PathResolution, error) {
	if strings.TrimSpace(plan.SourceRelPath) != "" && filepath.ToSlash(plan.SourceRelPath) != sourceRel {
		return PathResolution{}, Errorf(CodeConflict, "planned trash source does not match selected note")
	}
	cleanBaseRel, err := CleanRelPath(baseRel)
	if err != nil {
		return PathResolution{}, err
	}
	if strings.TrimSpace(plan.BaseRelPath) != "" && filepath.ToSlash(plan.BaseRelPath) != cleanBaseRel {
		return PathResolution{}, Errorf(CodeConflict, "planned trash base does not match selected note")
	}
	if strings.TrimSpace(plan.DestinationRelPath) == "" {
		return PathResolution{}, Errorf(CodeConflict, "planned trash destination is empty")
	}
	destRel, err := CleanRelPath(plan.DestinationRelPath)
	if err != nil {
		return PathResolution{}, err
	}
	dest, err := PathForRel(vaultRoot, destRel)
	if err != nil {
		return PathResolution{}, err
	}
	if dest.RelPath != destRel {
		return PathResolution{}, Errorf(CodeForbidden, "planned trash destination must not resolve through a symlink")
	}
	return dest, nil
}

func bulkPreviewMoveOne(v *vault.Vault, cfg *config.Config, item BulkSource, newFolderRel, newFolderAbs string, options bulkOptions) (BulkPreviewItem, error) {
	source, err := resolveExistingMutationPath(v.Root, mutationTargetPath(v.Root, item.RelPath), "move:")
	if err != nil {
		return BulkPreviewItem{}, err
	}
	if err := ensureRegularSource(source.AbsPath, "move:"); err != nil {
		return BulkPreviewItem{}, err
	}

	target := &vault.Note{Path: source.AbsPath}
	title := target.Title()
	oldFolder, err := target.Folder(v.Root)
	if err != nil {
		return BulkPreviewItem{}, WrapError(CodeInvalidRequest, "move: resolve source folder", err)
	}
	newPath := filepath.Join(newFolderAbs, title+filepath.Ext(source.AbsPath))
	dest, err := resolveWritableMutationPath(v.Root, newPath, "move:")
	if err != nil {
		return BulkPreviewItem{}, err
	}
	if err := CheckMoveDestination(source.AbsPath, dest.AbsPath, options.force); err != nil {
		return BulkPreviewItem{}, err
	}

	oldRel := JoinFolderTitle(oldFolder, title)
	newRel := JoinFolderTitle(newFolderRel, title)
	if oldRel == newRel {
		return BulkPreviewItem{}, Errorf(CodeInvalidRequest, "move: destination is the same as source: %s", oldRel)
	}

	plan, err := BuildMovePlan(v, source.AbsPath, dest.AbsPath, oldRel, newRel, cfg)
	if err != nil {
		return BulkPreviewItem{}, err
	}
	if err := CheckMoveDestination(plan.Target, plan.NewPath, options.force); err != nil {
		return BulkPreviewItem{}, err
	}
	affected, err := bulkMoveAffectedPaths(v.Root, source, dest, plan)
	if err != nil {
		return BulkPreviewItem{}, err
	}

	return BulkPreviewItem{
		Key:         item.Key,
		Source:      source,
		Destination: dest,
		Changed:     true,
		Move:        bulkMovePreviewForPlan(plan),
		Affected:    affected,
	}, nil
}

func bulkMovePreviewForPlan(plan *MovePlan) *BulkMovePreview {
	if plan == nil {
		return &BulkMovePreview{}
	}
	preview := &BulkMovePreview{
		RewriteCount:     len(plan.Changes),
		RewriteFileCount: len(plan.FileOrder),
	}
	for _, change := range plan.Changes {
		switch change.Kind {
		case ChangeBody:
			preview.BodyRewriteCount++
		case ChangeFrontmatter:
			preview.FrontmatterRewriteCount++
		}
	}
	return preview
}

func bulkMoveAffectedPaths(root string, source, dest PathResolution, plan *MovePlan) ([]BulkAffectedPath, error) {
	destExists, err := pathExists(dest.AbsPath)
	if err != nil {
		return nil, WrapError(CodeConflict, "stat move destination", err)
	}
	affected := []BulkAffectedPath{
		{Role: BulkAffectedSource, Path: source, Exists: true},
		{Role: BulkAffectedDestination, Path: dest, Exists: destExists},
	}
	if plan == nil {
		return affected, nil
	}
	for _, p := range plan.FileOrder {
		resolved, err := bulkPathResolutionForAbs(root, p)
		if err != nil {
			return nil, err
		}
		affected = append(affected, BulkAffectedPath{Role: BulkAffectedRewrite, Path: resolved, Exists: true})
	}
	return affected, nil
}

func bulkPathResolutionForAbs(root, abs string) (PathResolution, error) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return PathResolution{}, WrapError(CodeInternal, "compute vault-relative path", err)
	}
	if RelEscapesRoot(rel) || filepath.IsAbs(rel) {
		return PathResolution{}, Errorf(CodeForbidden, "path is outside the vault")
	}
	return PathResolution{AbsPath: abs, RelPath: filepath.ToSlash(rel)}, nil
}

func previewFrontmatterMutations(path string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig) ([]BulkFrontmatterPreview, bool, error) {
	_, front, err := loadFrontmatterNote(path)
	if err != nil {
		return nil, false, err
	}
	before := snapshotFrontmatterKeys(front, mutations)
	if _, err := ApplyFrontmatterMutationsToMap(front, mutations, fm); err != nil {
		return nil, false, err
	}
	changes := bulkFrontmatterPreviewForKeys(front, before, orderedFrontmatterKeys(mutations))
	return changes, bulkFrontmatterPreviewChanged(changes), nil
}

func previewFrontmatterUnset(path string, props []string, fm *config.FrontmatterConfig) ([]BulkFrontmatterPreview, bool, error) {
	if len(props) == 0 {
		return nil, false, Errorf(CodeInvalidRequest, "unset: at least one property name is required")
	}
	for _, p := range props {
		if strings.TrimSpace(p) == "" {
			return nil, false, Errorf(CodeInvalidRequest, "unset: empty property name")
		}
	}

	_, front, err := loadFrontmatterNote(path)
	if err != nil {
		return nil, false, err
	}
	keys := orderedFrontmatterProps(props)
	before := make(map[string]snapshot, len(keys))
	for _, p := range keys {
		v, ok := front.Get(p)
		before[p] = snapshot{value: v, present: ok}
	}
	for _, p := range props {
		if front.Has(p) {
			front.Unset(p)
		}
	}
	changes := bulkFrontmatterPreviewForKeys(front, before, keys)
	return changes, bulkFrontmatterPreviewChanged(changes), nil
}

func orderedFrontmatterProps(props []string) []string {
	seen := make(map[string]bool, len(props))
	out := make([]string, 0, len(props))
	for _, prop := range props {
		if seen[prop] {
			continue
		}
		seen[prop] = true
		out = append(out, prop)
	}
	return out
}

func bulkFrontmatterPreviewForKeys(front *frontmatter.Map, before map[string]snapshot, keys []string) []BulkFrontmatterPreview {
	out := make([]BulkFrontmatterPreview, 0, len(keys))
	for _, key := range keys {
		snap := before[key]
		afterValue, afterPresent := front.Get(key)
		beforePreview := BulkPreviewValue{Present: snap.present}
		if snap.present {
			beforePreview.Value = snap.value
		}
		afterPreview := BulkPreviewValue{Present: afterPresent}
		if afterPresent {
			afterPreview.Value = afterValue
		}
		out = append(out, BulkFrontmatterPreview{
			Key:    key,
			Before: beforePreview,
			After:  afterPreview,
			Changed: snap.present != afterPresent ||
				(snap.present && afterPresent && !reflect.DeepEqual(snap.value, afterValue)),
		})
	}
	return out
}

func bulkFrontmatterPreviewChanged(changes []BulkFrontmatterPreview) bool {
	for _, change := range changes {
		if change.Changed {
			return true
		}
	}
	return false
}

func addBulkPreviewItem(result *BulkPreviewResult, item BulkPreviewItem) {
	if item.Changed {
		result.Changed++
	} else {
		result.Unchanged++
	}
	result.Results = append(result.Results, item)
}
