package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

// execReplace is patched in tests to avoid actually replacing the test process.
var execReplace = syscall.Exec

// runEditor resolves editor against $PATH (or treats it as a path if it
// contains a separator) and exec-replaces the current process. argv[0] is
// preserved as the user-supplied editor name so the spawned editor sees a
// conventional argv[0]. syscall.Exec does not search $PATH itself, hence the
// LookPath step.
func runEditor(editor, file string) error {
	bin, err := exec.LookPath(editor)
	if err != nil {
		return fmt.Errorf("locate editor %q: %w", editor, err)
	}
	return execReplace(bin, []string{editor, file}, os.Environ())
}

// clockForNew is the clock used by the `new` command. Tests swap this for a
// FixedClock to make rendered dates deterministic.
var clockForNew tmpl.Clock = tmpl.SystemClock{}

// NewNewCmd returns the `new` subcommand. Exported for tests.
func NewNewCmd() *cobra.Command {
	var (
		folder string
		vars   []string
		edit   bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:           "new <template> <title>",
		Short:         "Create a note from a template",
		Long:          "Render a template from the configured templates directory and write it to the routed (or --folder) destination as <title>.md. Prints the created path to stdout.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("new: no config in context")
			}
			templateName := args[0]
			title := args[1]
			if err := validateNewTitle(title); err != nil {
				// validateNewTitle prefixes with "rename:"; relabel for new.
				return fmt.Errorf("new: %s", strings.TrimPrefix(err.Error(), "rename: "))
			}

			varMap, err := parseVars(vars)
			if err != nil {
				return err
			}

			dest, err := resolveDest(cfg, templateName, folder)
			if err != nil {
				return err
			}
			relFolder, err := filepath.Rel(cfg.Vault, dest)
			if err != nil {
				return fmt.Errorf("new: rel %q: %w", dest, err)
			}
			// Reject destinations outside the vault so '--folder ../escape'
			// (or a misconfigured route) can't write past the vault root.
			if relFolder == ".." || strings.HasPrefix(relFolder, ".."+string(filepath.Separator)) {
				return fmt.Errorf("new: destination %q is outside the vault", dest)
			}
			relFolderSlash := filepath.ToSlash(relFolder)
			if relFolderSlash == "." {
				relFolderSlash = ""
			}

			tmplPath, err := tmpl.Find(cfg, templateName)
			if err != nil {
				return fmt.Errorf("new: %w", err)
			}
			body, err := os.ReadFile(tmplPath)
			if err != nil {
				return fmt.Errorf("new: read template %q: %w", tmplPath, err)
			}

			ext := cfg.Templates.DefaultExtension
			if ext == "" {
				ext = ".md"
			}
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			outPath := filepath.Join(dest, title+ext)

			if !force {
				if _, statErr := os.Stat(outPath); statErr == nil {
					return fmt.Errorf("new: %s already exists (use --force to overwrite)", outPath)
				} else if !os.IsNotExist(statErr) {
					return fmt.Errorf("new: stat %q: %w", outPath, statErr)
				}
			}

			ctx := tmpl.NewContext(title, relFolderSlash, cfg.Vault, varMap, clockForNew)
			rendered, err := tmpl.Render(string(body), ctx)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("new: mkdir %q: %w", dest, err)
			}
			if err := atomicWriteFile(outPath, []byte(rendered)); err != nil {
				return err
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), outPath); err != nil {
				return err
			}

			if edit {
				editor := os.Getenv("EDITOR")
				if editor == "" {
					return errors.New("new: --edit requires $EDITOR to be set")
				}
				if err := runEditor(editor, outPath); err != nil {
					return fmt.Errorf("new: %w", err)
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&folder, "folder", "", "destination folder, vault-relative (overrides config route)")
	f.StringArrayVar(&vars, "var", nil, "template variable as key=value (repeatable)")
	f.BoolVar(&edit, "edit", false, "exec $EDITOR on the created note")
	f.BoolVar(&force, "force", false, "overwrite the destination file if it already exists")

	return cmd
}

// parseVars converts repeated --var key=value flag values into a map. An empty
// key or a value lacking `=` is an error.
func parseVars(vars []string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for _, kv := range vars {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("new: invalid --var %q (expected key=value)", kv)
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out, nil
}

// resolveDest picks the destination folder using the documented precedence:
// explicit --folder > cfg.New.Routes[template] > vault root.
func resolveDest(cfg *config.Config, templateName, folderFlag string) (string, error) {
	switch {
	case folderFlag != "":
		return filepath.Join(cfg.Vault, filepath.FromSlash(folderFlag)), nil
	case cfg.New.Routes != nil && cfg.New.Routes[templateName] != "":
		return filepath.Join(cfg.Vault, filepath.FromSlash(cfg.New.Routes[templateName])), nil
	default:
		return cfg.Vault, nil
	}
}

// atomicWriteFile writes data to dest by writing a sibling temp file, fsyncing,
// and renaming. Mirrors vault.atomicWrite which lives in another package.
func atomicWriteFile(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	var destMode os.FileMode
	destExists := false
	if info, err := os.Stat(dest); err == nil {
		destMode = info.Mode().Perm()
		destExists = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("new: stat %q: %w", dest, err)
	}

	tmp, err := os.CreateTemp(dir, ".new-*.tmp")
	if err != nil {
		return fmt.Errorf("new: tempfile in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("new: write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("new: fsync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("new: close %q: %w", tmpPath, err)
	}
	wantMode := destMode
	if !destExists {
		wantMode = 0o644
	}
	if err := os.Chmod(tmpPath, wantMode); err != nil {
		cleanup()
		return fmt.Errorf("new: chmod %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("new: rename %q -> %q: %w", tmpPath, dest, err)
	}
	return nil
}
