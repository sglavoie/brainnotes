package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

// execReplace is patched in tests to avoid actually replacing the test process.
var execReplace = syscall.Exec

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

			tmplPath, err := tmpl.Find(cfg, templateName)
			if err != nil {
				return fmt.Errorf("new: %w", err)
			}

			dest, err := resolveDest(cfg, tmplPath, folder)
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
			journaler := noteops.NewJournal(cfg.Vault, commandInvocation(cmd, args))

			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("new: mkdir %q: %w", dest, err)
			}
			if err := journaler.Capture(outPath); err != nil {
				return err
			}
			if err := atomicWriteFile("new", outPath, []byte(rendered)); err != nil {
				return err
			}
			if _, err := journaler.Finalize(); err != nil {
				return err
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), outPath); err != nil {
				return err
			}

			if edit {
				editorSpec, err := editutil.Spec("", os.Getenv("EDITOR"))
				if errors.Is(err, editutil.ErrNoEditorConfigured) {
					return errors.New("new: --edit requires $EDITOR to be set")
				}
				if err != nil {
					return fmt.Errorf("new: %w", err)
				}
				if err := runEditor(editorSpec, outPath); err != nil {
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
func resolveDest(cfg *config.Config, resolvedTemplatePath, folderFlag string) (string, error) {
	switch {
	case folderFlag != "":
		return resolveNewDestFolder(cfg.Vault, folderFlag, "new: --folder")
	case cfg.New.Routes != nil:
		if dest, ok, err := routeForTemplate(cfg, cfg.New.Routes, resolvedTemplatePath); err != nil {
			return "", err
		} else if ok {
			return dest, nil
		}
		return cfg.Vault, nil
	default:
		return cfg.Vault, nil
	}
}

func routeForTemplate(cfg *config.Config, routes map[string]string, resolvedTemplatePath string) (string, bool, error) {
	names := make([]string, 0, len(routes))
	for name := range routes {
		names = append(names, name)
	}
	sort.Strings(names)
	resolvedTemplatePath = filepath.Clean(resolvedTemplatePath)
	type routeMatch struct {
		name         string
		rawDest      string
		resolvedDest string
	}
	var matches []routeMatch
	for _, name := range names {
		dest := routes[name]
		if dest == "" {
			continue
		}
		matchedPath, err := tmpl.Find(cfg, name)
		if err != nil {
			continue
		}
		if filepath.Clean(matchedPath) != resolvedTemplatePath {
			continue
		}
		resolvedDest, err := resolveNewDestFolder(cfg.Vault, dest, fmt.Sprintf("new: route for %q", name))
		if err != nil {
			return "", false, err
		}
		matches = append(matches, routeMatch{name: name, rawDest: dest, resolvedDest: resolvedDest})
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	canonicalDest := matches[0].resolvedDest
	for _, match := range matches[1:] {
		if sameNewRouteDestination(cfg.Vault, resolvedTemplatePath, canonicalDest, match.resolvedDest) {
			continue
		}
		parts := make([]string, 0, len(matches))
		for _, m := range matches {
			parts = append(parts, fmt.Sprintf("%q -> %q", m.name, m.rawDest))
		}
		return "", false, fmt.Errorf("new: conflicting routes for resolved template %q: %s", describeNewResolvedTemplate(cfg.Vault, resolvedTemplatePath), strings.Join(parts, ", "))
	}
	return canonicalDest, true, nil
}
