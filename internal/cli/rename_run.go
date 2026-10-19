package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// renameOptions has no validate(): --dry-run and --force compose, and --force
// is consumed by the destination checks inside planRename, which run before
// --dry-run decides whether to print or apply. The absence is a decision, not
// an omission. Refusals that belong to the positional arguments live in
// planRename and in noteops.ValidateRenameTitle instead.
type renameOptions struct {
	dryRun bool
	force  bool
}

func runRenameCommand(cmd *cobra.Command, args []string, opts renameOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("rename: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	noteArg, newTitle, ok, err := resolveRenameArgs(v, cfg, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	plan, err := planRename(v, cfg, noteArg, newTitle, opts.force)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.dryRun {
		return noteops.PrintRenamePlan(out, v.Root, plan)
	}
	return applyRename(cmd, args, v, cfg, plan)
}

// resolveRenameArgs splits the positionals into the note to rename and its new
// title. With one argument the title is validated *before* the picker runs, so
// a bad title is refused without first making the user choose a note; an empty
// selection is a silent no-op, reported as ok=false rather than as an error.
func resolveRenameArgs(v *vault.Vault, cfg *config.Config, args []string) (noteArg, newTitle string, ok bool, err error) {
	if len(args) == 2 {
		return args[0], args[1], true, nil
	}
	newTitle = args[0]
	if err := noteops.ValidateRenameTitle(newTitle); err != nil {
		return "", "", false, err
	}
	selected, err := pickNotes(v, cfg, false)
	if err != nil {
		return "", "", false, err
	}
	if len(selected) == 0 {
		return "", "", false, nil
	}
	return selected[0], newTitle, true, nil
}

// planRename runs every refusal in order and returns the plan. All of it
// happens while nothing has been written, so a refused rename leaves the vault
// untouched. The order is behaviour: the title is validated before the note is
// resolved, an identical title is refused before the source-title uniqueness
// check, and both destination checks run before the plan is built.
func planRename(v *vault.Vault, cfg *config.Config, noteArg, newTitle string, force bool) (*noteops.RenamePlan, error) {
	if err := noteops.ValidateRenameTitle(newTitle); err != nil {
		return nil, err
	}
	target, err := resolveRenameTarget(v, noteArg)
	if err != nil {
		return nil, err
	}
	oldTitle := target.Title()
	if oldTitle == newTitle {
		return nil, fmt.Errorf("rename: new title %q is identical to current title", newTitle)
	}
	if err := noteops.EnsureUniqueRenameSourceTitle(v, target.Path, oldTitle); err != nil {
		return nil, err
	}
	newPath, err := noteops.ComputeRenamePath(target.Path, newTitle, cfg)
	if err != nil {
		return nil, err
	}
	if err := noteops.CheckRenameDestination(target.Path, newPath, force); err != nil {
		return nil, err
	}
	if err := noteops.EnsureUniqueRenameDestinationTitle(v, target.Path, newPath, newTitle, force); err != nil {
		return nil, err
	}
	return noteops.BuildRenamePlan(v, target.Path, newPath, oldTitle, newTitle, cfg)
}

// applyRename journals the plan, rewrites every referring file, and only then
// moves the note itself. The two interruption warnings name different states
// and must stay distinct: the first means no file moved, the second means the
// content rewrites landed but the note is still at its old path.
func applyRename(cmd *cobra.Command, args []string, v *vault.Vault, cfg *config.Config, plan *noteops.RenamePlan) error {
	out := cmd.OutOrStdout()
	journaler, err := noteops.BeginOperationContext(commandContext(cmd), v.Root, "rename", commandInvocation(cmd, args),
		[]string{plan.Target, plan.NewPath})
	if err != nil {
		return err
	}
	defer journaler.Release()
	if err := captureRenamePaths(journaler, plan); err != nil {
		return err
	}

	if err := noteops.ApplyRenamePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "rename: warning: interrupted; run 'git diff' to inspect partial state")
		return err
	}
	if err := noteops.RenameFile(v.Root, plan.Target, plan.NewPath); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "rename: warning: interrupted after content rewrites; run 'git diff' to inspect partial state")
		return err
	}
	if _, err := journaler.Finalize(); err != nil {
		return err
	}
	return printRenameResult(out, v.Root, plan)
}

// captureRenamePaths records the pre-state of the note's old path, its new
// path, and every file whose wikilinks the plan rewrites, so undo can restore
// all three classes.
func captureRenamePaths(journaler *noteops.Journal, plan *noteops.RenamePlan) error {
	if err := journaler.Capture(plan.Target); err != nil {
		return err
	}
	if err := journaler.Capture(plan.NewPath); err != nil {
		return err
	}
	for _, p := range plan.FileOrder {
		if err := journaler.Capture(p); err != nil {
			return err
		}
	}
	return nil
}

func printRenameResult(out io.Writer, vaultRoot string, plan *noteops.RenamePlan) error {
	rel, relErr := filepath.Rel(vaultRoot, plan.NewPath)
	if relErr != nil {
		rel = plan.NewPath
	}
	_, err := fmt.Fprintf(out, "renamed: %s -> %s\n", plan.Target, filepath.Join(vaultRoot, rel))
	return err
}

// validateNewTitle rejects titles that would map to an invalid filename: empty
// or whitespace-only, contain `/` (use `move`), contain a NUL byte or any
// other ASCII control character (including newline/tab), have leading or
// trailing whitespace, or begin with `.` (hidden).
//
// Its only caller is `bn new` (planNewNote), which relabels the "rename:"
// prefix; `bn rename` itself validates through noteops.ValidateRenameTitle.
func validateNewTitle(t string) error {
	if t == "" {
		return errors.New("rename: empty new title")
	}
	if strings.TrimSpace(t) == "" {
		return errors.New("rename: new title may not be whitespace-only")
	}
	if t != strings.TrimSpace(t) {
		return errors.New("rename: new title may not have leading or trailing whitespace")
	}
	if strings.ContainsRune(t, '/') {
		return errors.New("rename: new title may not contain '/'; use 'bn move' to change folders")
	}
	for _, r := range t {
		if r == 0 {
			return errors.New("rename: new title contains a NUL byte")
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("rename: new title contains a control character (U+%04X)", r)
		}
	}
	if strings.HasPrefix(t, ".") {
		return errors.New("rename: new title may not start with '.'")
	}
	return nil
}
