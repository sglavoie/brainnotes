// Package cli wires the Cobra command tree for the bn binary.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
)

type ctxKey string

const configKey ctxKey = "bnotes.config"

// skipConfigLoad is the annotation key a command sets to run without a loaded
// configuration. Its value is the reason, so the exemption has to be justified
// where it is taken rather than in a list somewhere else.
//
// A command that sets it gets no *config.Config in its context and must resolve
// the vault root itself. That is a deliberate cost: the only commands entitled
// to skip the load are the ones whose job is to repair a vault that cannot load,
// and they have to say which root they are repairing.
const skipConfigLoad = "bn:skip-config-load"

// skipsConfigLoad reports whether cmd runs without a loaded configuration.
//
// Three groups qualify, and they are different kinds of thing: cobra's own
// generated commands and `help`, which have no vault to speak of; `init`, which
// creates the vault-root configuration file that loading would require; and any
// command carrying the skipConfigLoad annotation.
//
// The first two groups are recognised by name, which only works because those
// names are cobra's or a top-level command's. A nested command cannot be
// recognised that way — `migrate` alone would also match some future
// `bn vault migrate` — so one that must run on a vault it cannot load says so on
// itself instead.
func skipsConfigLoad(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "completion", "help", "init", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	if _, skip := cmd.Annotations[skipConfigLoad]; skip {
		return true
	}
	for p := cmd.Parent(); p != nil; p = p.Parent() {
		if p.Name() == "completion" {
			return true
		}
	}
	return false
}

// Execute runs the root command and returns any error from the chosen
// subcommand. SIGINT and SIGTERM cancel the command's context instead of
// killing the process, so a journaled write and `bn serve`'s graceful shutdown
// get to finish; see notifyInterrupt.
func Execute() error {
	ctx, stop := notifyInterrupt(os.Stderr)
	defer stop()
	return executeRoot(ctx, NewRootCmd())
}

// executeRoot runs root and names the failing command on a usage error Cobra
// raised outside a validator or flag parse (see asUsageError).
func executeRoot(ctx context.Context, root *cobra.Command) error {
	cmd, err := root.ExecuteContextC(ctx)
	return asUsageError(cmd, err)
}

// RenderError writes err in the form the terminal should see. Every command in
// the tree sets SilenceErrors, so this is the single place a bn error is
// printed, and the single place that decides how one is spelled.
//
// A *queryexpr.ParseError is the one error worth more than a line: it carries
// the query text and the span that failed, so it renders as a caret-annotated
// block (plus any did-you-mean candidates) instead of the one-line Error form
// that would drop the position the user needs.
func RenderError(w io.Writer, err error) {
	if err == nil {
		return
	}
	var parseErr *queryexpr.ParseError
	if errors.As(err, &parseErr) {
		// Whatever wraps the parse error — the command name, normally — is
		// kept as the prefix of the first line, so a caret block is spelled
		// `bn: find: query: ...` like every other error.
		prefix := strings.TrimSuffix(err.Error(), parseErr.Error())
		_, _ = fmt.Fprintln(w, "bn: "+prefix+parseErr.Render())
		return
	}
	_, _ = fmt.Fprintln(w, "bn:", err)
}

// NewRootCmd builds the root cobra command with persistent flags wired up.
func NewRootCmd() *cobra.Command {
	var vaultPath string

	cmd := &cobra.Command{
		Use:   "bn",
		Short: "Brain Notes: tools for plain-Markdown vaults",
		Long:  "Brain Notes (bn) helps you capture, find, edit, and maintain a plain-Markdown notes vault. Run 'bn serve' to open the browser workspace. Use --dry-run previews where available, inspect with 'git diff', and use 'bn history' / 'bn undo' for short-term rollback.",
		// The same build identifier bn-vault-guard -version and bn-vault-sync
		// -version print, so the three can be compared by eye.
		Version:       bnBuildID(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if skipsConfigLoad(cmd) {
				return nil
			}
			cfg, err := config.Load(config.LoadOptions{VaultFlag: vaultPath})
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cmd.SetContext(context.WithValue(ctx, configKey, cfg))
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&vaultPath, "vault", "", "path to vault root (overrides BNOTES_VAULT and the upward search)")
	// Declared here so Cobra does not add its default -v shorthand for
	// --version: -v is the local --verbose of links, backlinks and values, and
	// 'bn -v tags' should be refused rather than silently print the version.
	cmd.Flags().Bool("version", false, "print the bn version")

	addRootCommandGroups(cmd)
	registerCompletions(cmd)
	installUsageErrors(cmd)

	return cmd
}

// ConfigFromContext returns the *config.Config attached by PersistentPreRunE.
// Returns nil if no config has been attached (e.g. called outside of Cobra).
func ConfigFromContext(ctx context.Context) *config.Config {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(configKey).(*config.Config); ok {
		return v
	}
	return nil
}

// refuseCombinedFlags is the one wording for two flags that cannot be given
// together. Cobra's own MarkFlagsMutuallyExclusive message ("if any flags in
// the group [a b] are set none of the others can be") is the alternative, and
// it reads like nothing else bn prints.
func refuseCombinedFlags(cmd *cobra.Command, label, a, b string) error {
	if cmd.Flags().Changed(a) && cmd.Flags().Changed(b) {
		return fmt.Errorf("%s: --%s cannot be combined with --%s", label, a, b)
	}
	return nil
}
