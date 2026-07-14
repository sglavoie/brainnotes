package recall

import (
	"errors"
	"fmt"
	"math"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// RankingVersion identifies the stable scoring and explanation semantics.
const RankingVersion = 1

const (
	TextScoreWeight     = 0.40
	GraphScoreWeight    = 0.30
	MetadataScoreWeight = 0.20
	RecallScoreWeight   = 0.10

	RecentOpenWindow  = 30 * 24 * time.Hour
	OldNoteAge        = 180 * 24 * time.Hour
	FullNoveltyAge    = 365 * 24 * time.Hour
	UsefulRecallBoost = 0.50
)

// Weights contains the fixed v1 signal weights.
type Weights struct {
	Text     float64
	Graph    float64
	Metadata float64
	Recall   float64
}

// RankingWeights returns a fresh copy of the fixed v1 weights.
func RankingWeights() Weights {
	return Weights{
		Text:     TextScoreWeight,
		Graph:    GraphScoreWeight,
		Metadata: MetadataScoreWeight,
		Recall:   RecallScoreWeight,
	}
}

// Mode controls which candidates are eligible without changing their scores.
type Mode string

const (
	ModeBalanced   Mode = "balanced"
	ModeRediscover Mode = "rediscover"
	ModeConnect    Mode = "connect"
)

// ParseMode validates and returns a ranking mode.
func ParseMode(value string) (Mode, error) {
	mode := Mode(value)
	if !mode.Valid() {
		return "", fmt.Errorf("recall: unknown ranking mode %q", value)
	}
	return mode, nil
}

// Valid reports whether mode is supported.
func (mode Mode) Valid() bool {
	switch mode {
	case ModeBalanced, ModeRediscover, ModeConnect:
		return true
	default:
		return false
	}
}

// RelationshipClass describes the strongest graph relationship between the
// source and candidate.
type RelationshipClass string

const (
	RelationshipNone           RelationshipClass = ""
	RelationshipDirect         RelationshipClass = "direct"
	RelationshipSharedNeighbor RelationshipClass = "shared-neighbor"
	RelationshipTwoHop         RelationshipClass = "two-hop"
)

// ReasonKind identifies a human-readable score explanation.
type ReasonKind string

const (
	ReasonText     ReasonKind = "text"
	ReasonGraph    ReasonKind = "graph"
	ReasonMetadata ReasonKind = "metadata"
	ReasonRecall   ReasonKind = "recall"
)

// Reason is one of at most three stable explanations for a result. Its
// contribution already includes the signal's fixed ranking weight.
type Reason struct {
	Kind         ReasonKind
	Contribution float64
	Detail       string
}

// SignalScores contains the normalized component scores used to produce the
// weighted Result.Score.
type SignalScores struct {
	Text     float64
	Graph    float64
	Metadata float64
	Recall   float64
}

// Result is one ranked candidate. BridgePath and BridgeTitle identify the
// stable first intermediate or shared note for indirect relationships.
type Result struct {
	Score        float64
	Path         string
	Title        string
	Relationship RelationshipClass
	BridgePath   string
	BridgeTitle  string
	Signals      SignalScores
	Reasons      []Reason
}

// RecallSignal is read-only pair history supplied by a RecallSignalSource.
// A dismissed pair or a snooze later than the ranker's clock is suppressed.
type RecallSignal struct {
	Useful       bool
	Dismissed    bool
	SnoozedUntil time.Time
	LastOpenedAt time.Time
}

// RecallSignalSource lets persistence integrate without coupling ranking to a
// sidecar format. Implementations may return load or validation errors.
type RecallSignalSource interface {
	Signal(sourcePath, candidatePath string) (RecallSignal, error)
}

// RecallSignalFunc adapts a function to RecallSignalSource.
type RecallSignalFunc func(sourcePath, candidatePath string) (RecallSignal, error)

// Signal implements RecallSignalSource.
func (function RecallSignalFunc) Signal(sourcePath, candidatePath string) (RecallSignal, error) {
	return function(sourcePath, candidatePath)
}

// RankOptions supplies the ranking clock and optional read-only recall state.
// Now is called once while the ranker is built, producing a stable snapshot.
type RankOptions struct {
	Now    func() time.Time
	Recall RecallSignalSource
}

// Ranker is an immutable in-memory hybrid retrieval index.
type Ranker struct {
	documents map[string]rankDocument
	paths     []string
	lexical   *LexicalIndex
	metadata  map[string]metadataVector
	graph     graphIndex
	now       time.Time
	recall    RecallSignalSource
}

type rankDocument struct {
	path      string
	title     string
	createdAt time.Time
}

type metadataVector struct {
	weights map[MetadataValue]float64
	pairs   []MetadataValue
	norm    float64
}

type metadataContribution struct {
	pair         MetadataValue
	contribution float64
}

type metadataMatch struct {
	score         float64
	contributions []metadataContribution
}

type graphNode struct {
	path  string
	title string
}

type graphIndex struct {
	documentKeys map[string]string
	forward      map[string][]string
	neighbors    map[string][]string
	nodes        map[string]graphNode
}

type graphMatch struct {
	score        float64
	relationship RelationshipClass
	bridge       graphNode
	shared       int
}

type recallMatch struct {
	score             float64
	suppressed        bool
	age               time.Duration
	old               bool
	notRecentlyOpened bool
	recentOpenAge     time.Duration
	useful            bool
}

// BuildRanker constructs the lexical, metadata, and graph indexes used by all
// modes. Documents are keyed by normalized vault-relative paths.
func BuildRanker(documents []Document, graph *query.Graph, options RankOptions) (*Ranker, error) {
	lexical, err := BuildLexicalIndex(documents)
	if err != nil {
		return nil, err
	}

	documents = append([]Document(nil), documents...)
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})

	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}

	ranker := &Ranker{
		documents: make(map[string]rankDocument, len(documents)),
		paths:     make([]string, 0, len(documents)),
		lexical:   lexical,
		metadata:  buildMetadataIndex(documents),
		now:       now,
		recall:    options.Recall,
	}
	for _, document := range documents {
		ranker.documents[document.Path] = rankDocument{
			path:      document.Path,
			title:     document.Title,
			createdAt: document.CreatedAt,
		}
		ranker.paths = append(ranker.paths, document.Path)
	}
	ranker.graph = buildGraphIndex(graph, documents)
	return ranker, nil
}

