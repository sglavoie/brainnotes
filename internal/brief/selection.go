package brief

import (
	"errors"
	"fmt"
	"os"
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

	documents, err := recall.ExtractCorpus(absRoot, notes, recall.ExtractOptions{
		MetadataProperties: options.MetadataProperties,
	})
	if err != nil {
		return SelectionCorpus{}, err
	}

	sourceByPath := make(map[string]selectionSourceNote, len(notes))
	for _, note := range notes {
		if note == nil {
			continue
		}
		relPath, err := selectionRelativePath(absRoot, note.Path)
		if err != nil {
			return SelectionCorpus{}, err
		}
		info, err := os.Stat(note.Path)
		if err != nil {
			return SelectionCorpus{}, fmt.Errorf("brief: stat note %q: %w", note.Path, err)
		}
		typeValues, err := noteTypeValues(note)
		if err != nil {
			return SelectionCorpus{}, err
		}
		sourceByPath[relPath] = selectionSourceNote{
			note:       note,
			modifiedAt: info.ModTime().UTC(),
			typeValues: typeValues,
		}
	}

	selectedDocuments := make([]recall.Document, 0, len(documents))
	notesOut := make([]CorpusNote, 0, len(documents))
	excerpts := make(map[string]string, len(documents))
	for _, document := range documents {
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

		createdAt := document.CreatedAt
		if !createdAt.IsZero() {
			createdAt = createdAt.UTC()
		}
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
		Explanation: "Most recently modified substantive note.",
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

	strict := make([]rediscoverCandidate, 0, len(corpus.Notes))
	relaxed := make([]rediscoverCandidate, 0, len(corpus.Notes))
	for _, note := range corpus.Notes {
		if continuePath != "" && note.Ref.RelPath == continuePath {
			continue
		}
		candidate, inCooldown, err := selector.rediscoverCandidate(note, now)
		if err != nil {
			return SlotResult{}, err
		}
		relaxed = append(relaxed, candidate)
		if !inCooldown {
			strict = append(strict, candidate)
		}
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
			Explanation: "Longest-unseen eligible note.",
			Excerpt:     selector.excerptForPath(chosen.note.Ref.RelPath),
		}), nil
	}

	sortRediscoverCandidates(relaxed)
	chosen := relaxed[0]
	return cardSlot(SlotRediscover, Card{
		Slot:        SlotRediscover,
		Source:      chosen.note.Ref,
		Explanation: "Cooldown relaxed because no alternative rediscover candidate is available.",
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
	now := selector.nowTime()
	strict := make([]connectCandidate, 0, len(results))
	relaxed := make([]connectCandidate, 0, len(results))
	for _, result := range results {
		note, ok := corpusByPath[result.Path]
		if !ok {
			continue
		}
		candidate := connectCandidate{result: result, note: note}
		relaxed = append(relaxed, candidate)

		inCooldown, err := selector.connectPairInCooldown(sourcePath, result.Path, now)
		if err != nil {
			return SlotResult{}, err
		}
		if !inCooldown {
			strict = append(strict, candidate)
		}
	}
	if len(relaxed) == 0 {
		return unavailableSlot(SlotConnect, "No connect candidate is available."), nil
	}
	chosen := strict
	explanation := "Related note suggested by ModeConnect ranking."
	if len(chosen) == 0 {
		chosen = relaxed
		explanation = "Cooldown relaxed because no alternative connect candidate is available."
	}

	selected := chosen[0]
	reasons := connectReasonStrings(selected.result.Reasons)
	if len(reasons) > 0 {
		explanation = reasons[0]
	}
	candidateRef := selected.note.Ref
	return cardSlot(SlotConnect, Card{
		Slot:        SlotConnect,
		Source:      sourceNote.Ref,
		Candidate:   &candidateRef,
		Explanation: explanation,
		Excerpt:     selector.excerptForPath(selected.note.Ref.RelPath),
		Metadata: CandidateMetadata{
			Score:        selected.result.Score,
			Relationship: string(selected.result.Relationship),
			Bridge:       connectBridge(selected.result),
			Reasons:      reasons,
		},
	}), nil
}
