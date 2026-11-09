package brief

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// SelectionCorpusOptions controls how BuildSelectionCorpus filters and enriches
// loaded notes before deterministic slot selection.
type SelectionCorpusOptions struct {
	MetadataProperties []string
	ExcludeFolders     []string
	ExcludePaths       []string
	ExcludeTypes       []string
}

// SelectionCorpus is the prepared in-memory dataset selectors operate on.
type SelectionCorpus struct {
	Snapshot CorpusSnapshot

	documents []recall.Document
	graph     *query.Graph
	excerpts  map[string]string
}

// RecallDocuments returns a defensive deep copy of the prepared recall corpus
// documents used by deterministic brief selection.
func (corpus SelectionCorpus) RecallDocuments() []recall.Document {
	if len(corpus.documents) == 0 {
		return nil
	}
	documents := make([]recall.Document, len(corpus.documents))
	for i, document := range corpus.documents {
		documents[i] = cloneRecallDocument(document)
	}
	return documents
}

// RecallGraph returns the graph paired with the prepared selection corpus.
func (corpus SelectionCorpus) RecallGraph() *query.Graph {
	return corpus.graph
}

// DeterministicSelectorOptions configures deterministic selection behavior.
type DeterministicSelectorOptions struct {
	State StateStore
	Now   func() time.Time
}

// DeterministicSelector selects Continue, Rediscover, and Connect cards with
// stable ordering and cooldown-aware filtering.
type DeterministicSelector struct {
	state StateStore
	now   func() time.Time

	ranker       *recall.Ranker
	notesByPath  map[string]CorpusNote
	excerptsByID map[string]string
	blocksByPath map[string][]recall.MarkdownBlock
}

var _ Selector = (*DeterministicSelector)(nil)

type selectionSourceNote struct {
	note       *vault.Note
	modifiedAt time.Time
	typeValues []string
}

type rediscoverCandidate struct {
	note            CorpusNote
	effectiveAt     time.Time
	connectivity    int
	metadataPresent int
}

type connectCandidate struct {
	result recall.Result
	note   CorpusNote
}

// BuildSelectionCorpus adapts loaded notes and recall documents into the
// deterministic corpus used by brief selection.
func BuildSelectionCorpus(
	root string,
	notes []*vault.Note,
	graph *query.Graph,
	options SelectionCorpusOptions,
) (SelectionCorpus, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return SelectionCorpus{}, errors.New("brief: empty corpus root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return SelectionCorpus{}, fmt.Errorf("brief: resolve corpus root %q: %w", root, err)
	}

	folderExclusions, err := normalizeFolderExclusions(options.ExcludeFolders)
	if err != nil {
		return SelectionCorpus{}, err
	}
	typeExclusions := normalizeTypeExclusions(options.ExcludeTypes)
	pathExclusions := make(map[string]bool, len(options.ExcludePaths))
	for _, path := range options.ExcludePaths {
		pathExclusions[filepath.ToSlash(filepath.Clean(path))] = true
	}

	documents, err := recall.ExtractCorpus(absRoot, notes, recall.ExtractOptions{
		MetadataProperties: options.MetadataProperties,
	})
	if err != nil {
		return SelectionCorpus{}, err
	}

	sourceByPath, err := selectionSources(absRoot, notes)
	if err != nil {
		return SelectionCorpus{}, err
	}

	return selectCorpusDocuments(documents, sourceByPath, graph, folderExclusions, pathExclusions, typeExclusions)
}

