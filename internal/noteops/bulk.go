package noteops

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// BulkSource identifies one route-validated note selected for a bulk action.
type BulkSource struct {
	Key     string
	RelPath string
}

// BulkItemResult describes one successfully processed selected note.
type BulkItemResult struct {
	Key         string
	Source      PathResolution
	Destination PathResolution
	Changed     bool
}

// BulkFailure describes one selected note that could not be processed.
type BulkFailure struct {
	Key     string    `json:"key,omitempty"`
	RelPath string    `json:"rel_path,omitempty"`
	Code    ErrorCode `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
}

// BulkResult describes a completed bulk operation and its single journal entry.
type BulkResult struct {
	Action    string
	Results   []BulkItemResult
	Failures  []BulkFailure
	Journal   *journal.Entry
	Changed   int
	Unchanged int
}

type bulkOptions struct {
	command        string
	force          bool
	out            io.Writer
	now            func() time.Time
	shortID        func() (string, error)
	trashPlans     map[string]TrashDestinationPlan
	journalOptions []journal.RecorderOption
	requires       []fileRequirement
}

// BulkOption customizes bulk operations.
type BulkOption func(*bulkOptions)

// WithBulkCommand sets the command stored in the undo journal entry.
func WithBulkCommand(command string) BulkOption {
	return func(o *bulkOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithBulkForce allows bulk move to overwrite existing files.
func WithBulkForce(force bool) BulkOption {
	return func(o *bulkOptions) {
		o.force = force
	}
}

// WithBulkOutput writes move rewrite summaries to out.
func WithBulkOutput(out io.Writer) BulkOption {
	return func(o *bulkOptions) {
		o.out = out
	}
}

// WithBulkTrashClock sets the timestamp source for trash collision suffixes.
func WithBulkTrashClock(now func() time.Time) BulkOption {
	return func(o *bulkOptions) {
		if now == nil {
			return
		}
		o.now = now
		o.journalOptions = append(o.journalOptions, journal.WithClock(now))
	}
}

// WithBulkTrashShortIDGenerator sets the short ID source for trash collision suffixes.
func WithBulkTrashShortIDGenerator(gen func() (string, error)) BulkOption {
	return func(o *bulkOptions) {
		if gen != nil {
			o.shortID = gen
		}
	}
}

// WithBulkTrashDestinationPlans reuses preview-planned Trash destinations.
func WithBulkTrashDestinationPlans(plans []TrashDestinationPlan) BulkOption {
	return func(o *bulkOptions) {
		if len(plans) == 0 {
			return
		}
		if o.trashPlans == nil {
			o.trashPlans = make(map[string]TrashDestinationPlan, len(plans))
		}
		for _, plan := range plans {
			sourceRel := strings.TrimSpace(filepath.ToSlash(plan.SourceRelPath))
			if sourceRel == "" {
				continue
			}
			plan.SourceRelPath = sourceRel
			plan.BaseRelPath = strings.TrimSpace(filepath.ToSlash(plan.BaseRelPath))
			plan.DestinationRelPath = strings.TrimSpace(filepath.ToSlash(plan.DestinationRelPath))
			plan.CollisionIDs = append([]string(nil), plan.CollisionIDs...)
			o.trashPlans[sourceRel] = plan
		}
	}
}

// WithBulkRequire pins a file every note write depends on but never changes.
// Each note's transaction verifies it at the commit boundary, so a dependency
// that changes partway through a bulk action stops the remaining writes instead
// of applying them under declarations nobody validated.
func WithBulkRequire(path string, expect vault.FileExpectation) BulkOption {
	return func(o *bulkOptions) {
		if strings.TrimSpace(path) == "" {
			return
		}
		o.requires = append(o.requires, fileRequirement{path: path, expect: expect})
	}
}

// WithBulkJournalOptions passes recorder options through to the undo journal.
func WithBulkJournalOptions(opts ...journal.RecorderOption) BulkOption {
	return func(o *bulkOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// BulkFailureForError converts an operation error into a per-note failure.
func BulkFailureForError(key, relPath string, err error) BulkFailure {
	return BulkFailure{
		Key:     strings.TrimSpace(key),
		RelPath: strings.TrimSpace(filepath.ToSlash(relPath)),
		Code:    CodeOf(err),
		Message: MessageOf(err),
	}
}

// BulkTrashNotes moves every selected note into Trash using one journal entry.
func BulkTrashNotes(vaultRoot string, sources []BulkSource, opts ...BulkOption) (*BulkResult, error) {
	options := newBulkOptions("bn web bulk trash", opts)
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	// One check for the whole batch, ahead of the journal recorder that would
	// otherwise create an entry directory before any item is judged. Each item is
	// then checked again on its own destination below.
	j, err := BeginOperation(vaultRoot, "bulk-trash", options.command, nil, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	result := &BulkResult{Action: "trash"}
	trashCfg := trashOptions{
		now:     options.now,
		shortID: options.shortID,
	}

	for _, item := range sources {
		itemResult, err := bulkTrashOne(vaultRoot, item, trashCfg, options.trashPlans, j)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		result.Changed++
		result.Results = append(result.Results, itemResult)
	}

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	result.Journal = entry
	return result, nil
}

// BulkMoveNotes moves every selected note into newFolderArg using one journal entry.
func BulkMoveNotes(v *vault.Vault, cfg *config.Config, sources []BulkSource, newFolderArg string, opts ...BulkOption) (*BulkResult, error) {
	if v == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil vault")
	}
	if cfg == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil config")
	}
	options := newBulkOptions("bn web bulk move", opts)
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
	// Ahead of the destination folder, which is created once for the batch and
	// would otherwise outlive a refused move.
	j, err := BeginOperation(v.Root, "bulk-move", options.command, []string{newFolderAbs}, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	// BeginOperation above named this folder as a target, so a refused batch
	// never reaches it.
	//bn:direct-write guarded by BeginOperation above
	if err := os.MkdirAll(newFolderAbs, 0o755); err != nil {
		return nil, WrapError(CodeInternal, fmt.Sprintf("move: mkdir %q", newFolderAbs), err)
	}

	result := bulkMoveAll(v, cfg, sources, newFolderRel, newFolderAbs, j, options)

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	result.Journal = entry
	return result, nil
}

// BulkSetFrontmatter applies frontmatter mutations to every selected note using one journal entry.
func BulkSetFrontmatter(vaultRoot string, sources []BulkSource, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, opts ...BulkOption) (*BulkResult, error) {
	options := newBulkOptions("bn web bulk frontmatter set", opts)
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	j, err := BeginOperation(vaultRoot, "bulk-frontmatter-set", options.command, nil, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	result := &BulkResult{Action: "frontmatter_set"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		changed, err := applyFrontmatterMutations(vaultRoot, source.AbsPath, mutations, fm, j, options.requires)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		if changed {
			result.Changed++
		} else {
			result.Unchanged++
		}
		result.Results = append(result.Results, BulkItemResult{
			Key:     item.Key,
			Source:  source,
			Changed: changed,
		})
	}

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	result.Journal = entry
	return result, nil
}

// BulkUnsetFrontmatter removes frontmatter properties from every selected note using one journal entry.
func BulkUnsetFrontmatter(vaultRoot string, sources []BulkSource, props []string, fm *config.FrontmatterConfig, opts ...BulkOption) (*BulkResult, error) {
	options := newBulkOptions("bn web bulk frontmatter unset", opts)
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	j, err := BeginOperation(vaultRoot, "bulk-frontmatter-unset", options.command, nil, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	result := &BulkResult{Action: "frontmatter_unset"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		changed, err := applyFrontmatterUnset(vaultRoot, source.AbsPath, props, fm, j, options.requires)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		if changed {
			result.Changed++
		} else {
			result.Unchanged++
		}
		result.Results = append(result.Results, BulkItemResult{
			Key:     item.Key,
			Source:  source,
			Changed: changed,
		})
	}

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	result.Journal = entry
	return result, nil
}

// newBulkOptions folds the variadic options onto the defaults for command,
// skipping the nil ones a conditional caller passes.
func newBulkOptions(command string, opts []BulkOption) bulkOptions {
	options := defaultBulkOptions(command)
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

// bulkTrashOne trashes a single item of a batch, under the batch's journal. Its
// failure is its own — the caller records it and carries on with the rest.
func bulkTrashOne(vaultRoot string, item BulkSource, trashCfg trashOptions, plans map[string]TrashDestinationPlan, j *Journal) (BulkItemResult, error) {
	source, err := resolveTrashSource(vaultRoot, item.RelPath)
	if err != nil {
		return BulkItemResult{}, err
	}
	dest, _, err := resolveBulkTrashDestination(vaultRoot, source.RelPath, path.Join("Trash", source.RelPath), trashCfg, plans)
	if err != nil {
		return BulkItemResult{}, err
	}
	// Per item as well as per batch: this item's destination folder is created
	// below, before the move takes its lock and re-checks, so the batch-level
	// root check is not on its own enough to keep a folder from appearing under
	// protected storage nested inside a safe vault.
	if err := guardMutation(vaultRoot, source.AbsPath, dest.AbsPath); err != nil {
		return BulkItemResult{}, err
	}
	if err := j.Capture(source.AbsPath); err != nil {
		return BulkItemResult{}, err
	}
	if err := j.Capture(dest.AbsPath); err != nil {
		return BulkItemResult{}, err
	}
	// The batch's BeginOperation already refused a forbidden location before
	// any note in it was touched.
	//bn:direct-write guarded by the batch's BeginOperation
	if err := os.MkdirAll(filepath.Dir(dest.AbsPath), 0o755); err != nil {
		return BulkItemResult{}, WrapError(CodeInternal, "create trash folder", err)
	}
	if err := moveToTrash(vaultRoot, source.AbsPath, dest.AbsPath); err != nil {
		return BulkItemResult{}, err
	}
	return BulkItemResult{
		Key:         item.Key,
		Source:      source,
		Destination: dest,
		Changed:     true,
	}, nil
}

// bulkMoveAll runs every selected note through bulkMoveOne under the batch's
// journal, recording each outcome. An item's failure is its own.
func bulkMoveAll(v *vault.Vault, cfg *config.Config, sources []BulkSource, newFolderRel, newFolderAbs string, j *Journal, options bulkOptions) *BulkResult {
	result := &BulkResult{Action: "move"}
	for _, item := range sources {
		itemResult, err := bulkMoveOne(v, cfg, item, newFolderRel, newFolderAbs, j, options)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		result.Changed++
		result.Results = append(result.Results, itemResult)
	}
	return result
}

func defaultBulkOptions(command string) bulkOptions {
	return bulkOptions{
		command: command,
		out:     io.Discard,
		now:     func() time.Time { return time.Now().UTC() },
		shortID: defaultTrashShortID,
	}
}

func bulkMoveOne(v *vault.Vault, cfg *config.Config, item BulkSource, newFolderRel, newFolderAbs string, j *Journal, options bulkOptions) (BulkItemResult, error) {
	source, err := resolveExistingMutationPath(v.Root, mutationTargetPath(v.Root, item.RelPath), "move:")
	if err != nil {
		return BulkItemResult{}, err
	}
	if err := ensureRegularSource(source.AbsPath, "move:"); err != nil {
		return BulkItemResult{}, err
	}

	target := &vault.Note{Path: source.AbsPath}
	title := target.Title()
	oldFolder, err := target.Folder(v.Root)
	if err != nil {
		return BulkItemResult{}, WrapError(CodeInvalidRequest, "move: resolve source folder", err)
	}
	newPath := filepath.Join(newFolderAbs, title+filepath.Ext(source.AbsPath))
	dest, err := resolveWritableMutationPath(v.Root, newPath, "move:")
	if err != nil {
		return BulkItemResult{}, err
	}
	if err := CheckMoveDestination(source.AbsPath, dest.AbsPath, options.force); err != nil {
		return BulkItemResult{}, err
	}

	oldRel := JoinFolderTitle(oldFolder, title)
	newRel := JoinFolderTitle(newFolderRel, title)
	if oldRel == newRel {
		return BulkItemResult{}, Errorf(CodeInvalidRequest, "move: destination is the same as source: %s", oldRel)
	}

	plan, err := BuildMovePlan(v, source.AbsPath, dest.AbsPath, oldRel, newRel, cfg)
	if err != nil {
		return BulkItemResult{}, err
	}
	if err := CheckMoveDestination(plan.Target, plan.NewPath, options.force); err != nil {
		return BulkItemResult{}, err
	}
	if err := j.Capture(plan.Target); err != nil {
		return BulkItemResult{}, err
	}
	if err := j.Capture(plan.NewPath); err != nil {
		return BulkItemResult{}, err
	}
	for _, p := range plan.FileOrder {
		if err := j.Capture(p); err != nil {
			return BulkItemResult{}, err
		}
	}

	if err := ApplyMovePlan(v.Root, plan, &cfg.Frontmatter, options.out); err != nil {
		return BulkItemResult{}, err
	}
	if err := RenameFile(v.Root, plan.Target, plan.NewPath); err != nil {
		return BulkItemResult{}, err
	}
	return BulkItemResult{
		Key:         item.Key,
		Source:      source,
		Destination: dest,
		Changed:     true,
	}, nil
}