// Weights returns the fixed weights used by the ranker.
func (ranker *Ranker) Weights() Weights {
	return RankingWeights()
}

// Rank scores every eligible candidate and returns descending score order,
// breaking exact ties by vault-relative path.
func (ranker *Ranker) Rank(sourcePath string, mode Mode) ([]Result, error) {
	if ranker == nil {
		return nil, errors.New("recall: rank: nil ranker")
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("recall: rank: unknown mode %q", mode)
	}
	if _, ok := ranker.documents[sourcePath]; !ok {
		return nil, fmt.Errorf("recall: rank: source path %q is not indexed", sourcePath)
	}

	results := make([]Result, 0, len(ranker.paths)-1)
	for _, candidatePath := range ranker.paths {
		if candidatePath == sourcePath {
			continue
		}
		result, eligible, err := ranker.scoreCandidate(sourcePath, candidatePath, mode)
		if err != nil {
			return nil, err
		}
		if eligible {
			results = append(results, result)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})
	return results, nil
}

func (ranker *Ranker) scoreCandidate(sourcePath, candidatePath string, mode Mode) (Result, bool, error) {
	candidate := ranker.documents[candidatePath]
	lexical, _ := ranker.lexical.Compare(sourcePath, candidatePath)
	metadata := compareMetadata(ranker.metadata[sourcePath], ranker.metadata[candidatePath])
	graph := ranker.graph.compare(sourcePath, candidatePath)

	signal := RecallSignal{}
	if ranker.recall != nil {
		var err error
		signal, err = ranker.recall.Signal(sourcePath, candidatePath)
		if err != nil {
			return Result{}, false, fmt.Errorf(
				"recall: rank %q against %q: load recall signal: %w",
				sourcePath,
				candidatePath,
				err,
			)
		}
	}
	recall := evaluateRecall(candidate.createdAt, signal, ranker.now)
	if recall.suppressed {
		return Result{}, false, nil
	}

	scores := SignalScores{
		Text:     lexical.Score,
		Graph:    graph.score,
		Metadata: metadata.score,
		Recall:   recall.score,
	}
	if !candidateEligible(mode, scores, graph.relationship, recall) {
		return Result{}, false, nil
	}

	result := Result{
		Score: scores.Text*TextScoreWeight +
			scores.Graph*GraphScoreWeight +
			scores.Metadata*MetadataScoreWeight +
			scores.Recall*RecallScoreWeight,
		Path:         candidate.path,
		Title:        candidate.title,
		Relationship: graph.relationship,
		Signals:      scores,
	}
	if graph.relationship == RelationshipTwoHop || graph.relationship == RelationshipSharedNeighbor {
		result.BridgePath = graph.bridge.path
		result.BridgeTitle = graph.bridge.title
	}
	result.Reasons = buildReasons(lexical, metadata, graph, recall)
	return result, true, nil
}

