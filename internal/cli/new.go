package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

// execReplace is patched in tests to avoid actually replacing the test process.
var execReplace = syscall.Exec

// runEditor resolves the editor command against $PATH and exec-replaces the
// current process. A limited shell-like split supports common "editor plus
// args" values such as `code --wait` or `nvim -f` without invoking a shell.
func runEditor(editor, file string) error {
	bin, argv, err := editorInvocation(editor, file)
	if err != nil {
		return err
	}
	return execEditor(bin, argv)
}

func execEditor(bin string, argv []string) error {
	return execReplace(bin, argv, os.Environ())
}

func editorInvocation(editor, file string) (string, []string, error) {
	argv, err := splitEditorCommand(editor)
	if err != nil {
		return "", nil, err
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", nil, fmt.Errorf("locate editor %q: %w", argv[0], err)
	}
	argv = append(argv, file)
	return bin, argv, nil
}

func splitEditorCommand(editor string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
	)
	runes := []rune(editor)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote != '\'' && i+1 < len(runes):
				next := runes[i+1]
				if next == quote || next == '\\' || unicode.IsSpace(next) {
					current.WriteRune(next)
					i++
				} else {
					current.WriteRune(r)
				}
			default:
				current.WriteRune(r)
			}
			continue
		}
		switch {
		case unicode.IsSpace(r):
			flush()
		case r == '\'' || r == '"':
			quote = r
		case r == '\\' && i+1 < len(runes):
			next := runes[i+1]
			if next == '\\' || next == '\'' || next == '"' || unicode.IsSpace(next) {
				current.WriteRune(next)
				i++
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("parse editor %q: unterminated quote", editor)
	}
	flush()
	if len(args) == 0 {
		return nil, errors.New("editor command is empty")
	}
	return args, nil
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

			ext := normalizeNewDefaultExtension(cfg.Templates.DefaultExtension)
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
		return resolveNewDestFolder(cfg.Vault, folderFlag, "new: --folder")
	case cfg.New.Routes != nil:
		if dest, ok := routeForTemplate(cfg.New.Routes, templateName); ok {
			return resolveNewDestFolder(cfg.Vault, dest, fmt.Sprintf("new: route for %q", templateName))
		}
		return cfg.Vault, nil
	default:
		return cfg.Vault, nil
	}
}

func routeForTemplate(routes map[string]string, templateName string) (string, bool) {
	if dest, ok := routes[templateName]; ok && dest != "" {
		return dest, true
	}
	for name, dest := range routes {
		if dest != "" && strings.EqualFold(name, templateName) {
			return dest, true
		}
	}
	return "", false
}

func normalizeNewDefaultExtension(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" || ext == "." {
		return ".md"
	}
	if strings.ContainsAny(ext, "/\\\x00") {
		return ".md"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

func resolveNewDestFolder(vaultRoot, raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s %q is whitespace-only", label, raw)
	}
	for _, r := range raw {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s %q contains a control character (U+%04X)", label, raw, r)
		}
	}
	normalized := filepath.ToSlash(trimmed)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	if isNewDestAbsLike(normalized) {
		return "", fmt.Errorf("%s %q is an absolute path (must be vault-relative)", label, raw)
	}
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "", fmt.Errorf("%s %q is whitespace-only", label, raw)
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return vaultRoot, nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s %q is outside the vault", label, raw)
	}
	return filepath.Join(vaultRoot, filepath.FromSlash(cleaned)), nil
}

func isNewDestAbsLike(p string) bool {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 2 && isASCIIAlpha(p[0]) && p[1] == ':' {
		return len(p) == 2 || p[2] == '/'
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
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
