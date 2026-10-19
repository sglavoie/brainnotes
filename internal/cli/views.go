package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

// NewViewsCmd builds the read-only `bn views` command tree: list, show, check.
//
// Every subcommand resolves the saved views through queryexpr.ResolveViews, so
// a view whose query stopped compiling is reported as disabled rather than
// dropped or fatal — listing views must keep working when one of them rotted.
func NewViewsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "views",
		Short: "List the saved views defined in the vault config",
		Long: `Views lists the saved queries in the [[views]] blocks of brainnotes.toml.
Each one is shown with its description and the query it saves, plus whether it
still compiles against this vault. Pass a name to 'bn views show' for the
canonical re-render of a view's expression, and use 'bn views check' to
diagnose the ones that no longer compile.

A saved view is used from any find-scoped command with --view.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsList(cmd, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false,
		`print a JSON array of {"name", "description", "query", "status", "error"} records`)

	cmd.AddCommand(newViewsShowCmd())
	cmd.AddCommand(newViewsCheckCmd())
	return cmd
}

// runViewsList prints every saved view as a table, or as JSON records. A
// vault with no views prints the hint on stderr; as JSON it is `[]`.
func runViewsList(cmd *cobra.Command, asJSON bool) error {
	cfg, views, err := resolveConfigViews(cmd)
	if err != nil {
		return err
	}
	if asJSON {
		return writeViewsJSON(cmd.OutOrStdout(), views)
	}
	if len(views) == 0 {
		writeViewsHint(cmd.ErrOrStderr(), "views", cfg.SourcePath)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tQUERY\tSTATUS")
	for _, view := range views {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			view.Saved.Name, view.Saved.Description, displayedViewQuery(view), viewStatus(view))
	}
	return w.Flush()
}

func viewStatus(view queryexpr.View) string {
	if view.Enabled() {
		return "ok"
	}
	return "error"
}

type viewJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Query       string `json:"query"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

// writeViewsJSON emits one record per view in config order. query is the same
// text the table's QUERY column shows; error is set exactly when status is
// "error".
func writeViewsJSON(out io.Writer, views []queryexpr.View) error {
	records := make([]viewJSON, 0, len(views))
	for _, view := range views {
		record := viewJSON{
			Name:        view.Saved.Name,
			Description: view.Saved.Description,
			Query:       displayedViewQuery(view),
			Status:      viewStatus(view),
		}
		if view.Err != nil {
			record.Error = view.Err.Error()
		}
		records = append(records, record)
	}
	return writeJSON(out, records)
}

// newViewsShowCmd renders one view in full, with its query put back through
// the expression printer rather than echoed. The canonical spelling is what
// `--view` actually runs, so seeing it is how a user confirms that what they
// saved means what they think it means.
func newViewsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one saved view, with its query re-rendered canonically",
		Long: `Show prints a single saved view: its description, its ordering and cap, and
its query re-rendered from the parsed expression. The canonical form is the one
--view runs, so it is what to read when a saved query does not select what you
expected: 'not' is spelled out where you typed '-', redundant parentheses are
gone, and values are quoted only where the grammar needs it. The query as
stored is shown too whenever the two differ.

A view that no longer compiles fails here with its parse error.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, views, err := resolveConfigViews(cmd)
			if err != nil {
				return err
			}

			name := strings.TrimSpace(args[0])
			view, ok := lookupResolvedView(views, name)
			if !ok {
				return unknownViewError(cfg, name)
			}
			if !view.Enabled() {
				return view.Err
			}

			saved := view.Saved
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "Name:\t%s\n", saved.Name)
			if saved.Description != "" {
				fmt.Fprintf(w, "Description:\t%s\n", saved.Description)
			}
			canonical := view.Plan.Expr.String()
			fmt.Fprintf(w, "Query:\t%s\n", canonical)
			if strings.TrimSpace(saved.Query) != canonical {
				fmt.Fprintf(w, "Stored as:\t%s\n", saved.Query)
			}
			if spec := view.Plan.Sort; spec.Key != "" {
				fmt.Fprintf(w, "Sort:\t%s\n", spec.String())
			}
			if saved.RelativeTo != "" {
				fmt.Fprintf(w, "Relative to:\t%s\n", saved.RelativeTo)
			}
			if saved.Limit > 0 {
				fmt.Fprintf(w, "Limit:\t%d\n", saved.Limit)
			}
			w.Flush()
			return nil
		},
	}
}

// newViewsCheckCmd compiles every saved view and reports the failures.
func newViewsCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Compile every saved view and report the ones that no longer work",
		Long: `Check compiles the query of every saved view against this vault and reports
