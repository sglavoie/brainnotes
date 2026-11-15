package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	tmpl "github.com/sglavoie/brainnotes/internal/template"
)

// todayDateLayout is the title of a daily note: the date, sortable as text.
const todayDateLayout = "2006-01-02"

type todayOptions struct {
	template string
	print    bool
}

// NewTodayCmd returns the `today` subcommand. Exported for tests.
//
// It is `bn new <template> <today's date> --edit` that does not fail on the
// second run of the day: a daily note is opened far more often than it is
// created, so the note existing already is the ordinary case rather than an
// error. Creation goes through writeNewNote, so it is the same journalled
// operation `bn new` performs and not a second way to write a note.
func NewTodayCmd() *cobra.Command {
	var opts todayOptions

	cmd := &cobra.Command{
		Use:   "today",
		Short: "Open today's daily note, creating it from a template first if needed",
		Long: `Open the note named after today's date (YYYY-MM-DD) in [open].editor or $EDITOR.
If it does not exist yet, it is created first from the "daily" template, in the
folder [new.routes] gives that template (or the vault root). Prints the note's
path to stdout either way.

The note is found where it would be created, so moving daily notes elsewhere
means changing the route. --print reports the path without opening an editor.`,
		Example: `  bn today
  bn today --print
  bn today --template journal`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTodayCommand(cmd, opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.template, "template", "daily", "template to create the note from")
	f.BoolVar(&opts.print, "print", false, "print the note's path without opening an editor")
	return cmd
}

func runTodayCommand(cmd *cobra.Command, opts todayOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("today: no config in context")
	}
	title := clockForNew.Now().Format(todayDateLayout)

	outPath, exists, err := todayNotePath(cfg, opts.template, title)
	if err != nil {
		return err
	}
	if !exists {
		plan, err := planNewNote(cfg, opts.template, title, newOptions{})
		if err != nil {
			return relabelNewError(err)
		}
		if err := writeNewNote(cmd, []string{opts.template, title}, cfg, plan); err != nil {
			return err
		}
		outPath = plan.outPath
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), outPath); err != nil {
		return err
	}
	if opts.print {
		return nil
	}
	editorSpec, err := resolveEditorSpec("today", cfg.Open.Editor)
	if err != nil {
		return err
	}
	if err := runEditor(editorSpec, outPath); err != nil {
		return fmt.Errorf("today: %w", err)
	}
	return nil
}

// todayNotePath is where today's note lives, and whether it is there. It asks
// resolveDest, the same routing planNewNote uses, so the note is looked for in
// exactly the place it would be created.
func todayNotePath(cfg *config.Config, templateName, title string) (string, bool, error) {
	tmplPath, err := tmpl.Find(cfg, templateName)
	if err != nil {
		return "", false, fmt.Errorf("today: %w; create a %q template or name another with --template", err, templateName)
	}
	dest, err := resolveDest(cfg, tmplPath, "")
	if err != nil {
		return "", false, relabelNewError(err)
	}
	outPath := filepath.Join(dest, title+normalizeNewDefaultExtension(cfg.Templates.DefaultExtension))
	switch _, err := os.Stat(outPath); {
	case err == nil:
		return outPath, true, nil
	case os.IsNotExist(err):
		return outPath, false, nil
	default:
		return "", false, fmt.Errorf("today: stat %q: %w", outPath, err)
	}
}

// relabelNewError renames the command in an error the shared `new` planning
// produced, so a refusal reads as coming from the command that was run.
func relabelNewError(err error) error {
	const prefix = "new: "
	if msg := err.Error(); len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return fmt.Errorf("today: %s", msg[len(prefix):])
	}
	return err
}