func candidateEligible(
	mode Mode,
	scores SignalScores,
	relationship RelationshipClass,
	recall recallMatch,
) bool {
	switch mode {
	case ModeBalanced:
		return scores.Text > 0 || scores.Graph > 0 || scores.Metadata > 0 || scores.Recall > 0
	case ModeRediscover:
		return (relationship == RelationshipDirect || relationship == RelationshipTwoHop) &&
			(recall.old || recall.notRecentlyOpened)
	case ModeConnect:
		return relationship != RelationshipDirect && (scores.Text > 0 || scores.Metadata > 0)
	default:
		return false
	}
}

func buildMetadataIndex(documents []Document) map[string]metadataVector {
	documentFrequency := make(map[MetadataValue]int)
	uniquePairs := make(map[string][]MetadataValue, len(documents))
	for _, document := range documents {
		seen := make(map[MetadataValue]struct{}, len(document.Metadata))
		for _, pair := range document.Metadata {
			if pair.Property == "" || pair.Value == "" {
				continue
			}
			seen[pair] = struct{}{}
		}
		pairs := sortedMetadataPairs(seen)
		uniquePairs[document.Path] = pairs
		for _, pair := range pairs {
			documentFrequency[pair]++
		}
	}

	corpusSize := float64(len(documents))
	inverseFrequency := make(map[MetadataValue]float64, len(documentFrequency))
	for pair, frequency := range documentFrequency {
		inverseFrequency[pair] = math.Log((1+corpusSize)/(1+float64(frequency))) + 1
	}

	vectors := make(map[string]metadataVector, len(documents))
	for _, document := range documents {
		pairs := uniquePairs[document.Path]
		weights := make(map[MetadataValue]float64, len(pairs))
		squaredNorm := 0.0
		for _, pair := range pairs {
			weight := inverseFrequency[pair]
			weights[pair] = weight
			squaredNorm += weight * weight
		}
		vectors[document.Path] = metadataVector{
			weights: weights,
			pairs:   pairs,
			norm:    math.Sqrt(squaredNorm),
		}
	}
	return vectors
}

func sortedMetadataPairs(values map[MetadataValue]struct{}) []MetadataValue {
	if len(values) == 0 {
		return nil
	}
	pairs := make([]MetadataValue, 0, len(values))
	for pair := range values {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Property != pairs[j].Property {
			return pairs[i].Property < pairs[j].Property
		}
		return pairs[i].Value < pairs[j].Value
	})
	return pairs
}

