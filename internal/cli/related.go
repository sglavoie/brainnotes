package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	defaultRelatedLimit = 10
	maxRelatedLimit     = 100
)

type relatedOptions struct {
	modeText    string
	limit       int
	paths       bool
	nullSep     bool
	asJSON      bool
	interactive bool
	openResult  bool
}

type relatedQuery struct {
	source    recall.Document
	documents []recall.Document
	mode      recall.Mode
	weights   recall.Weights
	results   []recall.Result
}

type relatedSelection struct {
	absolutePath string
	relativePath string
}

// relatedPicker is the fzf invocation used by `related --interactive`.
// Tests replace it so they never launch an external UI.
var relatedPicker pickerFunc = RunFZF

// relatedOpenExec is the editor invocation used by `related --open`.
// Tests replace it so they never replace the test process.
var relatedOpenExec = func(editorSpec, file string) error {
	return runEditor(editorSpec, file)
}

// NewRelatedCmd returns the contextual-recall command.
func NewRelatedCmd() *cobra.Command {
	options := relatedOptions{
		modeText: string(recall.ModeBalanced),
		limit:    defaultRelatedLimit,
	}

	cmd := &cobra.Command{
		Use:           "related <note>",
		Short:         "Rank notes related to a source note",
		Long:          "Rank notes related to a source note using deterministic text, graph, metadata, and recall signals. The source may be a title, relative path, or absolute path.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelatedCommand(cmd, args, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.modeText, "mode", options.modeText, "ranking mode: balanced, rediscover, or connect")
	flags.IntVar(&options.limit, "limit", options.limit, "maximum results to return (1-100)")
	flags.BoolVar(&options.paths, "paths", false, "print only absolute result paths")
	flags.BoolVarP(&options.nullSep, "null", "0", false, "with --paths, separate paths with NUL instead of newline")
	flags.BoolVar(&options.asJSON, "json", false, "emit stable JSON with ranking details")
	flags.BoolVarP(&options.interactive, "interactive", "i", false, "choose a related note with fzf")
	flags.BoolVar(&options.openResult, "open", false, "with --interactive, open the selected note in the configured editor")
	cmd.AddCommand(NewRelatedFeedbackCmd())

	return cmd
}

// validate refuses every unusable flag combination and returns the parsed mode.
//
// The order of the checks is the order they are reported in: the flag matrix
// first, then --limit, then --mode. `bn related x --interactive --json --limit
// 0` reports the --interactive/--json conflict, not the limit.
func (options relatedOptions) validate() (recall.Mode, error) {
	if err := options.validateFlagCombos(); err != nil {
		return "", err
	}
	if options.limit < 1 || options.limit > maxRelatedLimit {
		return "", rangeValueError("related", "limit", fmt.Sprintf("between 1 and %d", maxRelatedLimit), options.limit)
	}
	mode, err := recall.ParseMode(options.modeText)
	if err != nil {
		return "", enumValueError("related", "mode", options.modeText, "balanced", "rediscover", "connect")
	}
	return mode, nil
}

// validateFlagCombos owns the mutual-exclusion matrix, split into the
// --interactive cluster and the --json/--paths/--null cluster so neither half
// grows past the threshold when a flag is added.
func (options relatedOptions) validateFlagCombos() error {
	if options.openResult && !options.interactive {
		return errors.New("related: --open requires --interactive")
	}
	if err := options.validateInteractiveCombos(); err != nil {
		return err
	}
	return options.validateOutputCombos()
}

func (options relatedOptions) validateInteractiveCombos() error {
	if !options.interactive {
		return nil
	}
	if options.asJSON {
		return errors.New("related: --interactive cannot be combined with --json")
	}
	if options.paths {
		return errors.New("related: --interactive cannot be combined with --paths")
	}
	if options.nullSep {
		return errors.New("related: --interactive cannot be combined with --null")
	}
	return nil
}

func (options relatedOptions) validateOutputCombos() error {
	if options.asJSON && options.paths {
		return errors.New("related: --json cannot be combined with --paths")
	}
	if options.asJSON && options.nullSep {
		return errors.New("related: --json cannot be combined with --null")
	}
	if options.nullSep && !options.paths {
		return errors.New("related: --null requires --paths")
	}
	return nil
}

// selectRelatedPath runs the related-notes picker. No related notes, and a
// picker that selected nothing, are both ErrNothingSelected (exit 1).
func selectRelatedPath(root string, results []recall.Result, extraArgs []string) (relatedSelection, error) {
	if len(results) == 0 {
		return relatedSelection{}, nothingSelected("related", "no related notes")
	}

	rows := make([]string, len(results))
	selectionsByRow := make(map[string]relatedSelection, len(results))
	for index, result := range results {
		absolute := filepath.Join(root, filepath.FromSlash(result.Path))
		if strings.ContainsAny(absolute, "\t\r\n") {
			return relatedSelection{}, fmt.Errorf("related: cannot select path containing a tab or newline: %q", absolute)
		}

		relationship := string(result.Relationship)
		if relationship == "" {
			relationship = "none"
		}
		visible := fmt.Sprintf("%d. [%s] %s", index+1, relationship, cleanRelatedFZFField(result.Path))
		reasons := relatedFZFReasons(result.Reasons)
		row := strings.Join([]string{absolute, visible, reasons}, "\t")
		rows[index] = row
		selectionsByRow[row] = relatedSelection{absolutePath: absolute, relativePath: result.Path}
	}

	picked, err := relatedPicker(rows, relatedFZFArgs(extraArgs))
	selected, err := requireSelection("related", picked, err)
	if err != nil {
		return relatedSelection{}, err
	}
	if len(selected) != 1 {
		return relatedSelection{}, fmt.Errorf("related: fzf returned %d selections; expected exactly one", len(selected))
	}
	selection, ok := selectionsByRow[selected[0]]
	if !ok {
		return relatedSelection{}, errors.New("related: fzf returned an invalid selection; remove output-shaping options from [fzf].args")
	}
	return selection, nil
}

func relatedFZFReasons(reasons []recall.Reason) string {
	if len(reasons) == 0 {
		return "no reasons available"
	}
	limit := len(reasons)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, limit)
	for index := 0; index < limit; index++ {
		parts[index] = fmt.Sprintf("%s: %s", reasons[index].Kind, cleanRelatedFZFField(reasons[index].Detail))
	}
	return strings.Join(parts, " | ")
}

