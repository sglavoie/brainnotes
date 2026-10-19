package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// newOptions is the flag state of one `bn new` invocation. There is no
// validate() method: `new` has no mutual-exclusion rules — --folder, --var,
// --edit, and --force all compose — so an empty one would be ceremony rather
// than the single place a tenth rule goes.
type newOptions struct {
	folder string
	vars   []string
	edit   bool
	force  bool
}

// newPlan is the fully resolved description of the note about to be created:
// where its folder is, what the note is called, and the bytes it will hold.
// Everything in it is decided before the first artifact exists on disk.
type newPlan struct {
	dest     string
	outPath  string
	rendered string
}

func runNewCommand(cmd *cobra.Command, args []string, opts newOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("new: no config in context")
	}
	plan, err := planNewNote(cfg, args[0], args[1], opts)
	if err != nil {
		return err
	}
	if err := writeNewNote(cmd, args, cfg, plan); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), plan.outPath); err != nil {
		return err
	}
	if !opts.edit {
		return nil
	}
	return editNewNote(cfg.Open.Editor, plan.outPath)
}

// planNewNote resolves everything the write needs and refuses everything it can
// before anything is put on disk.
//
// Order is behaviour. The title is checked before the template is looked up, the
// template is read before the destination is tested for an existing file, and
// the whole plan is built before writeNewNote begins the journalled operation,
// so a refusal at any of these steps leaves no folder and no note behind.
func planNewNote(cfg *config.Config, templateName, title string, opts newOptions) (newPlan, error) {
	if err := validateNewTitle(title); err != nil {
		// validateNewTitle prefixes with "rename:"; relabel for new.
		return newPlan{}, fmt.Errorf("new: %s", strings.TrimPrefix(err.Error(), "rename: "))
	}

	varMap, err := parseVars(opts.vars)
	if err != nil {
		return newPlan{}, err
	}

	tmplPath, err := tmpl.Find(cfg, templateName)
	if err != nil {
		return newPlan{}, fmt.Errorf("new: %w", err)
	}

	dest, err := resolveDest(cfg, tmplPath, opts.folder)
	if err != nil {
		return newPlan{}, err
	}
	relFolder, err := vaultRelativeNewFolder(cfg.Vault, dest)
	if err != nil {
		return newPlan{}, err
	}

	body, err := os.ReadFile(tmplPath)
	if err != nil {
		return newPlan{}, fmt.Errorf("new: read template %q: %w", tmplPath, err)
	}

	outPath := filepath.Join(dest, title+normalizeNewDefaultExtension(cfg.Templates.DefaultExtension))
	if err := checkNewDestinationFree(outPath, opts.force); err != nil {
		return newPlan{}, err
	}

	ctx := tmpl.NewContext(title, relFolder, cfg.Vault, varMap, clockForNew)
	rendered, err := tmpl.Render(string(body), ctx)
	if err != nil {
		return newPlan{}, err
	}
	return newPlan{dest: dest, outPath: outPath, rendered: rendered}, nil
}

// vaultRelativeNewFolder renders dest as the vault-relative slash path the
// template context sees, which is "" for the vault root itself.
//
// Rejecting a destination outside the vault is what stops '--folder ../escape'
// (or a misconfigured route) from writing past the vault root.
func vaultRelativeNewFolder(vaultRoot, dest string) (string, error) {
	rel, err := filepath.Rel(vaultRoot, dest)
	if err != nil {
		return "", fmt.Errorf("new: rel %q: %w", dest, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("new: destination %q is outside the vault", dest)
	}
	slash := filepath.ToSlash(rel)
	if slash == "." {
		return "", nil
	}
	return slash, nil
}

// checkNewDestinationFree refuses an occupied destination unless --force was
// given. A stat error other than "does not exist" is reported rather than read
// as a free name, so a permission problem cannot turn into an overwrite.
func checkNewDestinationFree(outPath string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("new: %s already exists (use --force to overwrite)", outPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("new: stat %q: %w", outPath, err)
	}
	return nil
}

// writeNewNote creates the destination folder and the note under one journalled
// operation.
//
// BeginOperation runs ahead of the folder, which is the first artifact here and
// would otherwise survive a write the policy goes on to refuse.
func writeNewNote(cmd *cobra.Command, args []string, cfg *config.Config, plan newPlan) error {
	journaler, err := noteops.BeginOperationContext(commandContext(cmd), cfg.Vault, "new", commandInvocation(cmd, args),
		[]string{plan.dest, plan.outPath})
	if err != nil {
		return err
	}
	defer journaler.Release()
	// BeginOperation above named this folder as a target, so a refused
	// note never leaves it behind empty.
	//bn:direct-write guarded by BeginOperation above
	if err := os.MkdirAll(plan.dest, 0o755); err != nil {
		return fmt.Errorf("new: mkdir %q: %w", plan.dest, err)
	}
	if err := journaler.Capture(plan.outPath); err != nil {
		return err
	}
	// Locked here rather than inside atomicWriteFile because 'bn init' shares
	// that helper to write the vault marker, before any vault exists to lock in.
	release, err := vault.LockPaths(cfg.Vault, plan.outPath)
	if err != nil {
		return err
	}
	writeErr := atomicWriteFile("new", plan.outPath, []byte(plan.rendered))
	release()
	if writeErr != nil {
		return writeErr
	}
	_, err = journaler.Finalize()
	return err
}

// editNewNote hands the created note to the configured editor ([open].editor,
// then $EDITOR), the same choice `bn open` makes. Resolution is deliberately
// lazy — it runs only after the note exists and its path has been printed — so
// `new --edit` on a machine with no editor still creates the note before it
// reports that it cannot open it.
func editNewNote(configured, outPath string) error {
	editorSpec, err := resolveEditorSpec("new", configured)
	if err != nil {
		return err
	}
	if err := runEditor(editorSpec, outPath); err != nil {
		return fmt.Errorf("new: %w", err)
	}
	return nil
}
