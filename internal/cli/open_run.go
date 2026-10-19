package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// openOptions is the flag state of one `bn open` invocation.
type openOptions struct {
	printOnly bool
	nullSep   bool
}

// validate holds the single mutual-exclusion rule `open` enforces.
//
// --null only changes the separator on the printed path; pairing it with the
// editor handoff is meaningless and almost certainly a flag mistake. Reject
// upfront rather than silently dropping it.
func (o openOptions) validate() error {
	if o.nullSep && !o.printOnly {
		return errors.New("open: --null requires --print")
	}
	return nil
}

// runOpenCommand picks a note and then either prints it or hands it to the
// editor. Order is behaviour: the flag matrix is refused before the vault is
// opened, and the editor is resolved lazily — only after a selection exists —
// so `bn open --print` works on a machine with no $EDITOR and a dismissed
// picker never asks for one.
func runOpenCommand(cmd *cobra.Command, args []string, opts openOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("open: no config in context")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	target, ok, err := selectOpenTarget(cmd, v, cfg, args)
	if err != nil || !ok {
		return err
	}

	if opts.printOnly {
		return writePathList(cmd.OutOrStdout(), []string{target}, opts.nullSep)
	}
	return openTargetInEditor(cfg, target)
}

// selectOpenTarget runs the picker over every note in the vault, pre-filtered
// by the optional positional query. An empty vault and a picker that matched
// nothing are both ErrNothingSelected: there is no path to act on, and
// `p=$(bn open --print)` must not succeed with an empty value.
func selectOpenTarget(cmd *cobra.Command, v *vault.Vault, cfg *config.Config, args []string) (string, bool, error) {
	paths, err := collectNotePaths(v)
	if err != nil {
		return "", false, err
	}
	if len(paths) == 0 {
		return "", false, nothingSelected("open", "no notes in vault")
	}

	extraArgs := append([]string(nil), cfg.Fzf.Args...)
	// The query pre-filters the picker rather than auto-confirming it: `bn open
	// foo` still shows the list, narrowed.
	if len(args) == 1 && args[0] != "" {
		extraArgs = append(extraArgs, "--query", args[0])
	}

	selected, err := requireSelection(openPicker(paths, extraArgs))
	if err != nil {
		return "", false, err
	}
	return selected[0], true, nil
}

// openTargetInEditor resolves the configured editor and execs it on the
// selected path. Every failure is reported under the `open:` prefix, including
// the "no editor configured" case, which is reworded rather than wrapped so it
// names both places an editor can come from.
func openTargetInEditor(cfg *config.Config, target string) error {
	editorSpec, err := editutil.Spec(cfg.Open.Editor, os.Getenv("EDITOR"))
	if errors.Is(err, editutil.ErrNoEditorConfigured) {
		return errors.New("open: no editor configured (set [open].editor in config or $EDITOR)")
	}
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if err := openExec(editorSpec, target); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	return nil
}
