package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// sourceShort maps a ConfigSource to the short tag used in the "Active config"
// summary line (e.g. "default" in "(default, exists)").
func sourceShort(s config.ConfigSource) string {
	switch s {
	case config.SourceFlag:
		return "flag"
	case config.SourceEnv:
		return "env"
	case config.SourceXDG:
		return "xdg"
	case config.SourceDefault:
		return "default"
	}
	return "unknown"
}

// NewConfigCmd builds the `config` command tree. getConfigPath returns the
// current value of the --config persistent flag; it's a closure so the command
// can be constructed before cobra has parsed flags.
func NewConfigCmd(getConfigPath func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config",
		Short:         "Show the active config path and resolution chain",
		Long:          "Print the active config path (or the default path that would be used) and list each source in the resolution chain so users can see what was inspected.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cands, err := config.Candidates(getConfigPath())
			if err != nil {
				return err
			}
			return printResolution(cmd.OutOrStdout(), cands)
		},
	}

	cmd.AddCommand(newConfigEditCmd(getConfigPath))
	return cmd
}

func newConfigEditCmd(getConfigPath func() string) *cobra.Command {
	return &cobra.Command{
		Use:           "edit",
		Short:         "Open the config in $EDITOR, creating it from a template if missing",
		Long:          "Open the active config file in $EDITOR. If no config exists at the resolved path, write a commented starter template there first (creating parent directories as needed) and report the creation on stderr before handing off to the editor.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, ok, err := config.ResolvePath(getConfigPath())
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("config edit: no config path could be resolved")
			}

			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return fmt.Errorf("config edit: mkdir %q: %w", filepath.Dir(path), err)
				}
				if err := atomicWriteFile(path, []byte(config.DefaultTemplate())); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "created %s\n", path)
			} else if statErr != nil {
				return fmt.Errorf("config edit: stat %q: %w", path, statErr)
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				return errors.New("config edit: $EDITOR is not set")
			}
			if err := runEditor(editor, path); err != nil {
				return fmt.Errorf("config edit: %w", err)
			}
			return nil
		},
	}
}

// labelWidth is the width to which each source label is left-padded so paths
// line up in the resolution table. Driven by "$XDG_CONFIG_HOME" (16) + 5 spaces.
const labelWidth = 21

func printResolution(w io.Writer, cands []config.CandidatePath) error {
	var active *config.CandidatePath
	for i := range cands {
		if cands[i].Active {
			active = &cands[i]
			break
		}
	}

	var err error
	pr := func(format string, a ...any) {
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(w, format, a...)
	}

	switch {
	case active != nil && active.Exists:
		pr("Active config: %s (%s, exists)\n", active.Path, sourceShort(active.Source))
	case active != nil:
		pr("Active config: none found\n")
		pr("Default path:  %s (would be created by `bn config edit`)\n", active.Path)
	default:
		pr("Active config: none found\n")
	}

	pr("\n")
	pr("Resolution order (highest to lowest precedence):\n")
	for i, c := range cands {
		if err != nil {
			break
		}
		label := fmt.Sprintf("%-*s", labelWidth, c.Label)
		if c.Path == "" {
			pr("  %d. %snot set\n", i+1, label)
			continue
		}
		tags := ""
		if c.Active {
			tags += " [active]"
		}
		if c.Exists {
			tags += " [exists]"
		} else {
			tags += " [missing]"
		}
		pr("  %d. %s%s %s\n", i+1, label, c.Path, tags)
	}
	return err
}
