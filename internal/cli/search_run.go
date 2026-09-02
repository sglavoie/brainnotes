package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// searchOptions is the flag state of one `bn search` invocation. `ff` is the
// shared filter surface rather than a copy of it, so `search` keeps routing
// through findflags.go's single execution path.
type searchOptions struct {
	regex         bool
	caseSensitive bool
	nullSep       bool
	interactive   bool
	contextN      int
	count         bool
	openResult    bool
	ff            *findFlags
}

// validate holds every mutual-exclusion rule `search` enforces. The first rule
// cannot be derived from the option fields alone — whether a find flag was set
// is a property of the *cobra.Command — so it arrives as a parameter.
func (o searchOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("search: note arguments and find flags are mutually exclusive")
	}
	if err := o.validateContextCombos(); err != nil {
		return err
	}
	if err := o.validateCountCombos(); err != nil {
		return err
	}
	if o.openResult && o.nullSep {
		return errors.New("search: --open cannot be combined with --null")
	}
	return nil
}

// validateContextCombos rejects the four modes --context cannot share output with.
func (o searchOptions) validateContextCombos() error {
	if o.contextN <= 0 {
		return nil
	}
	switch {
	case o.count:
		return errors.New("search: --context cannot be combined with --count")
	case o.interactive:
		return errors.New("search: --context cannot be combined with --interactive")
	case o.nullSep:
		return errors.New("search: --context cannot be combined with --null")
	case o.openResult:
		return errors.New("search: --context cannot be combined with --open")
	}
	return nil
}

// validateCountCombos rejects the three modes --count cannot share output with.
func (o searchOptions) validateCountCombos() error {
	if !o.count {
		return nil
	}
	switch {
	case o.interactive:
		return errors.New("search: --count cannot be combined with --interactive")
	case o.nullSep:
		return errors.New("search: --count cannot be combined with --null")
	case o.openResult:
		return errors.New("search: --count cannot be combined with --open")
	}
	return nil
}

// searchRun is the resolved state the five output modes share: the selected
// notes plus everything needed to render or act on them. `matcher` is nil
// exactly when `pattern` is empty, which only interactive mode allows.
type searchRun struct {
	cmd     *cobra.Command
	cfg     *config.Config
	opts    searchOptions
	root    string
	notes   []*vault.Note
	pattern string
	matcher func(string) bool
}

func runSearchCommand(cmd *cobra.Command, args []string, opts searchOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("search: no config in context")
	}

	pattern, noteArgs, err := splitSearchArgs(args, opts.interactive)
	if err != nil {
		return err
	}

	// Order is load-bearing: an unparseable pattern is reported before the flag
	// matrix, and both before the vault is opened.
	var matcher func(string) bool
	if pattern != "" {
		matcher, err = buildMatcher(pattern, opts.regex, opts.caseSensitive)
		if err != nil {
			return err
		}
	}

	anyFlagSet := anyQueryFlagSet(cmd)
	if err := opts.validate(noteArgs, anyFlagSet); err != nil {
		return err
	}

	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	notes, err := selectSearchNotes(cmd, cfg, v, opts, noteArgs, anyFlagSet)
	if err != nil {
		return err
	}

	return runSearchMode(&searchRun{
		cmd:     cmd,
		cfg:     cfg,
		opts:    opts,
		root:    v.Root,
		notes:   notes,
		pattern: pattern,
		matcher: matcher,
	})
}

// runSearchMode dispatches to the one output mode the flags select. The order
// is the order validate() leaves reachable: --open is legal with -i, so
// interactive is tested first and owns that combination.
func runSearchMode(r *searchRun) error {
	switch {
	case r.opts.interactive:
		return runSearchInteractive(r)
	case r.opts.contextN > 0:
		return runSearchContext(r)
	case r.opts.count:
		return runSearchCount(r)
	case r.opts.openResult:
		return runSearchOpen(r)
	default:
		return runSearchPaths(r)
	}
}

// splitSearchArgs splits the positional arguments into the pattern and the note
// arguments. Only interactive mode may run without a pattern.
func splitSearchArgs(args []string, interactive bool) (pattern string, noteArgs []string, err error) {
	if len(args) == 0 {
		if !interactive {
			return "", nil, errors.New("search: pattern required; use -i for interactive mode without a pattern")
		}
		return "", nil, nil
	}
	return args[0], args[1:], nil
}