func compareMetadata(source, candidate metadataVector) metadataMatch {
	if source.norm == 0 || candidate.norm == 0 {
		return metadataMatch{}
	}

	denominator := source.norm * candidate.norm
	contributions := make([]metadataContribution, 0, min(len(source.pairs), len(candidate.pairs)))
	score := 0.0
	for sourceIndex, candidateIndex := 0, 0; sourceIndex < len(source.pairs) && candidateIndex < len(candidate.pairs); {
		sourcePair := source.pairs[sourceIndex]
		candidatePair := candidate.pairs[candidateIndex]
		switch compareMetadataPair(sourcePair, candidatePair) {
		case -1:
			sourceIndex++
		case 1:
			candidateIndex++
		default:
			contribution := source.weights[sourcePair] * candidate.weights[candidatePair] / denominator
			score += contribution
			contributions = append(contributions, metadataContribution{
				pair:         sourcePair,
				contribution: contribution,
			})
			sourceIndex++
			candidateIndex++
		}
	}
	if score > 1 && score < 1+1e-12 {
		score = 1
	}
	sort.SliceStable(contributions, func(i, j int) bool {
		if contributions[i].contribution != contributions[j].contribution {
			return contributions[i].contribution > contributions[j].contribution
		}
		return compareMetadataPair(contributions[i].pair, contributions[j].pair) < 0
	})
	return metadataMatch{score: score, contributions: contributions}
}

func compareMetadataPair(left, right MetadataValue) int {
	if left.Property < right.Property {
		return -1
	}
	if left.Property > right.Property {
		return 1
	}
	if left.Value < right.Value {
		return -1
	}
	if left.Value > right.Value {
		return 1
	}
	return 0
}

func buildGraphIndex(graph *query.Graph, documents []Document) graphIndex {
	index := graphIndex{
		documentKeys: make(map[string]string, len(documents)),
		forward:      make(map[string][]string),
		neighbors:    make(map[string][]string),
		nodes:        make(map[string]graphNode),
	}
	if graph == nil {
		return index
	}

	graphKeys := make([]string, 0, len(graph.PathsByTitle))
	for key := range graph.PathsByTitle {
		graphKeys = append(graphKeys, key)
	}
	sort.Strings(graphKeys)
	keysByPath := make(map[string]string, len(graphKeys))
	for _, key := range graphKeys {
		graphPath := graph.PathsByTitle[key]
		relativePath, ok := graphRelativePath(graph.Root, graphPath)
		if !ok {
			continue
		}
		index.nodes[key] = graphNode{
			path:  relativePath,
			title: graph.OriginalTitles[key],
		}
		if _, exists := keysByPath[relativePath]; !exists {
			keysByPath[relativePath] = key
		}
	}
	for _, document := range documents {
		key := keysByPath[document.Path]
		if key == "" {
			fallback := graphDocumentKey(document.Path)
			if _, ok := graph.Titles[fallback]; ok {
				key = fallback
			}
		}
		if key == "" {
			continue
		}
		index.documentKeys[document.Path] = key
		index.nodes[key] = graphNode{path: document.Path, title: document.Title}
	}

	neighborSets := make(map[string]map[string]struct{})
	forwardSets := make(map[string]map[string]struct{})
	for source, targets := range graph.Forward {
		for _, target := range targets {
			if source == "" || target == "" || source == target {
				continue
			}
			if neighborSets[source] == nil {
				neighborSets[source] = make(map[string]struct{})
			}
			if neighborSets[target] == nil {
				neighborSets[target] = make(map[string]struct{})
			}
			if forwardSets[source] == nil {
				forwardSets[source] = make(map[string]struct{})
			}
			forwardSets[source][target] = struct{}{}
			neighborSets[source][target] = struct{}{}
			neighborSets[target][source] = struct{}{}
		}
	}
	for key, targets := range forwardSets {
		values := make([]string, 0, len(targets))
		for target := range targets {
			values = append(values, target)
		}
		sort.Strings(values)
		index.forward[key] = values
	}
	for key, neighbors := range neighborSets {
		values := make([]string, 0, len(neighbors))
		for neighbor := range neighbors {
			values = append(values, neighbor)
		}
		sort.Strings(values)
		index.neighbors[key] = values
	}
	return index
}

