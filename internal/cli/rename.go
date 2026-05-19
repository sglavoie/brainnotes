package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// renameChangeKind tags a change with the locus that was rewritten so the
// dry-run output can render an informative source location.
type renameChangeKind int

const (
	changeBody renameChangeKind = iota
	changeFrontmatter
)

// renameChange is one line-level edit produced by the pre-scan. Body changes
// carry the 1-based line number within the note; frontmatter changes carry the
// affected property key instead.
type renameChange struct {
	File    string
	Kind    renameChangeKind
	Line    int    // body only
	Key     string // frontmatter only
	OldText string
	NewText string
}

// renamePlan is the full set of edits required to rename a note plus rewrite
// every reference to it. NewPath is the on-disk path the target file will be
// moved to once the plan is applied.
type renamePlan struct {
	Target    string
	NewPath   string
	OldTitle  string
	NewTitle  string
	Changes   []renameChange
	FileOrder []string // affected files in walk order, for deterministic apply
}

// NewRenameCmd returns the `rename` subcommand. Exported for tests.
func NewRenameCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "rename <note> <new-title>",
		Short: "Rename a note and rewrite every [[wikilink]] pointing at it",
		Long: `Rename changes a note's filename to <new-title>.md (same folder) and rewrites
every wikilink in the vault that pointed at the old title — including
folder-prefixed forms (e.g. [[Books/OldTitle]]), aliased forms ([[OldTitle|Alt]]),
heading forms ([[OldTitle#Section]]), and wikilink-typed frontmatter values
(categories, parent, ...).

<note> may be a path or a bare title; bare titles are resolved
case-insensitively against the vault.

Atomicity: each affected file is rewritten via a temp-file + rename. There is
no transaction across files: if a mid-apply error occurs, prior writes stand.
Inspect with 'git diff' to recover.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("rename: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			noteArg := args[0]
			newTitle := args[1]
			if err := validateNewTitle(newTitle); err != nil {
				return err
			}

			target, err := resolveRenameTarget(v, noteArg)
			if err != nil {
				return err
			}

			oldTitle := target.Title()
			if oldTitle == newTitle {
				return fmt.Errorf("rename: new title %q is identical to current title", newTitle)
			}

			newPath, err := computeNewPath(target.Path, newTitle, cfg)
			if err != nil {
				return err
			}

			if err := checkDestination(target.Path, newPath, force); err != nil {
				return err
			}

			plan, err := buildRenamePlan(v, target.Path, newPath, oldTitle, newTitle, cfg)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return printPlan(out, v.Root, plan)
			}

			if err := applyRenamePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted; run 'git diff' to inspect partial state")
				return err
			}
			if err := renameFile(plan.Target, plan.NewPath); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted after content rewrites; run 'git diff' to inspect partial state")
				return err
			}
			rel, relErr := filepath.Rel(v.Root, plan.NewPath)
			if relErr != nil {
				rel = plan.NewPath
			}
			_, err = fmt.Fprintf(out, "renamed: %s -> %s\n", plan.Target, filepath.Join(v.Root, rel))
			return err
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "print the plan without modifying any files")
	f.BoolVar(&force, "force", false, "overwrite the destination file if it already exists")
	return cmd
}

// validateNewTitle rejects titles that would map to an invalid filename: empty,
// contain `/` (use `move`), contain a NUL byte, or begin with `.` (hidden).
func validateNewTitle(t string) error {
	if t == "" {
		return errors.New("rename: empty new title")
	}
	if strings.ContainsRune(t, '/') {
		return errors.New("rename: new title may not contain '/'; use 'bn move' to change folders")
	}
	if strings.ContainsRune(t, 0) {
		return errors.New("rename: new title contains a NUL byte")
	}
	if strings.HasPrefix(t, ".") {
		return errors.New("rename: new title may not start with '.'")
	}
	return nil
}

// resolveRenameTarget treats arg as a path when it looks like one (contains a
// path separator, is absolute, or ends in .md and exists), otherwise resolves
// it case-insensitively as a bare title.
func resolveRenameTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	looksLikePath := filepath.IsAbs(arg) ||
		strings.ContainsRune(arg, filepath.Separator) ||
		strings.HasSuffix(strings.ToLower(arg), ".md")
	if looksLikePath {
		abs, err := absInVault(v.Root, arg)
		if err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		return &vault.Note{Path: abs}, nil
	}
	n, err := v.ResolveByTitle(arg, "")
	if err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	return n, nil
}

// computeNewPath returns the destination path: same directory as target, new
// filename = <newTitle><default-extension>.
func computeNewPath(targetPath, newTitle string, cfg *config.Config) (string, error) {
	dir := filepath.Dir(targetPath)
	ext := cfg.Templates.DefaultExtension
	if ext == "" {
		ext = ".md"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return filepath.Join(dir, newTitle+ext), nil
}

// checkDestination errors when newPath already names a different file unless
// --force is set. A case-only rename on a case-insensitive filesystem (where
// stat returns the same file) is allowed.
func checkDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return fmt.Errorf("rename: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rename: stat %q: %w", newPath, err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		// Case-only rename on case-insensitive filesystem — allowed.
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("rename: destination %s already exists (use --force to overwrite)", newPath)
}

// buildRenamePlan walks the vault and collects every file that needs an edit.
// Both body wikilinks and wikilink-typed frontmatter properties are scanned.
// The renamed note itself is included so self-references update.
func buildRenamePlan(v *vault.Vault, targetPath, newPath, oldTitle, newTitle string, cfg *config.Config) (*renamePlan, error) {
	plan := &renamePlan{
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
		bodyChanges := scanBodyChanges(n.Path, body, oldTitle, newTitle)
		if len(bodyChanges) > 0 {
			plan.Changes = append(plan.Changes, bodyChanges...)
			addFile(n.Path)
		}

		front, err := n.Front()
		if err != nil {
			return err
		}
		fmChanges := scanFrontmatterChanges(n.Path, front, oldTitle, newTitle, cfg.Frontmatter.WikilinkProperties)
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

// scanBodyChanges returns one renameChange per body line whose content shifts
// after applying RewriteTitle. Line numbers are 1-based. The rewrite is run
// on the whole body first so that code-fence masking in `wikilink.Parse` sees
// the multi-line context — a per-line rewrite would miss fence openers on a
// preceding line and produce phantom changes for masked links.
func scanBodyChanges(file, body, oldTitle, newTitle string) []renameChange {
	if body == "" {
		return nil
	}
	newBody := wikilink.RewriteTitle(body, oldTitle, newTitle)
	if newBody == body {
		return nil
	}
	return diffBodyLines(file, body, newBody)
}

// diffBodyLines walks oldBody/newBody in lockstep (the rewrite never adds or
// removes newlines, so line counts match) and emits one renameChange per
// differing line. Line numbers are 1-based.
func diffBodyLines(file, oldBody, newBody string) []renameChange {
	var changes []renameChange
	line := 1
	oStart, nStart := 0, 0
	for oStart <= len(oldBody) && nStart <= len(newBody) {
		oEnd := strings.IndexByte(oldBody[oStart:], '\n')
		nEnd := strings.IndexByte(newBody[nStart:], '\n')
		var oldLine, newLine string
		if oEnd < 0 {
			oldLine = oldBody[oStart:]
		} else {
			oldLine = oldBody[oStart : oStart+oEnd]
		}
		if nEnd < 0 {
			newLine = newBody[nStart:]
		} else {
			newLine = newBody[nStart : nStart+nEnd]
		}
		if oldLine != newLine {
			changes = append(changes, renameChange{
				File:    file,
				Kind:    changeBody,
				Line:    line,
				OldText: oldLine,
				NewText: newLine,
			})
		}
		if oEnd < 0 || nEnd < 0 {
			break
		}
		oStart += oEnd + 1
		nStart += nEnd + 1
		line++
	}
	return changes
}

// scanFrontmatterChanges inspects every wikilink-typed property and records a
// change when its string value (or any list element) contains a link whose
// title matches oldTitle. Non-string values are ignored.
func scanFrontmatterChanges(file string, front *frontmatter.Map, oldTitle, newTitle string, wikilinkProps []string) []renameChange {
	var changes []renameChange
	for _, key := range wikilinkProps {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			newV := wikilink.RewriteTitle(vv, oldTitle, newTitle)
			if newV != vv {
				changes = append(changes, renameChange{
					File:    file,
					Kind:    changeFrontmatter,
					Key:     key,
					OldText: vv,
					NewText: newV,
				})
			}
		case []any:
			for _, item := range vv {
				s, ok := item.(string)
				if !ok {
					continue
				}
				ns := wikilink.RewriteTitle(s, oldTitle, newTitle)
				if ns != s {
					changes = append(changes, renameChange{
						File:    file,
						Kind:    changeFrontmatter,
						Key:     key,
						OldText: s,
						NewText: ns,
					})
				}
			}
		}
	}
	return changes
}

// printPlan renders the plan in source order: file rename first, then each
// change as `<relpath>:<line>: <old> -> <new>` (body) or
// `<relpath>:frontmatter[<key>]: <old> -> <new>` (frontmatter).
func printPlan(out io.Writer, vaultRoot string, plan *renamePlan) error {
	relTarget, err := filepath.Rel(vaultRoot, plan.Target)
	if err != nil {
		relTarget = plan.Target
	}
	relNew, err := filepath.Rel(vaultRoot, plan.NewPath)
	if err != nil {
		relNew = plan.NewPath
	}
	if _, err := fmt.Fprintf(out, "rename: %s -> %s\n", relTarget, relNew); err != nil {
		return err
	}
	sorted := make([]renameChange, len(plan.Changes))
	copy(sorted, plan.Changes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Key < sorted[j].Key
	})
	for _, c := range sorted {
		rel, err := filepath.Rel(vaultRoot, c.File)
		if err != nil {
			rel = c.File
		}
		switch c.Kind {
		case changeBody:
			if _, err := fmt.Fprintf(out, "%s:%d: %s -> %s\n", rel, c.Line, c.OldText, c.NewText); err != nil {
				return err
			}
		case changeFrontmatter:
			if _, err := fmt.Fprintf(out, "%s:frontmatter[%s]: %s -> %s\n", rel, c.Key, c.OldText, c.NewText); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyRenamePlan rewrites every affected file in order. The actual file
// rename of the target is performed by the caller after this returns
// successfully.
func applyRenamePlan(vaultRoot string, plan *renamePlan, fmCfg *config.FrontmatterConfig, out io.Writer) error {
	for _, file := range plan.FileOrder {
		if err := rewriteFile(file, plan.OldTitle, plan.NewTitle, fmCfg); err != nil {
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

// rewriteFile applies RewriteTitle to the body and to every wikilink-typed
// frontmatter property, then atomically saves. The original note metadata
// (such as titles in plain frontmatter properties) is left untouched — only
// wikilink-typed values move.
func rewriteFile(path, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) error {
	n := &vault.Note{Path: path}
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
	rewriteFrontmatter(front, oldTitle, newTitle, fmCfg)
	return n.Save(fmCfg)
}

// rewriteFrontmatter mutates front in place: every string value (scalar or
// list element) under a wikilink-typed key whose embedded title matches
// oldTitle is rewritten to newTitle.
func rewriteFrontmatter(front *frontmatter.Map, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) {
	if fmCfg == nil {
		return
	}
	for _, key := range fmCfg.WikilinkProperties {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			ns := wikilink.RewriteTitle(vv, oldTitle, newTitle)
			if ns != vv {
				front.Set(key, ns)
			}
		case []any:
			changed := false
			newList := make([]any, len(vv))
			for i, item := range vv {
				if s, ok := item.(string); ok {
					ns := wikilink.RewriteTitle(s, oldTitle, newTitle)
					if ns != s {
						changed = true
					}
					newList[i] = ns
				} else {
					newList[i] = item
				}
			}
			if changed {
				front.Set(key, newList)
			}
		}
	}
}

// renameFile moves target to newPath. When the two paths differ only in case
// the rename is performed via a temporary pivot so it works on filesystems
// that compare names case-insensitively (HFS+/APFS default).
func renameFile(target, newPath string) error {
	if target == newPath {
		return nil
	}
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
			return fmt.Errorf("rename: pivot %q -> %q: %w", tmpPath, newPath, err)
		}
		return nil
	}
	if err := os.Rename(target, newPath); err != nil {
		return fmt.Errorf("rename: %q -> %q: %w", target, newPath, err)
	}
	return nil
}