func cleanRelatedFZFField(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}

// relatedFZFArgs is the `related --interactive` row layout: rank and
// relationship in column 2, reasons in column 3, and the absolute path hidden in
// column 1 for the preview. Injection follows the shared fzf table rules.
func relatedFZFArgs(extraArgs []string) []string {
	return injectFZFDefaults(extraArgs, []fzfDefault{
		{suppressedBy: []string{"--delimiter"}, args: []string{"--delimiter", "\t"}},
		{suppressedBy: []string{"--with-nth"}, args: []string{"--with-nth", "{2}\t{3}"}},
		{suppressedBy: []string{"--nth", "--with-nth"}, args: []string{"--nth", "1.."}},
		{suppressedBy: []string{"--tabstop"}, args: []string{"--tabstop", "1"}},
		{suppressedBy: []string{"--no-hscroll", "--hscroll-off"}, args: []string{"--no-hscroll"}},
		{suppressedBy: []string{"--preview"}, args: []string{"--preview", "cat {1}"}},
		{suppressedBy: []string{"--preview-window"}, args: []string{"--preview-window", "right:60%"}},
	})
}

func buildRelatedQuery(
	errOut io.Writer,
	command string,
	v *vault.Vault,
	sourceArg string,
	mode recall.Mode,
	rankOptions recall.RankOptions,
) (relatedQuery, error) {
	source, err := resolveNoteTarget(command, v, sourceArg)
	if err != nil {
		return relatedQuery{}, err
	}
	notes, err := loadReadableNotesTo(errOut, command, v)
	if err != nil {
		return relatedQuery{}, err
	}

	sourceDocument, err := recall.ExtractDocument(v.Root, source, recallExtractOptions(v))
	if err != nil {
		return relatedQuery{}, err
	}

	corpus, err := buildRecallCorpus(v, notes, nil, rankOptions)
	if err != nil {
		return relatedQuery{}, err
	}
	// Rank, not RankSelected: `related` is discovery, so mode eligibility and
	// dismiss/snooze suppression are exactly what the user asked for. A caller
	// that has already chosen its notes wants RankSelected instead.
	results, err := corpus.ranker.Rank(sourceDocument.Path, mode)
	if err != nil {
		return relatedQuery{}, err
	}

	return relatedQuery{
		source:    sourceDocument,
		documents: corpus.documents,
		mode:      mode,
		weights:   corpus.ranker.Weights(),
		results:   results,
	}, nil
}
