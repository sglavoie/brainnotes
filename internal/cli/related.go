package cli

import (
	"errors"
	"fmt"
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

func validateRelatedOptions(options relatedOptions) (recall.Mode, error) {
	if options.openResult && !options.interactive {
		return "", errors.New("related: --open requires --interactive")
	}
	if options.interactive && options.asJSON {
		return "", errors.New("related: --interactive cannot be used with --json")
	}
	if options.interactive && options.paths {
		return "", errors.New("related: --interactive cannot be used with --paths")
	}
	if options.interactive && options.nullSep {
		return "", errors.New("related: --interactive cannot be used with --null")
	}
	if options.asJSON && options.paths {
		return "", errors.New("related: --json cannot be used with --paths")
	}
	if options.asJSON && options.nullSep {
		return "", errors.New("related: --json cannot be used with --null")
	}
	if options.nullSep && !options.paths {
		return "", errors.New("related: --null requires --paths")
	}
	if options.limit < 1 || options.limit > maxRelatedLimit {
		return "", fmt.Errorf("related: --limit must be between 1 and %d (got %d)", maxRelatedLimit, options.limit)
	}
	mode, err := recall.ParseMode(options.modeText)
	if err != nil {
		return "", fmt.Errorf(
			"related: --mode must be balanced, rediscover, or connect (got %q)",
			options.modeText,
		)
	}
	return mode, nil
}

func selectRelatedPath(root string, results []recall.Result, extraArgs []string) (relatedSelection, error) {
	if len(results) == 0 {
		return relatedSelection{}, nil
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

	selected, err := relatedPicker(rows, relatedFZFArgs(extraArgs))
	if err != nil {
		return relatedSelection{}, err
	}
	if len(selected) == 0 {
		return relatedSelection{}, nil
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

func relatedFZFArgs(extraArgs []string) []string {
	hasFlag := func(flag string) bool {
		for _, arg := range extraArgs {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				return true
			}
		}
		return false
	}

	hasWithNth := hasFlag("--with-nth")
	hasNth := hasFlag("--nth")
	var injected []string
	if !hasFlag("--delimiter") {
		injected = append(injected, "--delimiter", "\t")
	}
	if !hasWithNth {
		injected = append(injected, "--with-nth", "{2}\t{3}")
	}
	if !hasNth && !hasWithNth {
		injected = append(injected, "--nth", "1..")
	}
	if !hasFlag("--tabstop") {
		injected = append(injected, "--tabstop", "1")
	}
	if !hasFlag("--no-hscroll") && !hasFlag("--hscroll-off") {
		injected = append(injected, "--no-hscroll")
	}
	if !hasFlag("--preview") {
		injected = append(injected, "--preview", "cat {1}")
	}
	if !hasFlag("--preview-window") {
		injected = append(injected, "--preview-window", "right:60%")
	}
	return append(injected, extraArgs...)
}

func buildRelatedQuery(
	v *vault.Vault,
	sourceArg string,
	mode recall.Mode,
	rankOptions recall.RankOptions,
) (relatedQuery, error) {
	source, err := resolveNoteTarget(v, sourceArg)
	if err != nil {
		return relatedQuery{}, err
	}
	notes, err := v.Notes()
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
