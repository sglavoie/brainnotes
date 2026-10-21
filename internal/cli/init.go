package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
)

// NewInitCmd builds the `init` command, which marks an existing directory as a
// vault root by writing a starter brainnotes.toml into it.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "init [path]",
		Short:         "Write a starter brainnotes.toml into a vault root",
		Long:          "Initialize an existing directory as a vault by writing a commented starter brainnotes.toml into it. The path defaults to the --vault directory when that flag is given, otherwise the current directory; naming both with different directories is refused. An existing brainnotes.toml is never overwritten.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, args)
		},
	}
}

// runInit writes the starter config into the resolved target directory.
func runInit(cmd *cobra.Command, args []string) error {
	root, err := resolveInitTarget(cmd, args)
	if err != nil {
		return err
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
}

// resolveInitTarget picks the directory init writes into: the positional path,
// else the root --vault flag, else the current directory. init skips config
// loading, so --vault would otherwise be silently ignored and the starter
// config written somewhere the user did not name. A positional path and a
// --vault naming different directories is refused rather than guessing which
// one was meant.
func resolveInitTarget(cmd *cobra.Command, args []string) (string, error) {
	vaultFlag := ""
	if f := cmd.Flag("vault"); f != nil {
		vaultFlag = f.Value.String()
	}
	target := "."
	switch {
	case len(args) == 1:
		target = args[0]
	case vaultFlag != "":
		target = vaultFlag
	}
	root, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("init: resolve %q: %w", target, err)
	}
	if len(args) == 0 || vaultFlag == "" {
		return root, nil
	}
	flagRoot, err := filepath.Abs(vaultFlag)
	if err != nil {
		return "", fmt.Errorf("init: resolve --vault %q: %w", vaultFlag, err)
	}
	if flagRoot != root {
		return "", fmt.Errorf("init: path %q and --vault %q name different directories; give only one", args[0], vaultFlag)
	}
	return root, nil
}
