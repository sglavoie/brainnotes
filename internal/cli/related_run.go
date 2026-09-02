package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// runRelatedCommand ranks the notes related to the source note and renders the
// result in the mode the flags select.
//
// Order is behaviour: the whole flag matrix and the --mode value are refused by
// validateRelatedOptions before the config is read, so `bn related x --mode
// nonsense` outside a vault is a mode error rather than a vault error.
func runRelatedCommand(cmd *cobra.Command, args []string, options relatedOptions) error {
	mode, err := validateRelatedOptions(options)
	if err != nil {
		return err
	}

	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("related: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	setup, err := newRecallSetup(v.Root, recallNow())
	if err != nil {
		return err
	}

	queryResult, err := buildRelatedQuery(v, args[0], mode, setup.rankOptions())
	if err != nil {
		return err
	}
	if len(queryResult.results) > options.limit {
		queryResult.results = queryResult.results[:options.limit]
	}

	if options.interactive {
		return runRelatedInteractive(cmd, cfg, v.Root, setup, queryResult, options)
	}
	return runRelatedListing(cmd, v.Root, queryResult, options)
}

// runRelatedInteractive is the only path through `bn related` that writes.
//
// A successful selection records the opened pair in <vault>/.bnotes/recall.json
// through setup.store.RecordOpened, and that write is the point of the mode:
// it is what later rankings read back as the recall signal. Every other path
// through this command — and every non-interactive invocation — is read-only,
// which is the boundary docs/internals/recall-context.md states. Two things
// keep it true here: this function is reached only when options.interactive is
// set, and RecordOpened runs only after the picker returned a path, so a
// dismissed picker records nothing.
func runRelatedInteractive(
	cmd *cobra.Command,
	cfg *config.Config,
	root string,
	setup *recallSetup,
	queryResult relatedQuery,
	options relatedOptions,
) error {
	selection, err := selectRelatedPath(root, queryResult.results, cfg.Fzf.Args)
	if err != nil {
		return err
	}
	if selection.absolutePath == "" {
		return nil
	}
	if err := setup.store.RecordOpened(queryResult.source.Path, selection.relativePath); err != nil {
		return fmt.Errorf("related: record selected note: %w", err)
	}

	if !options.openResult {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), selection.absolutePath)
		return err
	}
	return openRelatedSelection(cfg, selection.absolutePath)
}

// openRelatedSelection resolves the editor and hands it the selected note.
// Resolution is deliberately after the selection and after the recall write:
// the selection is recorded whether or not an editor turns out to be
// configured, so a missing $EDITOR does not silently drop the signal.
func openRelatedSelection(cfg *config.Config, absolutePath string) error {
	editorSpec, err := editutil.Spec(cfg.Open.Editor, os.Getenv("EDITOR"))
	if errors.Is(err, editutil.ErrNoEditorConfigured) {
		return errors.New("related: --open requires [open].editor or $EDITOR")
	}
	if err != nil {
		return fmt.Errorf("related: resolve editor: %w", err)
	}
	if err := relatedOpenExec(editorSpec, absolutePath); err != nil {
		return fmt.Errorf("related: open selected note: %w", err)
	}
	return nil
}

// runRelatedListing is the read-only half: three renderings of the same ranked
// results, selected by flags validateRelatedOptions has already proven are not
// combined.
func runRelatedListing(cmd *cobra.Command, root string, queryResult relatedQuery, options relatedOptions) error {
	out := cmd.OutOrStdout()
	switch {
	case options.asJSON:
		return writeRelatedJSON(out, queryResult)
	case options.paths:
		return writeRelatedPaths(out, root, queryResult.results, options.nullSep)
	default:
		return writeRelatedHuman(out, queryResult.results)
	}
}
