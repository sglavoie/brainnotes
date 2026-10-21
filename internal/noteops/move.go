package noteops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// MovePlan captures every edit required to move a note across folders.
type MovePlan struct {
	Target    string
	NewPath   string
	OldRel    string
	NewRel    string
	Changes   []Change
	FileOrder []string
}

// MoveResult describes a completed note move.
type MoveResult struct {
	Source      PathResolution
	Destination PathResolution
	Plan        *MovePlan
	Journal     *journal.Entry
}

type moveOptions struct {
	command        string
	force          bool
	out            io.Writer
	journalOptions []journal.RecorderOption
}

// MoveOption customizes MoveNote.
type MoveOption func(*moveOptions)

// MoveNote moves targetPath into newFolderArg, preserving its filename and
// rewriting only folder-qualified wikilinks.
func MoveNote(v *vault.Vault, cfg *config.Config, targetPath, newFolderArg string, opts ...MoveOption) (*MoveResult, error) {
	if v == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil vault")
	}
	if cfg == nil {
		return nil, Errorf(CodeInvalidRequest, "move: nil config")
	}
	options := newMoveOptions(opts)

	source, title, oldFolder, err := resolveMoveSource(v, targetPath)
	if err != nil {
		return nil, err
	}
	dst, err := resolveMoveDestination(v, source, title, newFolderArg, options.force)
	if err != nil {
		return nil, err
	}

	oldRel := JoinFolderTitle(oldFolder, title)
	newRel := JoinFolderTitle(dst.folderRel, title)
	if oldRel == newRel {
		return nil, Errorf(CodeInvalidRequest, "move: destination is the same as source: %s", oldRel)
	}

	plan, err := BuildMovePlan(v, source.AbsPath, dst.note.AbsPath, oldRel, newRel, cfg)
	if err != nil {
		return nil, err
	}
	entry, err := applyMove(v, cfg, plan, dst, options)
	if err != nil {
		return nil, err
	}
	return &MoveResult{
		Source:      source,
		Destination: dst.note,
		Plan:        plan,
		Journal:     entry,
	}, nil
}

// moveDestination is where a move is going: the note's new path, and the folder
// that has to exist for it — kept together because the folder is named as a
// mutation target in its own right.
type moveDestination struct {
	note      PathResolution
	folderRel string
	folderAbs string
}

// resolveMoveSource answers which note is being moved, what it is called, and
// which folder it is leaving.
func resolveMoveSource(v *vault.Vault, targetPath string) (PathResolution, string, string, error) {
	source, err := resolveExistingMutationPath(v.Root, mutationTargetPath(v.Root, targetPath), "move:")
	if err != nil {
		return PathResolution{}, "", "", err
	}
	if err := ensureRegularSource(source.AbsPath, "move:"); err != nil {
		return PathResolution{}, "", "", err
	}
	target := &vault.Note{Path: source.AbsPath}
	oldFolder, err := target.Folder(v.Root)
	if err != nil {
		return PathResolution{}, "", "", WrapError(CodeInvalidRequest, "move: resolve source folder", err)
	}
	return source, target.Title(), oldFolder, nil
}

// resolveMoveDestination derives the new folder and the path inside it, refusing
// a folder the vault will not accept and a destination that is occupied unless
// the caller forced it.
func resolveMoveDestination(v *vault.Vault, source PathResolution, title, newFolderArg string, force bool) (moveDestination, error) {
	folderRel, folderAbs, err := ResolveNewFolder(v.Root, newFolderArg)
	if err != nil {
		return moveDestination{}, err
	}
	newPath := filepath.Join(folderAbs, title+filepath.Ext(source.AbsPath))
	note, err := resolveWritableMutationPath(v.Root, newPath, "move:")
	if err != nil {
		return moveDestination{}, err
	}
	if err := CheckMoveDestination(source.AbsPath, note.AbsPath, force); err != nil {
		return moveDestination{}, err
	}
	if err := ValidateMoveTargetPath(newFolderArg, folderRel); err != nil {
		return moveDestination{}, err
	}
	return moveDestination{note: note, folderRel: folderRel, folderAbs: folderAbs}, nil
}

