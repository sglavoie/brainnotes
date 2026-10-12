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
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

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