func selectCorpusDocuments(documents []recall.Document, sourceByPath map[string]selectionSourceNote, graph *query.Graph,
	folderExclusions []string, pathExclusions map[string]bool, typeExclusions map[string]struct{}) (SelectionCorpus, error) {
	selectedDocuments := make([]recall.Document, 0, len(documents))
	notesOut := make([]CorpusNote, 0, len(documents))
	excerpts := make(map[string]string, len(documents))
	for _, document := range documents {
		if pathExclusions[document.Path] {
			continue
		}
		source, ok := sourceByPath[document.Path]
		if !ok {
			return SelectionCorpus{}, fmt.Errorf("brief: document %q missing source note", document.Path)
		}
		if len(document.Blocks) == 0 {
			continue
		}
		if pathExcludedByPrefix(document.Path, folderExclusions) {
			continue
		}
		if hasExcludedType(source.typeValues, typeExclusions) {
			continue
		}

		createdAt := document.CreatedAt.UTC()
		inbound := 0
		outbound := 0
		if graph != nil {
			inbound = graph.InDegreeForNote(source.note)
			outbound = graph.OutDegreeForNote(source.note)
		}
		note := CorpusNote{
			Ref: NoteRef{
				RelPath: document.Path,
				Title:   document.Title,
			},
			ModifiedAt:     source.modifiedAt,
			CreatedAt:      createdAt,
			LastActivityAt: source.modifiedAt,
			InboundLinks:   inbound,
			OutboundLinks:  outbound,
			BlockCount:     len(document.Blocks),
			MetadataValues: metadataValuesByProperty(document.Metadata),
		}
		notesOut = append(notesOut, note)
		selectedDocuments = append(selectedDocuments, document)
		excerpts[document.Path] = RepresentativeExcerpt(document)
	}
	sort.SliceStable(notesOut, func(i, j int) bool {
		return notesOut[i].Ref.RelPath < notesOut[j].Ref.RelPath
	})
	return SelectionCorpus{
		Snapshot: CorpusSnapshot{
			GeneratedAt: time.Now().UTC(),
			Notes:       notesOut,
		},
		documents: selectedDocuments,
		graph:     graph,
		excerpts:  excerpts,
	}, nil
}

func selectionSources(absRoot string, notes []*vault.Note) (map[string]selectionSourceNote, error) {
	sourceByPath := make(map[string]selectionSourceNote, len(notes))
	for _, note := range notes {
		if note == nil {
			continue
		}
		relPath, err := selectionRelativePath(absRoot, note.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(note.Path)
		if err != nil {
			return nil, fmt.Errorf("brief: stat note %q: %w", note.Path, err)
		}
		typeValues, err := noteTypeValues(note)
		if err != nil {
			return nil, err
		}
		sourceByPath[relPath] = selectionSourceNote{
			note:       note,
			modifiedAt: info.ModTime().UTC(),
			typeValues: typeValues,
		}
	}

	return sourceByPath, nil
}

// NewDeterministicSelector builds a selector bound to a prepared selection corpus.
func NewDeterministicSelector(corpus SelectionCorpus, options DeterministicSelectorOptions) (*DeterministicSelector, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	notesByPath := make(map[string]CorpusNote, len(corpus.Snapshot.Notes))
	for _, note := range corpus.Snapshot.Notes {
		notesByPath[note.Ref.RelPath] = cloneCorpusNote(note)
	}
	excerpts := make(map[string]string, len(corpus.excerpts))
	for path, excerpt := range corpus.excerpts {
		excerpts[path] = excerpt
	}
	blocks := make(map[string][]recall.MarkdownBlock, len(corpus.documents))
	for _, document := range corpus.documents {
		blocks[document.Path] = append([]recall.MarkdownBlock(nil), document.Blocks...)
	}

	var ranker *recall.Ranker
	if len(corpus.documents) > 0 {
		var err error
		ranker, err = recall.BuildRanker(corpus.documents, corpus.graph, recall.RankOptions{Now: now})
		if err != nil {
			return nil, err
		}
	}
	return &DeterministicSelector{
		state:        options.State,
		now:          now,
		ranker:       ranker,
		notesByPath:  notesByPath,
		excerptsByID: excerpts,
		blocksByPath: blocks,
	}, nil
}

// SelectContinue picks the most recent substantive note deterministically.
func (selector *DeterministicSelector) SelectContinue(corpus CorpusSnapshot) (SlotResult, error) {
	if selector == nil {
		return SlotResult{}, errors.New("brief: nil selector")
	}
	candidates := append([]CorpusNote(nil), corpus.Notes...)
	if len(candidates) == 0 {
		return unavailableSlot(SlotContinue, "No substantive notes are available."), nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].ModifiedAt.Equal(candidates[j].ModifiedAt) {
			return candidates[i].ModifiedAt.After(candidates[j].ModifiedAt)
		}
		if candidates[i].BlockCount != candidates[j].BlockCount {
			return candidates[i].BlockCount > candidates[j].BlockCount
		}
		return candidates[i].Ref.RelPath < candidates[j].Ref.RelPath
	})
	chosen := candidates[0]
	return cardSlot(SlotContinue, Card{
		Slot:        SlotContinue,
		Source:      chosen.Ref,
		Explanation: "Your most recently edited note.",
		Excerpt:     selector.excerptForPath(chosen.Ref.RelPath),
	}), nil
}

