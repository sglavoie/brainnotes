package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	tmpl "github.com/sglavoie/brainnotes/internal/template"
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
	f.BoolVar(&opts.edit, "edit", false, "open the created note in [open].editor or $EDITOR")
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

// routeMatch is one [new.routes] entry whose template name resolves to the same
// template file the command is rendering. rawDest is kept alongside the resolved
// one so a conflict is reported in the words the user wrote in the config.
type routeMatch struct {
	name         string
	rawDest      string
	resolvedDest string
}

func routeForTemplate(cfg *config.Config, routes map[string]string, resolvedTemplatePath string) (string, bool, error) {
	resolvedTemplatePath = filepath.Clean(resolvedTemplatePath)
	matches, err := matchingTemplateRoutes(cfg, routes, resolvedTemplatePath)
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	canonicalDest := matches[0].resolvedDest
	for _, match := range matches[1:] {
		if sameNewRouteDestination(cfg.Vault, resolvedTemplatePath, canonicalDest, match.resolvedDest) {
			continue
		}
		return "", false, conflictingRoutesError(cfg, resolvedTemplatePath, matches)
	}
	return canonicalDest, true, nil
}

// matchingTemplateRoutes returns every route naming the same template file, in
// route-name order so the first match is deterministic. A route naming a
// template that no longer resolves is skipped rather than refused: it routes
// nothing, and `bn new` already reports an unresolvable template by name.
func matchingTemplateRoutes(cfg *config.Config, routes map[string]string, resolvedTemplatePath string) ([]routeMatch, error) {
	names := make([]string, 0, len(routes))
	for name := range routes {
		names = append(names, name)
	}
	sort.Strings(names)

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
			return nil, err
		}
		matches = append(matches, routeMatch{name: name, rawDest: dest, resolvedDest: resolvedDest})
	}
	return matches, nil
}

// conflictingRoutesError names every route involved, not just the two that
// disagreed, so the config edit that fixes it is visible from the message.
func conflictingRoutesError(cfg *config.Config, resolvedTemplatePath string, matches []routeMatch) error {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, fmt.Sprintf("%q -> %q", m.name, m.rawDest))
	}
	return fmt.Errorf("new: conflicting routes for resolved template %q: %s",
		describeNewResolvedTemplate(cfg.Vault, resolvedTemplatePath), strings.Join(parts, ", "))
}
