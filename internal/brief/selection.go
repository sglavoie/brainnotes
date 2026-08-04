package brief

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
		excerpts[document.Path] = excerptForDocument(document)
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

func (selector *DeterministicSelector) rediscoverCandidate(note CorpusNote, now time.Time) (rediscoverCandidate, bool, error) {
	effective := note.ModifiedAt
	if note.LastActivityAt.After(effective) {
		effective = note.LastActivityAt
	}
	inCooldown := false
	if selector.state != nil {
		lastExposure, seenExposure, err := selector.state.LastNoteExposure(note.Ref.RelPath)
		if err != nil {
			return rediscoverCandidate{}, false, err
		}
		if seenExposure {
			if lastExposure.After(effective) {
				effective = lastExposure
			}
			inCooldown = activeCooldown(lastExposure, now)
		}
		lastOpen, seenOpen, err := selector.state.LastNoteOpen(note.Ref.RelPath)
		if err != nil {
			return rediscoverCandidate{}, false, err
		}
		if seenOpen && lastOpen.After(effective) {
			effective = lastOpen
		}
	}
	return rediscoverCandidate{
		note:            note,
		effectiveAt:     effective,
		connectivity:    note.InboundLinks + note.OutboundLinks,
		metadataPresent: metadataPresence(note.MetadataValues),
	}, inCooldown, nil
}

func (selector *DeterministicSelector) connectPairInCooldown(sourcePath, candidatePath string, now time.Time) (bool, error) {
	if selector.state == nil {
		return false, nil
	}
	last, ok, err := selector.state.LastPairExposure(sourcePath, candidatePath)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return activeCooldown(last, now), nil
}

func (selector *DeterministicSelector) nowTime() time.Time {
	if selector == nil || selector.now == nil {
		return time.Now()
	}
	return selector.now()
}

func (selector *DeterministicSelector) excerptForPath(relPath string) string {
	if selector == nil {
		return ""
	}
	return selector.excerptsByID[relPath]
}

func selectionRelativePath(root, notePath string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("brief: empty corpus root")
	}
	if notePath == "" {
		return "", errors.New("brief: empty note path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(notePath)
	if err != nil {
		return "", fmt.Errorf("brief: resolve note %q: %w", notePath, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("brief: make %q relative to %q: %w", absPath, absRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("brief: note %q is outside vault root %q", absPath, absRoot)
	}
	return textnorm.NFC(filepath.ToSlash(rel)), nil
}

func noteTypeValues(note *vault.Note) ([]string, error) {
	if note == nil {
		return nil, nil
	}
	front, err := note.Front()
	if err != nil {
		relPath := filepath.Base(note.Path)
		if note.Path != "" {
			relPath = note.Path
		}
		return nil, fmt.Errorf("brief: load frontmatter for %q: %w", relPath, err)
	}
	value, ok := front.Get("type")
	if !ok {
		return nil, nil
	}
	seen := make(map[string]struct{})
	for _, raw := range scalarTypeValues(value) {
		normalized := normalizeTypeValue(raw)
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values, nil
}

func scalarTypeValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, scalarTypeValues(item)...)
		}
		return values
	case bool:
		return []string{strconv.FormatBool(typed)}
	case int:
		return []string{strconv.Itoa(typed)}
	case int64:
		return []string{strconv.FormatInt(typed, 10)}
	case uint64:
		return []string{strconv.FormatUint(typed, 10)}
	case float64:
		return []string{strconv.FormatFloat(typed, 'g', -1, 64)}
	case time.Time:
		return []string{typed.Format(time.RFC3339Nano)}
	default:
		return []string{fmt.Sprint(typed)}
	}
}

func normalizeTypeValue(value string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(value)))
}

