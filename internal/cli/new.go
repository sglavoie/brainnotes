package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

// execReplace is patched in tests to avoid actually replacing the test process.
var execReplace = syscall.Exec

// clockForNew is the clock used by the `new` command. Tests swap this for a
// FixedClock to make rendered dates deterministic.
var clockForNew tmpl.Clock = tmpl.SystemClock{}

// NewNewCmd returns the `new` subcommand. Exported for tests.
func NewNewCmd() *cobra.Command {
	var opts newOptions

	cmd := &cobra.Command{
		Use:           "new <template> <title>",
		Short:         "Create a note from a template",
		Long:          "Render a template from the configured templates directory and write it to the routed (or --folder) destination as <title>.md. Prints the created path to stdout.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.folder, "folder", "", "destination folder, vault-relative (overrides config route)")
	f.StringArrayVar(&opts.vars, "var", nil, "template variable as key=value (repeatable)")
	f.BoolVar(&opts.edit, "edit", false, "exec $EDITOR on the created note")
	f.BoolVar(&opts.force, "force", false, "overwrite the destination file if it already exists")

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
