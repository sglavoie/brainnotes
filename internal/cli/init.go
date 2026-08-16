package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// NewInitCmd builds the `init` command, which marks an existing directory as a
// vault root by writing a starter brainnotes.toml into it.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "init [path]",
		Short:         "Write a starter brainnotes.toml into a vault root",
		Long:          "Initialize an existing directory as a vault by writing a commented starter brainnotes.toml into it. The path defaults to the current directory. An existing brainnotes.toml is never overwritten.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			root, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("init: resolve %q: %w", target, err)
			}
			info, err := os.Stat(root)
			if err != nil {
				return fmt.Errorf("init: %q: %w", root, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("init: %q is not a directory", root)
			}

			path := filepath.Join(root, config.FileName)
			switch existing, lerr := os.Lstat(path); {
			case lerr == nil && existing.Mode()&os.ModeSymlink != 0:
				return fmt.Errorf("init: %s is a symlink; refusing to write through it", path)
			case lerr == nil:
				return fmt.Errorf("init: %s already exists", path)
			case !os.IsNotExist(lerr):
				return fmt.Errorf("init: stat %s: %w", path, lerr)
			}

			if err := atomicWriteFile("init", path, []byte(config.DefaultTemplate())); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", path)
			return nil
		},
	}
}
