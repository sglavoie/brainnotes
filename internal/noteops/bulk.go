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
	journalOptions []journal.RecorderOption
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
	options := defaultBulkOptions("bn web bulk trash")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	result := &BulkResult{Action: "trash"}
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
		dest, err := resolveTrashDestination(vaultRoot, path.Join("Trash", source.RelPath), trashCfg)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		if err := j.Capture(source.AbsPath); err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		if err := j.Capture(dest.AbsPath); err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest.AbsPath), 0o755); err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, WrapError(CodeInternal, "create trash folder", err)))
			continue
		}
		if exists, err := pathExists(dest.AbsPath); err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, WrapError(CodeConflict, "check trash destination", err)))
			continue
		} else if exists {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, Errorf(CodeConflict, "trash destination already exists")))
			continue
		}
		if err := os.Rename(source.AbsPath, dest.AbsPath); err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, WrapError(CodeInternal, "move note to trash", err)))
			continue
		}

		result.Changed++
		result.Results = append(result.Results, BulkItemResult{
			Key:         item.Key,
			Source:      source,
			Destination: dest,
			Changed:     true,
		})
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
	options := defaultBulkOptions("bn web bulk move")
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
	if err := os.MkdirAll(newFolderAbs, 0o755); err != nil {
		return nil, WrapError(CodeInternal, fmt.Sprintf("move: mkdir %q", newFolderAbs), err)
	}

	j := NewJournal(v.Root, options.command, options.journalOptions...)
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

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	result.Journal = entry
	return result, nil
}

// BulkSetFrontmatter applies frontmatter mutations to every selected note using one journal entry.
func BulkSetFrontmatter(vaultRoot string, sources []BulkSource, mutations []FrontmatterMutation, fm *config.FrontmatterConfig, opts ...BulkOption) (*BulkResult, error) {
	options := defaultBulkOptions("bn web bulk frontmatter set")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	result := &BulkResult{Action: "frontmatter_set"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		changed, err := ApplyFrontmatterMutations(source.AbsPath, mutations, fm, j)
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
	options := defaultBulkOptions("bn web bulk frontmatter unset")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(sources) == 0 {
		return nil, Errorf(CodeInvalidRequest, "bulk: no notes selected")
	}

	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	result := &BulkResult{Action: "frontmatter_unset"}
	for _, item := range sources {
		source, err := resolveFrontmatterSource(vaultRoot, item.RelPath)
		if err != nil {
			result.Failures = append(result.Failures, BulkFailureForError(item.Key, item.RelPath, err))
			continue
		}
		changed, err := ApplyFrontmatterUnset(source.AbsPath, props, fm, j)
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
	if err := RenameFile(plan.Target, plan.NewPath); err != nil {
		return BulkItemResult{}, err
	}
	return BulkItemResult{
		Key:         item.Key,
		Source:      source,
		Destination: dest,
		Changed:     true,
	}, nil
}
