package noteops

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
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

func WithRenameOutput(out io.Writer) RenameOption {
	return func(o *renameOptions) {
		o.out = out
	}
}

func WithRenameJournalOptions(opts ...journal.RecorderOption) RenameOption {
	return func(o *renameOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
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
// the plan touches, rewrites the backlinks, moves the file, and finalizes.
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
	if err := ApplyRenamePlan(v.Root, plan, &cfg.Frontmatter, options.out); err != nil {
		return nil, err
	}
	if err := RenameFile(v.Root, plan.Target, plan.NewPath); err != nil {
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

func ValidateNewTitle(t string) error {
	if t == "" {
		return Errorf(CodeInvalidRequest, "rename: empty new title")
	}
	if strings.TrimSpace(t) == "" {
		return Errorf(CodeInvalidRequest, "rename: new title may not be whitespace-only")
	}
	if t != strings.TrimSpace(t) {
		return Errorf(CodeInvalidRequest, "rename: new title may not have leading or trailing whitespace")
	}
	if strings.ContainsRune(t, '/') {
		return Errorf(CodeInvalidRequest, "rename: new title may not contain '/'; use 'bn move' to change folders")
	}
	for _, r := range t {
		if r == 0 {
			return Errorf(CodeInvalidRequest, "rename: new title contains a NUL byte")
		}
		if r < 0x20 || r == 0x7f {
			return Errorf(CodeInvalidRequest, "rename: new title contains a control character (U+%04X)", r)
		}
	}
	if strings.HasPrefix(t, ".") {
		return Errorf(CodeInvalidRequest, "rename: new title may not start with '.'")
	}
	return nil
}

func ValidateRenameTitle(t string) error {
	if err := ValidateNewTitle(t); err != nil {
		return err
	}
	if r, ok := wikilink.FirstReservedTargetRune(t); ok {
		return Errorf(CodeInvalidRequest, "rename: new title may not contain %q; wikilink rewrites require plain note titles", r)
	}
	return nil
}

func ComputeRenamePath(targetPath, newTitle string, cfg *config.Config) (string, error) {
	dir := filepath.Dir(targetPath)
	ext := filepath.Ext(targetPath)
	if ext == "" {
		ext = cfg.Templates.DefaultExtension
		if ext == "" {
			ext = ".md"
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
	}
	return filepath.Join(dir, newTitle+ext), nil
}

func CheckRenameDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return Errorf(CodeInvalidRequest, "rename: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errorsIs(err, fs.ErrNotExist) {
			return nil
		}
		return WrapError(CodeConflict, fmt.Sprintf("rename: stat %q", newPath), err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		return nil
	}
	if newInfo.IsDir() {
		return Errorf(CodeConflict, "rename: destination %s is an existing directory", newPath)
	}
	if force {
		return nil
	}
	return Errorf(CodeConflict, "rename: destination %s already exists (use --force to overwrite)", newPath)
}

func EnsureUniqueRenameSourceTitle(v *vault.Vault, targetPath, title string) error {
	duplicates, err := collectDuplicateRenameTitles(v, targetPath, title)
	if err != nil {
		return WrapError(CodeInternal, "rename: scan vault for duplicate titles", err)
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return Errorf(CodeConflict, "rename: title %q is duplicated elsewhere in the vault (%s); refusing title-only rewrite", title, strings.Join(duplicates, ", "))
}

func EnsureUniqueRenameDestinationTitle(v *vault.Vault, targetPath, newPath, title string, force bool) error {
	duplicates, err := collectDuplicateRenameTitles(v, targetPath, title)
	if err != nil {
		return WrapError(CodeInternal, "rename: scan vault for duplicate titles", err)
	}
	if force && len(duplicates) > 0 {
		filtered := duplicates[:0]
		for _, rel := range duplicates {
			p := filepath.Join(v.Root, filepath.FromSlash(rel))
			if samePath(p, newPath) {
				continue
			}
			filtered = append(filtered, rel)
		}
		duplicates = filtered
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return Errorf(CodeConflict, "rename: new title %q already exists elsewhere in the vault (%s); refusing ambiguous rewrite", title, strings.Join(duplicates, ", "))
}

func collectDuplicateRenameTitles(v *vault.Vault, targetPath, title string) ([]string, error) {
	var duplicates []string
	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if n.Path == targetPath || !strings.EqualFold(textnorm.NFC(n.Title()), textnorm.NFC(title)) {
			return nil
		}
		rel, err := filepath.Rel(v.Root, n.Path)
		if err != nil {
			rel = n.Path
		}
		duplicates = append(duplicates, rel)
		return nil
	})
	return duplicates, err
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func BuildRenamePlan(v *vault.Vault, targetPath, newPath, oldTitle, newTitle string, cfg *config.Config) (*RenamePlan, error) {
	plan := &RenamePlan{
		Target:   targetPath,
		NewPath:  newPath,
		OldTitle: oldTitle,
		NewTitle: newTitle,
	}
	seenFiles := make(map[string]struct{})
	addFile := func(p string) {
		if _, ok := seenFiles[p]; ok {
			return
		}
		seenFiles[p] = struct{}{}
		plan.FileOrder = append(plan.FileOrder, p)
	}

	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if err := n.Load(); err != nil {
			return err
		}
		body, err := n.Body()
		if err != nil {
			return err
		}
		bodyChanges := ScanBodyRename(n.Path, body, oldTitle, newTitle)
		if len(bodyChanges) > 0 {
			plan.Changes = append(plan.Changes, bodyChanges...)
			addFile(n.Path)
		}

		front, err := n.Front()
		if err != nil {
			return err
		}
		fmChanges := ScanFrontmatterRename(n.Path, front, oldTitle, newTitle)
		if len(fmChanges) > 0 {
			plan.Changes = append(plan.Changes, fmChanges...)
			addFile(n.Path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rename: scan vault: %w", err)
	}
	return plan, nil
}

func ScanBodyRename(file, body, oldTitle, newTitle string) []Change {
	return scanBodyWithRewrite(file, body, oldTitle, newTitle, wikilink.RewriteTitle)
}

func ScanFrontmatterRename(file string, front *frontmatter.Map, oldTitle, newTitle string) []Change {
	return scanFrontmatterWithRewrite(file, front, oldTitle, newTitle, wikilink.RewriteTitle)
}

// PrintRenamePlan renders the plan as canonical NotePreview blocks: a
// `--- <relpath>` header per affected note, the target note carrying a leading
// `path: <old> -> <new>` line for the file rename, then `body:<line>:` and
// `frontmatter[<key>]:` change lines. This is the same shared preview contract
// the frontmatter-only dry-runs emit, so all `bn` previews agree.
func PrintRenamePlan(out io.Writer, vaultRoot string, plan *RenamePlan) error {
	previews := planPreviews(vaultRoot, plan.Target,
		relOrAbs(vaultRoot, plan.Target), relOrAbs(vaultRoot, plan.NewPath), plan.Changes)
	return RenderNotePreviews(out, previews)
}

func ApplyRenamePlan(vaultRoot string, plan *RenamePlan, fmCfg *config.FrontmatterConfig, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	for _, file := range plan.FileOrder {
		if err := RewriteFileRename(vaultRoot, file, plan.OldTitle, plan.NewTitle, fmCfg); err != nil {
			return fmt.Errorf("rename: rewrite %s: %w", file, err)
		}
		rel, err := filepath.Rel(vaultRoot, file)
		if err != nil {
			rel = file
		}
		if _, err := fmt.Fprintf(out, "%s: updated\n", rel); err != nil {
			return err
		}
	}
	return nil
}

func RewriteFileRename(vaultRoot, path, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) error {
	n := vault.NewNote(vaultRoot, path)
	if err := n.Load(); err != nil {
		return err
	}
	body, err := n.Body()
	if err != nil {
		return err
	}
	newBody := wikilink.RewriteTitle(body, oldTitle, newTitle)
	if newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return err
		}
	}
	front, err := n.Front()
	if err != nil {
		return err
	}
	RewriteFrontmatterRename(front, oldTitle, newTitle, fmCfg)
	return n.Save(fmCfg)
}

func RewriteFrontmatterRename(front *frontmatter.Map, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) {
	rewriteFrontmatterProps(front, oldTitle, newTitle, fmCfg, wikilink.RewriteTitle)
}

// RenameFile moves target to newPath, holding the path lock on both ends for the
// whole move. Both need locking: the case-only pivot below empties target for a
// moment, and the plain rename can land on a newPath that a concurrent
// transaction is holding in custody — either vacancy is what strands a writer's
// content in a recovery file.
// The rename itself is the operation, so it is performed directly rather than
// through a replacement primitive; the lock above and the BeginOperation the
// caller went through are what make it safe.
//
//bn:direct-write the rename is the operation, locked on both ends, reached through BeginOperation
func RenameFile(vaultRoot, target, newPath string) error {
	if target == newPath {
		return nil
	}
	release, err := vault.LockPaths(vaultRoot, target, newPath)
	if err != nil {
		return err
	}
	defer release()
	if strings.EqualFold(target, newPath) && target != newPath {
		dir := filepath.Dir(target)
		tmp, err := os.CreateTemp(dir, ".rename-pivot-*")
		if err != nil {
			return fmt.Errorf("rename: tempfile in %q: %w", dir, err)
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		if err := os.Rename(target, tmpPath); err != nil {
			return fmt.Errorf("rename: pivot %q -> %q: %w", target, tmpPath, err)
		}
		if err := os.Rename(tmpPath, newPath); err != nil {
			if restoreErr := os.Rename(tmpPath, target); restoreErr != nil {
				return fmt.Errorf("rename: pivot %q -> %q: %w; restore to %q failed: %v", tmpPath, newPath, err, target, restoreErr)
			}
			return fmt.Errorf("rename: pivot %q -> %q: %w", tmpPath, newPath, err)
		}
		return nil
	}
	if err := os.Rename(target, newPath); err != nil {
		return fmt.Errorf("rename: %q -> %q: %w", target, newPath, err)
	}
	return nil
}

func errorsIs(err, target error) bool {
	return err != nil && target != nil && (err == target || os.IsNotExist(err))
}
