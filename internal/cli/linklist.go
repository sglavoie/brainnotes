package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// linkListOptions is the flag state of `bn links` and `bn backlinks`. The two
// commands read the same wikilink graph from opposite directions and register
// the same two flags, so they share one options struct rather than declaring a
// pair of identical ones.
type linkListOptions struct {
	nullSep bool
	verbose bool
	asJSON  bool
}

// validate holds the mutual-exclusion rules both commands enforce: NUL
// separation is a property of a path list, which neither verbose mode nor JSON
// emits, and JSON has no verbose form. The command name is a parameter because
// the refusals are otherwise identical and the message has to say which command
// produced it.
func (o linkListOptions) validate(name string) error {
	switch {
	case o.nullSep && o.verbose:
		return fmt.Errorf("%s: --null cannot be combined with --verbose", name)
	case o.asJSON && o.nullSep:
		return fmt.Errorf("%s: --json cannot be combined with --null", name)
	case o.asJSON && o.verbose:
		return fmt.Errorf("%s: --json cannot be combined with --verbose", name)
	}
	return nil
}

// writePaths renders the path list in the form the flags chose: the shared
// JSON note records, or one path per line (or NUL-separated).
func (o linkListOptions) writePaths(out io.Writer, root string, paths []string) error {
	if o.asJSON {
		return writeNotesJSON(out, root, paths)
	}
	return writePathList(out, paths, o.nullSep)
}

// linkListRun is the resolved state a link-listing command renders from: the
// selected target note, every note in the vault, and the path-aware wikilink
// graph built over them. `links` needs the graph alone; `backlinks` needs the
// note list as well, to walk each referrer's body in verbose mode.
type linkListRun struct {
	root   string
	target *vault.Note
	notes  []*vault.Note
	graph  *query.Graph
}

// newLinkListRun performs the resolution `links` and `backlinks` previously
// duplicated line for line: read the config, refuse the flag combination, open
// the vault, select the target, load the notes, and build the graph. It is the
// shared link-listing helper the two commands' thirteen duplicate windows
// collapse into.
//
// Order is behaviour and is preserved from both closures: the flag matrix is
// refused before the vault is opened, so `bn links --null --verbose` outside a
// vault is a flag error rather than a vault error.
//
// ok is false for the two cases that are a silent success rather than a target:
// a vault with no notes (which reports itself on stderr) and a picker the user
// dismissed. A nil target returned as a value would be indistinguishable from a
// resolved one, which is the same reason `resolveUnsetPaths` reports
// cancellation through a third return rather than a nil sentinel.
func newLinkListRun(cmd *cobra.Command, name string, args []string, opts linkListOptions) (*linkListRun, bool, error) {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return nil, false, fmt.Errorf("%s: no config in context", name)
	}
	if err := opts.validate(name); err != nil {
		return nil, false, err
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return nil, false, err
	}

	target, ok, err := resolveLinkListTarget(cmd, v, cfg.Fzf.Args, name, args)
	if err != nil || !ok {
		return nil, false, err
	}

	notes, err := v.Notes()
	if err != nil {
		return nil, false, err
	}
	return &linkListRun{
		root:   v.Root,
		target: target,
		notes:  notes,
		graph:  query.BuildGraphForRoot(v.Root, notes),
	}, true, nil
}

// resolveLinkListTarget returns the note whose links are being listed: the sole
// positional argument put through the shared NFC-aware resolver, or an fzf
// selection when there is none. Both commands take `cobra.MaximumNArgs(1)`, so
// "no argument" is the only way into the picker.
//
// It reports (nil, false, nil) for an empty vault and for a dismissed picker.
func resolveLinkListTarget(
	cmd *cobra.Command,
	v *vault.Vault,
	fzfArgs []string,
	name string,
	args []string,
) (*vault.Note, bool, error) {
	if len(args) > 0 {
		target, err := resolveNoteTarget(name, v, args[0])
		if err != nil {
			return nil, false, err
		}
		return target, true, nil
	}

	paths, err := collectNotePaths(v)
	if err != nil {
		return nil, false, err
	}
	if len(paths) == 0 {
		return nil, false, nothingSelected(name, "no notes in vault")
	}
	picked, err := notePicker(paths, append([]string(nil), fzfArgs...))
	selected, err := requireSelection(name, picked, err)
	if err != nil {
		return nil, false, err
	}
	return &vault.Note{Path: selected[0]}, true, nil
}