func normalizeFolderExclusions(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for i, raw := range values {
		value := textnorm.NFC(filepath.ToSlash(strings.TrimSpace(raw)))
		if value == "" {
			continue
		}
		if filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" || looksLikeWindowsDrivePath(value) {
			return nil, fmt.Errorf("brief: exclude_folders[%d]: %q must be vault-relative", i, raw)
		}
		cleaned := path.Clean(value)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("brief: exclude_folders[%d]: %q escapes vault root", i, raw)
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeTypeExclusions(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := normalizeTypeValue(value)
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func hasExcludedType(typeValues []string, excluded map[string]struct{}) bool {
	if len(typeValues) == 0 || len(excluded) == 0 {
		return false
	}
	for _, value := range typeValues {
		if _, ok := excluded[value]; ok {
			return true
		}
	}
	return false
}

func pathExcludedByPrefix(notePath string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return false
	}
	for _, prefix := range prefixes {
		if notePath == prefix || strings.HasPrefix(notePath, prefix+"/") {
			return true
		}
	}
	return false
}

func metadataValuesByProperty(values []recall.MetadataValue) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	grouped := make(map[string]map[string]struct{})
	for _, value := range values {
		if value.Property == "" || value.Value == "" {
			continue
		}
		if _, ok := grouped[value.Property]; !ok {
			grouped[value.Property] = make(map[string]struct{})
		}
		grouped[value.Property][value.Value] = struct{}{}
	}
	if len(grouped) == 0 {
		return nil
	}
	out := make(map[string][]string, len(grouped))
	for property, set := range grouped {
		values := make([]string, 0, len(set))
		for value := range set {
			values = append(values, value)
		}
		sort.Strings(values)
		out[property] = values
	}
	return out
}

func excerptForDocument(document recall.Document) string {
	pick := func(kind recall.BlockKind) string {
		for _, block := range document.Blocks {
			if block.Kind != kind {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text != "" {
				return BoundCardExcerpt(text)
			}
		}
		return ""
	}
	if excerpt := pick(recall.BlockProse); excerpt != "" {
		return excerpt
	}
	if excerpt := pick(recall.BlockHeading); excerpt != "" {
		return excerpt
	}
	if excerpt := pick(recall.BlockTable); excerpt != "" {
		return excerpt
	}
	return BoundCardExcerpt(document.Title)
}

func sortRediscoverCandidates(candidates []rediscoverCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].effectiveAt.Equal(candidates[j].effectiveAt) {
			return candidates[i].effectiveAt.Before(candidates[j].effectiveAt)
		}
		if candidates[i].connectivity != candidates[j].connectivity {
			return candidates[i].connectivity > candidates[j].connectivity
		}
		if candidates[i].metadataPresent != candidates[j].metadataPresent {
			return candidates[i].metadataPresent > candidates[j].metadataPresent
		}
		return candidates[i].note.Ref.RelPath < candidates[j].note.Ref.RelPath
	})
}

func metadataPresence(values map[string][]string) int {
	if len(values) == 0 {
		return 0
	}
	count := 0
	for _, propertyValues := range values {
		if len(propertyValues) > 0 {
			count++
		}
	}
	return count
}

func activeCooldown(last, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	if now.Before(last) {
		return true
	}
	return now.Sub(last) < RecommendationCooldown
}

func connectBridge(result recall.Result) string {
	switch {
	case result.BridgePath != "":
		return result.BridgePath
	case result.BridgeTitle != "":
		return result.BridgeTitle
	default:
		return ""
	}
}

func connectReasonStrings(reasons []recall.Reason) []string {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if strings.TrimSpace(reason.Detail) != "" {
			out = append(out, reason.Detail)
			continue
		}
		out = append(out, string(reason.Kind))
	}
	return out
}

func unavailableSlot(slot SlotKind, message string) SlotResult {
	return SlotResult{
		Slot: slot,
		Unavailable: &UnavailableSlot{
			Slot:    slot,
			Message: message,
		},
	}
}

func cardSlot(slot SlotKind, card Card) SlotResult {
	card.Slot = slot
	return SlotResult{
		Slot: slot,
		Card: &card,
	}
}

func cloneCorpusNote(note CorpusNote) CorpusNote {
	cloned := note
	if note.MetadataValues == nil {
		return cloned
	}
	cloned.MetadataValues = make(map[string][]string, len(note.MetadataValues))
	for property, values := range note.MetadataValues {
		cloned.MetadataValues[property] = append([]string(nil), values...)
	}
	return cloned
}

func cloneRecallDocument(document recall.Document) recall.Document {
	cloned := document
	cloned.Fields = append([]recall.TextField(nil), document.Fields...)
	cloned.Blocks = append([]recall.MarkdownBlock(nil), document.Blocks...)
	cloned.Metadata = append([]recall.MetadataValue(nil), document.Metadata...)
	return cloned
}