func graphRelativePath(root, graphPath string) (string, bool) {
	if graphPath == "" {
		return "", false
	}
	if root == "" {
		if filepath.IsAbs(graphPath) {
			return "", false
		}
		return filepath.ToSlash(filepath.Clean(graphPath)), true
	}
	relative, err := filepath.Rel(root, graphPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return textnorm.NFC(filepath.ToSlash(relative)), true
}

func graphDocumentKey(documentPath string) string {
	documentPath = strings.TrimSpace(strings.ReplaceAll(documentPath, "\\", "/"))
	for _, part := range strings.Split(documentPath, "/") {
		if part == "." || part == ".." {
			return ""
		}
	}
	documentPath = strings.TrimPrefix(path.Clean("/"+documentPath), "/")
	extension := path.Ext(documentPath)
	if strings.EqualFold(extension, ".md") {
		documentPath = strings.TrimSuffix(documentPath, extension)
	}
	return strings.ToLower(textnorm.NFC(documentPath))
}

func (index graphIndex) compare(sourcePath, candidatePath string) graphMatch {
	sourceKey := index.documentKeys[sourcePath]
	candidateKey := index.documentKeys[candidatePath]
	if sourceKey == "" || candidateKey == "" {
		return graphMatch{}
	}
	if sortedStringsContain(index.neighbors[sourceKey], candidateKey) {
		return graphMatch{score: 1, relationship: RelationshipDirect}
	}

	sharedKeys := intersectStrings(index.neighbors[sourceKey], index.neighbors[candidateKey])
	if len(sharedKeys) == 0 {
		return graphMatch{}
	}
	twoHopKeys := make([]string, 0, len(sharedKeys))
	for _, bridgeKey := range sharedKeys {
		if (sortedStringsContain(index.forward[sourceKey], bridgeKey) &&
			sortedStringsContain(index.forward[bridgeKey], candidateKey)) ||
			(sortedStringsContain(index.forward[candidateKey], bridgeKey) &&
				sortedStringsContain(index.forward[bridgeKey], sourceKey)) {
			twoHopKeys = append(twoHopKeys, bridgeKey)
		}
	}
	relationship := RelationshipSharedNeighbor
	bridgeKeys := sharedKeys
	if len(twoHopKeys) > 0 {
		relationship = RelationshipTwoHop
		bridgeKeys = twoHopKeys
	}
	sort.SliceStable(bridgeKeys, func(i, j int) bool {
		left := index.nodes[bridgeKeys[i]].path
		right := index.nodes[bridgeKeys[j]].path
		if left != right {
			return left < right
		}
		return bridgeKeys[i] < bridgeKeys[j]
	})
	overlap := float64(len(sharedKeys))
	return graphMatch{
		score:        0.6 + 0.4*(overlap-1)/overlap,
		relationship: relationship,
		bridge:       index.nodes[bridgeKeys[0]],
		shared:       len(sharedKeys),
	}
}

func sortedStringsContain(values []string, wanted string) bool {
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}

func intersectStrings(left, right []string) []string {
	values := make([]string, 0, min(len(left), len(right)))
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		switch {
		case left[leftIndex] < right[rightIndex]:
			leftIndex++
		case left[leftIndex] > right[rightIndex]:
			rightIndex++
		default:
			values = append(values, left[leftIndex])
			leftIndex++
			rightIndex++
		}
	}
	return values
}

func evaluateRecall(createdAt time.Time, signal RecallSignal, now time.Time) recallMatch {
	match := recallMatch{
		suppressed: signal.Dismissed || (!signal.SnoozedUntil.IsZero() && signal.SnoozedUntil.After(now)),
		useful:     signal.Useful,
	}
	if match.suppressed {
		return match
	}

	if !createdAt.IsZero() && createdAt.Before(now) {
		match.age = now.Sub(createdAt)
		match.score = min(float64(match.age)/float64(FullNoveltyAge), 1)
		match.old = match.age >= OldNoteAge
	}
	if signal.Useful && match.score < UsefulRecallBoost {
		match.score = UsefulRecallBoost
	}

	match.notRecentlyOpened = signal.LastOpenedAt.IsZero()
	if !signal.LastOpenedAt.IsZero() {
		match.recentOpenAge = now.Sub(signal.LastOpenedAt)
		if match.recentOpenAge >= RecentOpenWindow {
			match.notRecentlyOpened = true
		} else {
			factor := max(float64(match.recentOpenAge)/float64(RecentOpenWindow), 0)
			match.score *= factor
		}
	}
	return match
}