// SelectRediscover picks the longest-unseen eligible note using deterministic
// cooldown filtering and stable tie-breakers.
func (selector *DeterministicSelector) SelectRediscover(corpus CorpusSnapshot, currentContinue *Card) (SlotResult, error) {
	if selector == nil {
		return SlotResult{}, errors.New("brief: nil selector")
	}
	now := selector.nowTime()
	continuePath := ""
	if currentContinue != nil {
		normalizedPath, err := normalizeBriefPath(currentContinue.Source.RelPath)
		if err != nil {
			return SlotResult{}, fmt.Errorf("brief: continue source path: %w", err)
		}
		continuePath = normalizedPath
	}

	strict, relaxed, err := selector.rediscoverCandidates(corpus.Notes, continuePath, now)
	if err != nil {
		return SlotResult{}, err
	}
	if len(relaxed) == 0 {
		return unavailableSlot(SlotRediscover, "No rediscover candidate is available."), nil
	}

	sortRediscoverCandidates(strict)
	if len(strict) > 0 {
		chosen := strict[0]
		return cardSlot(SlotRediscover, Card{
			Slot:        SlotRediscover,
			Source:      chosen.note.Ref,
			Explanation: "This note has been away from your attention longest.",
			Excerpt:     selector.excerptForPath(chosen.note.Ref.RelPath),
		}), nil
	}

	sortRediscoverCandidates(relaxed)
	chosen := relaxed[0]
	return cardSlot(SlotRediscover, Card{
		Slot:        SlotRediscover,
		Source:      chosen.note.Ref,
		Explanation: "Another look at this note may bring a fresh perspective.",
		Excerpt:     selector.excerptForPath(chosen.note.Ref.RelPath),
	}), nil
}

// SelectConnect ranks related notes in ModeConnect and applies pair cooldowns.
func (selector *DeterministicSelector) SelectConnect(corpus CorpusSnapshot, continueSource NoteRef) (SlotResult, error) {
	if selector == nil {
		return SlotResult{}, errors.New("brief: nil selector")
	}
	if selector.ranker == nil {
		return unavailableSlot(SlotConnect, "No connect candidate is available."), nil
	}
	sourcePath, err := normalizeBriefPath(continueSource.RelPath)
	if err != nil {
		return SlotResult{}, fmt.Errorf("brief: continue source path: %w", err)
	}
	corpusByPath := make(map[string]CorpusNote, len(corpus.Notes))
	for _, note := range corpus.Notes {
		corpusByPath[note.Ref.RelPath] = note
	}
	sourceNote, ok := corpusByPath[sourcePath]
	if !ok {
		return unavailableSlot(SlotConnect, "Continue source is not available for Connect."), nil
	}

	results, err := selector.ranker.Rank(sourcePath, recall.ModeConnect)
	if err != nil {
		return SlotResult{}, err
	}
	strict, relaxed, err := selector.connectCandidates(results, corpusByPath, sourcePath)
	if err != nil {
		return SlotResult{}, err
	}
	if len(relaxed) == 0 {
		return unavailableSlot(SlotConnect, "No connect candidate is available."), nil
	}
	chosen := strict
	explanation := "Read this alongside your current note for another angle."
	if len(chosen) == 0 {
		chosen = relaxed
		explanation = "A connection worth returning to today."
	}

	return selector.connectCard(sourceNote.Ref, chosen[0], explanation), nil
}

func (selector *DeterministicSelector) connectCard(source NoteRef, selected connectCandidate, explanation string) SlotResult {
	reasons := connectReasonStrings(selected.result.Reasons)
	if len(reasons) > 0 {
		explanation = reasons[0]
	} else if selected.result.Relationship == recall.RelationshipDirect {
		explanation = "These notes link directly."
	} else if bridge := connectBridge(selected.result); bridge != "" {
		explanation = "These notes meet through " + strings.TrimSuffix(path.Base(bridge), ".md") + "."
	}
	candidateRef := selected.note.Ref
	return cardSlot(SlotConnect, Card{
		Slot:        SlotConnect,
		Source:      source,
		Candidate:   &candidateRef,
		Explanation: explanation,
		Excerpt:     selector.connectionCardExcerpt(selected.result, source),
		Metadata: CandidateMetadata{
			Score:        selected.result.Score,
			Relationship: string(selected.result.Relationship),
			Bridge:       connectBridge(selected.result),
			Reasons:      reasons,
		},
	})
}
