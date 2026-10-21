package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/index"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// indexOptions is the flag state of one `bn index` invocation.
type indexOptions struct {
	status  bool
	rebuild bool
	asJSON  bool
}

func NewIndexCmd() *cobra.Command {
	var opts indexOptions

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Report on or rebuild the vault note cache",
		Long: `Index reports on the note cache bn keeps under .bnotes/index, and rebuilds it
on demand.

The cache is unconditional and invisible: there is no flag to enable or disable
it. It holds nothing but bytes bn already read out of the vault, so any state
file bn cannot use is discarded and rebuilt on the next load without asking.
That leaves '--rebuild' and 'rm -rf .bnotes/index' as the entire support
surface, and they do the same thing.

With no flags, 'bn index' prints the same report as '--status'. Neither form
walks the vault: reporting on the cache must never be the thing that builds it.
--json prints that report as a JSON object.`,
		Args:          noArgs("index"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndexCommand(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.status, "status", false, "report the cache state file: entry count, byte size, and last write (the default)")
	cmd.Flags().BoolVar(&opts.rebuild, "rebuild", false, "discard the cache and rebuild it from the vault")
	cmd.Flags().BoolVar(&opts.asJSON, "json", false,
		`print the status report as a JSON object {"state_file", "status", "problem", "entries", "bytes", "last_write"}`)

	return cmd
}

func runIndexCommand(cmd *cobra.Command, opts indexOptions) error {
	if err := refuseCombinedFlags(cmd, "index", "status", "rebuild"); err != nil {
		return err
	}
	if err := refuseCombinedFlags(cmd, "index", "json", "rebuild"); err != nil {
		return err
	}
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("index: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	if opts.rebuild {
		return runIndexRebuild(cmd, v)
	}
	// --status is the default and so needs no branch of its own; the flag
	// exists so the report can be asked for explicitly.
	if opts.asJSON {
		return runIndexStatusJSON(cmd.OutOrStdout(), v.Root)
	}
	return runIndexStatus(cmd.OutOrStdout(), v.Root)
}

// runIndexStatus prints what the state file says about itself. It reads the
// file and stops; a vault that has never been indexed reports "not built"
// rather than being indexed as a side effect of being asked about.
func runIndexStatus(out io.Writer, root string) error {
	st, err := index.Stat(root)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%-12s %s\n", "State file:", indexDisplayPath(root, st.Path))
	switch {
	case !st.Exists:
		fmt.Fprintf(out, "%-12s %s\n", "Status:", "not built")
		return nil
	case st.Problem != "":
		// The failure class, then what bn will do about it — an unusable cache
		// is a self-healing condition, not something to act on.
		fmt.Fprintf(out, "%-12s unusable (%s); the next load rebuilds it\n", "Status:", st.Problem)
	default:
		fmt.Fprintf(out, "%-12s %s\n", "Status:", "healthy")
		fmt.Fprintf(out, "%-12s %d\n", "Entries:", st.Entries)
	}
	fmt.Fprintf(out, "%-12s %d B\n", "Size:", st.Bytes)
	fmt.Fprintf(out, "%-12s %s\n", "Last write:", st.ModTime.UTC().Format(time.RFC3339))
	return nil
}

// indexStatusJSON is `index --json`. Status is "not built", "unusable" or
// "healthy", as in the text form; the fields the text form leaves out for a
// state are null rather than absent, so every record has the same keys.
type indexStatusJSON struct {
	StateFile string  `json:"state_file"`
	Status    string  `json:"status"`
	Problem   *string `json:"problem"`
	Entries   *int    `json:"entries"`
	Bytes     *int64  `json:"bytes"`
	LastWrite *string `json:"last_write"`
}

func runIndexStatusJSON(out io.Writer, root string) error {
	st, err := index.Stat(root)
	if err != nil {
		return err
	}
	record := indexStatusJSON{StateFile: indexDisplayPath(root, st.Path), Status: "not built"}
	if st.Exists {
		size := int64(st.Bytes)
		lastWrite := st.ModTime.UTC().Format(time.RFC3339)
		record.Bytes, record.LastWrite = &size, &lastWrite
		record.Status = "healthy"
		if st.Problem != "" {
			problem := st.Problem
			record.Status, record.Problem = "unusable", &problem
		} else {
			entries := st.Entries
			record.Entries = &entries
		}
	}
	return writeJSON(out, record)
}

// runIndexRebuild throws the cache away and walks the vault to refill it. The
// counts it prints are the same classification a snapshot sees: notes that
// loaded, and notes the walk visited but could not turn into a note.
func runIndexRebuild(cmd *cobra.Command, v *vault.Vault) error {
	// Route the index's own warnings through this command's stderr instead of
	// the standard logger, so a rebuild that trips a self-healing class says so
	// on the stream the user is already reading.
	stderr := cmd.ErrOrStderr()
	ix, err := index.New(v, index.WithLogger(func(format string, args ...any) {
		fmt.Fprintf(stderr, format+"\n", args...)
	}))
	if err != nil {
		return err
	}

	ix.Reset()
	result, err := ix.Load()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Rebuilt the index: %d notes indexed, %d unreadable.\n", len(result.Notes), len(result.Failed))
	for _, failed := range result.Failed {
		fmt.Fprintf(out, "  %s\n", failed.Error())
	}
	return nil
}

// indexDisplayPath prefers the vault-relative form, which is how the docs and
// the repair instructions spell it.
func indexDisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