// applyMove is the mutating half: it opens the operation, creates the
// destination folder, captures everything the plan touches, and hands the
// rewrite and the move to CommitMovePlan, which re-checks the destination last
// and undoes the rewrites if the move is refused, then finalizes. The early
// check only keeps an obvious conflict from creating a journal entry.
//
// BeginOperation runs ahead of the destination folder, which a refused move
// would otherwise leave behind empty, and names it as a target so the refusal
// happens before the directory exists.
func applyMove(v *vault.Vault, cfg *config.Config, plan *MovePlan, dst moveDestination, options moveOptions) (*journal.Entry, error) {
	j, err := BeginOperation(v.Root, "move", options.command, []string{plan.Target, dst.note.AbsPath, dst.folderAbs}, options.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	//bn:direct-write guarded by BeginOperation above
	if err := os.MkdirAll(dst.folderAbs, 0o755); err != nil {
		return nil, WrapError(CodeInternal, fmt.Sprintf("move: mkdir %q", dst.folderAbs), err)
	}
	if err := CheckMoveDestination(plan.Target, plan.NewPath, options.force); err != nil {
		return nil, err
	}
	if err := captureRelocation(j, plan.Target, plan.NewPath, plan.FileOrder); err != nil {
		return nil, err
	}
	if err := CommitMovePlan(j, v.Root, plan, &cfg.Frontmatter, RelocationOptions{Force: options.force, Out: options.out}); err != nil {
		return nil, err
	}
	return j.Finalize()
}

// newMoveOptions folds the variadic options onto the defaults, skipping the nil
// ones a conditional caller passes.
func newMoveOptions(opts []MoveOption) moveOptions {
	options := defaultMoveOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

func defaultMoveOptions() moveOptions {
	return moveOptions{
		command: "bn web move",
		out:     io.Discard,
	}
}

func ResolveNewFolder(vaultRoot, arg string) (rel, abs string, err error) {
	if arg == "" {
		return "", vaultRoot, nil
	}
	fullPath, err := resolveNewDestFolder(vaultRoot, arg, "move: new folder")
	if err != nil {
		return "", "", err
	}
	fullPath, err = filepath.Abs(fullPath)
	if err != nil {
		return "", "", WrapError(CodeInternal, "move: resolve new folder", err)
	}
	if err := checkMutationPathContained(vaultRoot, fullPath); err != nil {
		return "", "", Errorf(CodeForbidden, "move: new folder %q is outside the vault", arg)
	}

	relPath, relErr := filepath.Rel(vaultRoot, fullPath)
	if relErr != nil {
		return "", "", WrapError(CodeInternal, fmt.Sprintf("move: resolve %q", arg), relErr)
	}
	if relPath == "." {
		return "", vaultRoot, nil
	}
	if RelEscapesRoot(relPath) || filepath.IsAbs(relPath) {
		return "", "", Errorf(CodeForbidden, "move: new folder %q is outside the vault", arg)
	}
	if info, statErr := os.Stat(fullPath); statErr == nil && !info.IsDir() {
		return "", "", Errorf(CodeConflict, "move: new folder %q exists but is not a directory", arg)
	}
	return filepath.ToSlash(relPath), fullPath, nil
}

func JoinFolderTitle(folder, title string) string {
	if folder == "" {
		return title
	}
	return folder + "/" + title
}

func CheckMoveDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return Errorf(CodeInvalidRequest, "move: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return WrapError(CodeConflict, fmt.Sprintf("move: stat %q", newPath), err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		return nil
	}
	if newInfo.IsDir() {
		return Errorf(CodeConflict, "move: destination %s is an existing directory", newPath)
	}
	if force {
		return nil
	}
	return destinationExistsError("move", newPath)
}

func ValidateMoveTargetPath(newFolderArg, folder string) error {
	if r, ok := wikilink.FirstReservedTargetRune(folder); ok {
		return Errorf(CodeInvalidRequest, "move: new folder %q may not contain %q; folder-qualified wikilinks would become invalid", newFolderArg, r)
	}
	return nil
}

func BuildMovePlan(v *vault.Vault, targetPath, newPath, oldRel, newRel string, cfg *config.Config) (*MovePlan, error) {
	plan := &MovePlan{
		Target:  targetPath,
		NewPath: newPath,
		OldRel:  oldRel,
		NewRel:  newRel,
	}
	seenFiles := make(map[string]struct{})
	addFile := func(path string) {
		if _, ok := seenFiles[path]; ok {
			return
		}
		seenFiles[path] = struct{}{}
		plan.FileOrder = append(plan.FileOrder, path)
	}

	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if err := n.Load(); err != nil {
			return err
		}
		body, err := n.Body()
		if err != nil {
			return err
		}
		bodyChanges := ScanBodyMove(n.Path, body, oldRel, newRel)
		if len(bodyChanges) > 0 {
			plan.Changes = append(plan.Changes, bodyChanges...)
			addFile(n.Path)
		}

		front, err := n.Front()
		if err != nil {
			return err
		}
		fmChanges := ScanFrontmatterMove(n.Path, front, oldRel, newRel)
		if len(fmChanges) > 0 {
			plan.Changes = append(plan.Changes, fmChanges...)
			addFile(n.Path)
		}
		return nil
	})
	if err != nil {
		return nil, WrapScanError("move", v.Root, err)
	}
	return plan, nil
}