each failure with the config file it came from, the view's name, and a caret
pointing at the offending text.

This command is the answer to internal/config's inability to validate a saved
expression. The config layer is the bottom of the package layering, so it
cannot import the query language to check that a view's query parses without
closing an import cycle; its validation is shape-only. A view whose query
rotted — a property it names was renamed, a field was removed — therefore loads
without complaint, and is reported as a disabled entry wherever views are
listed. Run 'bn views check' to find out why.

It exits non-zero when any view fails, so it is usable as a config health gate.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, views, err := resolveConfigViews(cmd)
			if err != nil {
				return err
			}
			if len(views) == 0 {
				writeViewsHint(cmd.ErrOrStderr(), "views check", cfg.SourcePath)
				return nil
			}

			out := cmd.OutOrStdout()
			failed := 0
			for _, view := range views {
				if view.Enabled() {
					continue
				}
				failed++
				fmt.Fprintf(out, "%s: view %q\n%s\n\n", cfg.SourcePath, view.Saved.Name, view.Detail())
			}
			if failed > 0 {
				return fmt.Errorf("views check: %d of %d saved views failed to compile", failed, len(views))
			}
			fmt.Fprintf(out, "views check: %d saved %s\n", len(views), plural(len(views), "view compiles", "views compile"))
			return nil
		},
	}
}

// resolveConfigViews loads the config from the command context and resolves
// every saved view against it.
//
// Only the config is read: compiling a query needs the declared property names
// and the vault root, never the notes, so `bn views` does not walk the vault.
// The graph is not built either — Compile reports what a plan needs through
// Plan.NeedsGraph rather than requiring it.
func resolveConfigViews(cmd *cobra.Command) (*config.Config, []queryexpr.View, error) {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return nil, nil, errors.New("views: no config in context")
	}
	env := queryexpr.Env{
		Root:        filepath.Dir(cfg.SourcePath),
		Frontmatter: &cfg.Frontmatter,
	}
	return cfg, queryexpr.ResolveViews(cfg.Views, env), nil
}

// lookupResolvedView finds a resolved view by name, folding case the same way
// config.View and the config patcher do so all three agree on which stored view
// a name refers to.
func lookupResolvedView(views []queryexpr.View, name string) (queryexpr.View, bool) {
	for _, view := range views {
		if strings.EqualFold(view.Saved.Name, name) {
			return view, true
		}
	}
	return queryexpr.View{}, false
}

// unknownViewError reports a miss by listing what is available, matching the
// error --view raises for the same mistake.
func unknownViewError(cfg *config.Config, name string) error {
	known := cfg.ViewNames()
	if len(known) == 0 {
		return fmt.Errorf("unknown view %q: no views are defined in %s", name, config.FileName)
	}
	return fmt.Errorf("unknown view %q: defined views are %s", name, strings.Join(known, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func writeViewsHint(w io.Writer, cmdName, configPath string) {
	fmt.Fprintf(w, "%s: no saved views defined in config.\n", cmdName)
	if configPath != "" {
		fmt.Fprintf(w, "\n  Config: %s\n", configPath)
	}
	fmt.Fprint(w, `
  Add a [[views]] block, for example:

    [[views]]
    name        = "Active work"
    query       = "status:wip and not folder:Archive"
    sort        = "-modified"
    limit       = 20
    description = "What I am actually working on"

  Then run it with: bn find --view "Active work"
`)
}

func displayedViewQuery(view queryexpr.View) string {
	if view.Plan != nil && view.Saved.Search != "" {
		return view.Plan.Expr.String()
	}
	return view.Saved.Query
}
