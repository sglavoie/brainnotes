package noteops

import (
	"io"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// RenamePlan is the full set of edits required to rename a note and rewrite
// every reference to it.
type RenamePlan struct {
	Target    string
	NewPath   string
	OldTitle  string
	NewTitle  string
	Changes   []Change
	FileOrder []string
}

// RenameResult describes a completed note rename.
type RenameResult struct {
	Source      PathResolution
	Destination PathResolution
	Plan        *RenamePlan
	Journal     *journal.Entry
}

type renameOptions struct {
	command        string
	force          bool
	out            io.Writer
	journalOptions []journal.RecorderOption
}

// RenameOption customizes RenameNote.
type RenameOption func(*renameOptions)

func WithRenameCommand(command string) RenameOption {
	return func(o *renameOptions) {
		o.command = strings.TrimSpace(command)
	}
}

func WithRenameForce(force bool) RenameOption {
	return func(o *renameOptions) {
		o.force = force
	}
}

// RenameNote renames targetPath and rewrites wikilinks pointing at its old
// title. targetPath may be absolute or relative to the process.
func RenameNote(v *vault.Vault, cfg *config.Config, targetPath, newTitle string, opts ...RenameOption) (*RenameResult, error) {
	if v == nil {
		return nil, Errorf(CodeInvalidRequest, "rename: nil vault")
	}
	if cfg == nil {
		return nil, Errorf(CodeInvalidRequest, "rename: nil config")
	}
	options := newRenameOptions(opts)

	source, oldTitle, err := resolveRenameSource(v, targetPath, newTitle)
	if err != nil {
		return nil, err
	}
	dest, err := resolveRenameDestination(v, cfg, source.AbsPath, newTitle, options.force)
	if err != nil {
		return nil, err
	}

	plan, err := BuildRenamePlan(v, source.AbsPath, dest.AbsPath, oldTitle, newTitle, cfg)
	if err != nil {
		return nil, err
	}
	entry, err := applyRename(v, cfg, plan, options)
	if err != nil {
		return nil, err
	}
	return &RenameResult{
		Source:      source,
		Destination: dest,
		Plan:        plan,
		Journal:     entry,
	}, nil
}

// resolveRenameSource answers which note is being renamed and what it is called
// now, refusing every reason there is nothing to rename: an unusable title, a
// path that is not a note, a new title identical to the old one, and an old
// title the vault holds twice — which would make "rewrite the links pointing at
// it" ambiguous.
func resolveRenameSource(v *vault.Vault, targetPath, newTitle string) (PathResolution, string, error) {
	if err := ValidateRenameTitle(newTitle); err != nil {
		return PathResolution{}, "", err
	}
	source, err := resolveExistingMutationPath(v.Root, mutationTargetPath(v.Root, targetPath), "rename:")
	if err != nil {
		return PathResolution{}, "", err
	}
	if err := ensureRegularSource(source.AbsPath, "rename:"); err != nil {
		return PathResolution{}, "", err
	}
	oldTitle := (&vault.Note{Path: source.AbsPath}).Title()
	if oldTitle == newTitle {
		return PathResolution{}, "", Errorf(CodeInvalidRequest, "rename: new title %q is identical to current title", newTitle)
	}
	if err := EnsureUniqueRenameSourceTitle(v, source.AbsPath, oldTitle); err != nil {
		return PathResolution{}, "", err
	}
	return source, oldTitle, nil
}

// resolveRenameDestination derives the path the new title implies and refuses it
// unless it is writable, free (or forced), and does not collide with a title the
// vault already holds.
func resolveRenameDestination(v *vault.Vault, cfg *config.Config, sourceAbs, newTitle string, force bool) (PathResolution, error) {
	newPath, err := ComputeRenamePath(sourceAbs, newTitle, cfg)
	if err != nil {
		return PathResolution{}, err
	}
	dest, err := resolveWritableMutationPath(v.Root, newPath, "rename:")
	if err != nil {
		return PathResolution{}, err
	}
	if err := CheckRenameDestination(sourceAbs, dest.AbsPath, force); err != nil {
		return PathResolution{}, err
	}
	if err := EnsureUniqueRenameDestinationTitle(v, sourceAbs, dest.AbsPath, newTitle, force); err != nil {
		return PathResolution{}, err
	}
	return dest, nil
}

// applyRename is the mutating half: it opens the operation, captures everything
// the plan touches, then re-checks the destination, rewrites the backlinks and
// moves the file through CommitRenamePlan, which undoes the rewrites if the
// move is refused, and finalizes.
//
// BeginOperation runs ahead of the first Capture. RenameFile takes locks and
// would be refused there anyway, but by then the journal entry and every
// backlink snapshot would already be on the storage the policy is protecting.
func applyRename(v *vault.Vault, cfg *config.Config, plan *RenamePlan, options renameOptions) (*journal.Entry, error) {
	j, err := BeginOperation(v.Root, "rename", options.command, []string{plan.Target, plan.NewPath}, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	if err := captureRelocation(j, plan.Target, plan.NewPath, plan.FileOrder); err != nil {
		return nil, err
	}
	if err := CommitRenamePlan(j, v.Root, plan, &cfg.Frontmatter, RelocationOptions{Force: options.force, Out: options.out}); err != nil {
		return nil, err
	}
	return j.Finalize()
}

// newRenameOptions folds the variadic options onto the defaults, skipping the
// nil ones a conditional caller passes.
func newRenameOptions(opts []RenameOption) renameOptions {
	options := defaultRenameOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

func defaultRenameOptions() renameOptions {
	return renameOptions{
		command: "bn web rename",
		out:     io.Discard,
	}
}
