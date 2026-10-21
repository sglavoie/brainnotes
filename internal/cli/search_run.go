package cli

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	// contextSet records that --context was given at all, so `-C 0` (match
	// lines only, grep-style) is a context run rather than the paths mode.
	contextSet bool
	count      bool
	openResult bool
	asJSON     bool
	// limit caps how many matching notes are reported; 0 is no cap.
	limit int
	ff    *findFlags
}

// validate holds every mutual-exclusion rule `search` enforces. The first rule
// cannot be derived from the option fields alone — whether a find flag was set
// is a property of the *cobra.Command — so it arrives as a parameter.
func (o searchOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("search: note arguments cannot be combined with find flags")
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
	if err := o.validateLimitCombos(); err != nil {
		return err
	}
	return o.validateJSONCombos()
}

// validateJSONCombos rejects the modes --json cannot share output with. The
// JSON records already carry each note's count and matching lines, so --count
// and --context have nothing to add.
func (o searchOptions) validateJSONCombos() error {
	if !o.asJSON {
		return nil
	}
	switch {
	case o.interactive:
		return errors.New("search: --json cannot be combined with --interactive")
	case o.openResult:
		return errors.New("search: --json cannot be combined with --open")
	case o.nullSep:
		return errors.New("search: --json cannot be combined with --null")
	case o.count:
		return errors.New("search: --json cannot be combined with --count")
	case o.contextMode():
		return errors.New("search: --json cannot be combined with --context")
	}
	return nil
}

// validateLimitCombos rejects the two modes that report no list to cap: -i
// hands the whole corpus to fzf, and --open opens only the first match.
func (o searchOptions) validateLimitCombos() error {
	if o.limit == 0 {
		return nil
	}
	switch {
	case o.interactive:
		return errors.New("search: --limit cannot be combined with --interactive")
	case o.openResult:
		return errors.New("search: --limit cannot be combined with --open")
	}
	return nil
}

// contextMode reports whether --context selects the grep-style context output:
// the flag was given, even as 0, or a positive count was set directly.
func (o searchOptions) contextMode() bool {
	return o.contextSet || o.contextN > 0
}

// validateContextCombos rejects a negative --context and the four modes
// --context cannot share output with.
func (o searchOptions) validateContextCombos() error {
	if o.contextN < 0 {
		return rangeValueError("search", "context", "0 or more", o.contextN)
	}
	if !o.contextMode() {
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

	opts.contextSet = cmd.Flags().Changed("context")
	if err := validateLimitFlag(cmd, "search", opts.limit); err != nil {
		return err
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
	case r.opts.contextMode():
		return runSearchContext(r)
	case r.opts.count:
		return runSearchCount(r)
	case r.opts.openResult:
		return runSearchOpen(r)
	case r.opts.asJSON:
		return runSearchJSON(r)
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
		return loadReadableNotes(cmd, v)
	}
}

func searchNotesByArgs(cmd *cobra.Command, v *vault.Vault, noteArgs []string, nullSep bool) ([]*vault.Note, error) {
	paths, err := resolveNoteInputs("search", v.Root, noteArgs, cmd.InOrStdin(), nullSep)
	if err != nil {
		return nil, err
	}
	allNotes, err := loadReadableNotes(cmd, v)
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
	allNotes, err := loadReadableNotes(cmd, v)
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
	picked, err := RunFZFLines(corpusLines, fzfArgs)
	selected, err := requireSelection("search", picked, err)
	if err != nil {
		return err
	}
	return writePathList(r.cmd.OutOrStdout(), selected, r.opts.nullSep)
}

// runSearchInteractiveOpen is `-i --open`: fzf selects the line, the editor
// opens it. Selecting nothing is a negative answer (exit 1) that opens nothing.
func runSearchInteractiveOpen(r *searchRun, corpusLines, fzfArgs []string) error {
	selected, err := runFZFLinesSelections(corpusLines, fzfArgs)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return nothingSelected("search", "nothing selected")
	}
	return openSearchMatch(r.cfg, selected[0].path, selected[0].line)
}

// runSearchContext prints grep-style context blocks, separated by "--" between
// notes as well as between non-adjacent windows within one note.
func runSearchContext(r *searchRun) error {
	sort.Slice(r.notes, func(i, j int) bool { return r.notes[i].Path < r.notes[j].Path })
	out := r.cmd.OutOrStdout()
	first := true
	printed := 0
	for _, n := range r.notes {
		if r.opts.limit > 0 && printed == r.opts.limit {
			break
		}
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
		printed++
		if _, err := out.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	if first {
		return noMatch("search")
	}
	return nil
}

func runSearchCount(r *searchRun) error {
	results, err := collectSearchCounts(r.notes, r.root, r.matcher)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return noMatch("search")
	}
	return writeSearchCounts(r.cmd.OutOrStdout(), capResults(results, r.opts.limit))
}

// runSearchOpen opens the first match of the lexicographically first matching
// note. No match is a negative answer (exit 1) that opens nothing.
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
		return noMatch("search")
	}
	return openSearchMatch(r.cfg, matched[0].path, matched[0].line)
}

// runSearchPaths is the default mode: one absolute path per matching note. No
// match is grep's exit 1.
func runSearchPaths(r *searchRun) error {
	paths, err := matchingSearchPaths(r)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return noMatch("search")
	}
	return writePathList(r.cmd.OutOrStdout(), paths, r.opts.nullSep)
}

// matchingSearchPaths is the sorted absolute paths of the notes whose body has
// a matching line: the set the paths and JSON modes both report.
func matchingSearchPaths(r *searchRun) ([]string, error) {
	var paths []string
	for _, n := range r.notes {
		body, err := n.Body()
		if err != nil {
			return nil, err
		}
		if bodyHasMatch(body, r.matcher) {
			paths = append(paths, n.Path)
		}
	}
	sort.Strings(paths)
	return capResults(paths, r.opts.limit), nil
}

// openSearchMatch resolves the editor and opens path at line. Resolution is
// deliberately lazy — it runs only once a match exists — so `--open` against a
// vault with no match succeeds without an editor configured.
func openSearchMatch(cfg *config.Config, path string, line int) error {
	editorSpec, err := resolveEditorSpec("search", cfg.Open.Editor)
	if err != nil {
		return err
	}
	if err := searchOpenExec(editorSpec, path, line); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	return nil
}