// anyQueryFlagSet reports whether any find-style scoping flag was set on cmd.
// queryFlagNames (replace.go) must list every one of them or the note-args /
// find-flags mutual exclusion silently stops firing.
func anyQueryFlagSet(cmd *cobra.Command) bool {
	for _, name := range queryFlagNames {
		if f := cmd.Flag(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}

// selectSearchNotes resolves the note set to search: explicit note arguments,
// the find-flag filter, or the whole vault.
func selectSearchNotes(
	cmd *cobra.Command,
	cfg *config.Config,
	v *vault.Vault,
	opts searchOptions,
	noteArgs []string,
	anyFlagSet bool,
) ([]*vault.Note, error) {
	switch {
	case len(noteArgs) > 0:
		return searchNotesByArgs(cmd, v, noteArgs, opts.nullSep)
	case anyFlagSet:
		return searchNotesByFlags(cmd, cfg, v, opts.ff)
	default:
		return v.Notes()
	}
}

func searchNotesByArgs(cmd *cobra.Command, v *vault.Vault, noteArgs []string, nullSep bool) ([]*vault.Note, error) {
	paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
	if err != nil {
		return nil, err
	}
	allNotes, err := v.Notes()
	if err != nil {
		return nil, err
	}
	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	var notes []*vault.Note
	for _, n := range allNotes {
		if pathSet[n.Path] {
			notes = append(notes, n)
		}
	}
	return notes, nil
}

func searchNotesByFlags(cmd *cobra.Command, cfg *config.Config, v *vault.Vault, ff *findFlags) ([]*vault.Note, error) {
	allNotes, err := v.Notes()
	if err != nil {
		return nil, err
	}
	sel, err := ff.selectNotes(cmd.Context(), cfg, v.Root, allNotes, nil)
	if err != nil {
		return nil, err
	}
	return sel.notes, nil
}

// runSearchInteractive pipes the matching body lines into fzf. With no pattern
// the corpus is every body line and fzf itself is the filter.
func runSearchInteractive(r *searchRun) error {
	corpusLines, err := buildInteractiveLines(r.notes, r.root, r.matcher)
	if err != nil {
		return err
	}
	fzfArgs := searchFzfArgs(r.cfg, r.pattern, r.opts)
	if r.opts.openResult {
		return runSearchInteractiveOpen(r, corpusLines, fzfArgs)
	}
	selected, err := RunFZFLines(corpusLines, fzfArgs)
	if err != nil {
		return err
	}
	return writeSearchPaths(r.cmd.OutOrStdout(), selected, r.opts.nullSep)
}

// runSearchInteractiveOpen is `-i --open`: fzf selects the line, the editor
// opens it. Selecting nothing is a success that opens nothing.
func runSearchInteractiveOpen(r *searchRun, corpusLines, fzfArgs []string) error {
	selected, err := runFZFLinesSelections(corpusLines, fzfArgs)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}
	return openSearchMatch(r.cfg, selected[0].path, selected[0].line)
}

// runSearchContext prints grep-style context blocks, separated by "--" between
// notes as well as between non-adjacent windows within one note.
func runSearchContext(r *searchRun) error {
	sort.Slice(r.notes, func(i, j int) bool { return r.notes[i].Path < r.notes[j].Path })
	out := r.cmd.OutOrStdout()
	first := true
	for _, n := range r.notes {
		var buf bytes.Buffer
		if err := contextOutput(&buf, n, r.root, r.matcher, r.opts.contextN); err != nil {
			return err
		}
		if buf.Len() == 0 {
			continue
		}
		if !first {
			fmt.Fprintln(out, "--")
		}
		first = false
		if _, err := out.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func runSearchCount(r *searchRun) error {
	results, err := collectSearchCounts(r.notes, r.root, r.matcher)
	if err != nil {
		return err
	}
	return writeSearchCounts(r.cmd.OutOrStdout(), results)
}

// runSearchOpen opens the first match of the lexicographically first matching
// note. No match is a success that opens nothing.
func runSearchOpen(r *searchRun) error {
	var matched []searchMatch
	for _, n := range r.notes {
		line, ok, err := firstMatchingLine(n.Path, r.matcher)
		if err != nil {
			return err
		}
		if ok {
			matched = append(matched, searchMatch{path: n.Path, line: line})
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].path < matched[j].path })
	if len(matched) == 0 {
		return nil
	}
	return openSearchMatch(r.cfg, matched[0].path, matched[0].line)
}

// runSearchPaths is the default mode: one absolute path per matching note.
func runSearchPaths(r *searchRun) error {
	var matched []*vault.Note
	for _, n := range r.notes {
		body, err := n.Body()
		if err != nil {
			return err
		}
		if bodyHasMatch(body, r.matcher) {
			matched = append(matched, n)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })

	paths := make([]string, len(matched))
	for i, n := range matched {
		paths[i] = n.Path
	}
	return writeSearchPaths(r.cmd.OutOrStdout(), paths, r.opts.nullSep)
}

// openSearchMatch resolves the editor and opens path at line. Resolution is
// deliberately lazy — it runs only once a match exists — so `--open` against a
// vault with no match succeeds without an editor configured.
func openSearchMatch(cfg *config.Config, path string, line int) error {
	editorSpec, err := editutil.Spec(cfg.Open.Editor, os.Getenv("EDITOR"))
	if errors.Is(err, editutil.ErrNoEditorConfigured) {
		return errors.New("search: --open requires [open].editor or $EDITOR")
	}
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if err := searchOpenExec(editorSpec, path, line); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	return nil
}