func buildReasons(
	lexical LexicalMatch,
	metadata metadataMatch,
	graph graphMatch,
	recall recallMatch,
) []Reason {
	reasons := make([]Reason, 0, 4)
	if lexical.Score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonText,
			Contribution: lexical.Score * TextScoreWeight,
			Detail:       lexicalReasonDetail(lexical.Terms),
		})
	}
	if graph.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonGraph,
			Contribution: graph.score * GraphScoreWeight,
			Detail:       graphReasonDetail(graph),
		})
	}
	if metadata.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonMetadata,
			Contribution: metadata.score * MetadataScoreWeight,
			Detail:       metadataReasonDetail(metadata.contributions),
		})
	}
	if recall.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonRecall,
			Contribution: recall.score * RecallScoreWeight,
			Detail:       recallReasonDetail(recall),
		})
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Contribution != reasons[j].Contribution {
			return reasons[i].Contribution > reasons[j].Contribution
		}
		if reasonOrder(reasons[i].Kind) != reasonOrder(reasons[j].Kind) {
			return reasonOrder(reasons[i].Kind) < reasonOrder(reasons[j].Kind)
		}
		return reasons[i].Detail < reasons[j].Detail
	})
	if len(reasons) > 3 {
		reasons = reasons[:3]
	}
	return reasons
}

func reasonOrder(kind ReasonKind) int {
	switch kind {
	case ReasonText:
		return 0
	case ReasonGraph:
		return 1
	case ReasonMetadata:
		return 2
	case ReasonRecall:
		return 3
	default:
		return 4
	}
}

func lexicalReasonDetail(terms []TermContribution) string {
	values := make([]string, 0, min(3, len(terms)))
	for _, term := range terms[:min(3, len(terms))] {
		values = append(values, term.Term)
	}
	return "shared terms: " + strings.Join(values, ", ")
}

func graphReasonDetail(match graphMatch) string {
	if match.relationship == RelationshipDirect {
		return "direct link"
	}
	bridge := match.bridge.title
	if bridge == "" {
		bridge = match.bridge.path
	}
	if match.relationship == RelationshipTwoHop && match.shared == 1 {
		return "two hops via " + bridge
	}
	if match.relationship == RelationshipTwoHop {
		return fmt.Sprintf("two hops via %s (%d shared neighbors)", bridge, match.shared)
	}
	if match.shared == 1 {
		return "shared neighbor " + bridge
	}
	return fmt.Sprintf("shared neighbor %s (%d shared neighbors)", bridge, match.shared)
}

func metadataReasonDetail(contributions []metadataContribution) string {
	values := make([]string, 0, min(3, len(contributions)))
	for _, contribution := range contributions[:min(3, len(contributions))] {
		values = append(values, contribution.pair.Property+"="+contribution.pair.Value)
	}
	return "shared metadata: " + strings.Join(values, ", ")
}

func recallReasonDetail(match recallMatch) string {
	parts := make([]string, 0, 3)
	if match.useful {
		parts = append(parts, "useful feedback")
	}
	if match.age > 0 {
		parts = append(parts, fmt.Sprintf("created %d days ago", int(match.age/(24*time.Hour))))
	}
	if match.recentOpenAge > 0 && match.recentOpenAge < RecentOpenWindow {
		parts = append(parts, fmt.Sprintf("opened %d days ago", int(match.recentOpenAge/(24*time.Hour))))
	}
	return strings.Join(parts, "; ")
}