func ScanBodyMove(file, body, oldRel, newRel string) []Change {
	return scanBodyWithRewrite(file, body, oldRel, newRel, wikilink.RewriteMove)
}

func ScanFrontmatterMove(file string, front *frontmatter.Map, oldRel, newRel string) []Change {
	return scanFrontmatterWithRewrite(file, front, oldRel, newRel, wikilink.RewriteMove)
}

// PrintMovePlan renders the plan as canonical NotePreview blocks, matching
// PrintRenamePlan: a `--- <relpath>` header per affected note, the moved note
// carrying a leading `path: <old> -> <new>` line, then `body:<line>:` and
// `frontmatter[<key>]:` change lines.
func PrintMovePlan(out io.Writer, vaultRoot string, plan *MovePlan) error {
	previews := planPreviews(vaultRoot, plan.Target,
		relOrAbs(vaultRoot, plan.Target), relOrAbs(vaultRoot, plan.NewPath), plan.Changes)
	return RenderNotePreviews(out, previews)
}

func RewriteFrontmatterMove(front *frontmatter.Map, oldRel, newRel string, fmCfg *config.FrontmatterConfig) {
	rewriteFrontmatterProps(front, oldRel, newRel, fmCfg, wikilink.RewriteMove)
}

func resolveNewDestFolder(vaultRoot, raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", Errorf(CodeInvalidRequest, "%s %q is whitespace-only", label, raw)
	}
	for _, r := range raw {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", Errorf(CodeInvalidRequest, "%s %q contains a control character (U+%04X)", label, raw, r)
		}
	}
	normalized := filepath.ToSlash(trimmed)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	if isNewDestAbsLike(normalized) {
		return "", Errorf(CodeInvalidRequest, "%s %q is an absolute path (must be vault-relative)", label, raw)
	}
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "", Errorf(CodeInvalidRequest, "%s %q is whitespace-only", label, raw)
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return vaultRoot, nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", Errorf(CodeForbidden, "%s %q is outside the vault", label, raw)
	}
	return filepath.Join(vaultRoot, filepath.FromSlash(cleaned)), nil
}

func isNewDestAbsLike(p string) bool {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 2 && isASCIIAlpha(p[0]) && p[1] == ':' {
		return len(p) == 2 || p[2] == '/'
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}
