package recall

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/brainnotes/internal/query"
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
// Now is called once while the options are bound, producing a stable snapshot.
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

	ranker := &Ranker{
		documents: make(map[string]rankDocument, len(documents)),
		paths:     make([]string, 0, len(documents)),
		lexical:   lexical,
		metadata:  buildMetadataIndex(documents),
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
	return ranker.WithOptions(options), nil
}

// WithOptions returns an immutable request-scoped view over the ranker's
// prebuilt lexical, metadata, and graph indexes. The indexes are shared and
// never mutated; only the stable clock and recall signal source are rebound.
func (ranker *Ranker) WithOptions(options RankOptions) *Ranker {
	if ranker == nil {
		return nil
	}
	bound := *ranker
	bound.now = time.Now().UTC()
	if options.Now != nil {
		bound.now = options.Now().UTC()
	}
	bound.recall = options.Recall
	return &bound
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

// RankSelected scores an explicitly chosen candidate set and returns the
// results in the caller's order. Selection is authoritative: mode eligibility
// is not applied, and recall dismissal or an unexpired snooze never removes a
// candidate. Useful and open history still contributes its ordinary recall
// score and reason, so scores, relationship data, and reasons are the same
// balanced ones Rank produces.
func (ranker *Ranker) RankSelected(sourcePath string, candidatePaths []string) ([]Result, error) {
	if ranker == nil {
		return nil, errors.New("recall: rank selected: nil ranker")
	}
	if _, ok := ranker.documents[sourcePath]; !ok {
		return nil, fmt.Errorf("recall: rank selected: source path %q is not indexed", sourcePath)
	}

	results := make([]Result, 0, len(candidatePaths))
	seen := make(map[string]struct{}, len(candidatePaths))
	for index, candidatePath := range candidatePaths {
		if candidatePath == sourcePath {
			return nil, fmt.Errorf("recall: rank selected: candidate %d repeats source path %q", index, sourcePath)
		}
		if _, exists := seen[candidatePath]; exists {
			return nil, fmt.Errorf("recall: rank selected: duplicate candidate path %q", candidatePath)
		}
		seen[candidatePath] = struct{}{}
		candidate, ok := ranker.documents[candidatePath]
		if !ok {
			return nil, fmt.Errorf("recall: rank selected: candidate path %q is not indexed", candidatePath)
		}

		match, err := ranker.matchCandidate(sourcePath, candidatePath)
		if err != nil {
			return nil, err
		}
		match.signal.Dismissed = false
		match.signal.SnoozedUntil = time.Time{}
		results = append(results, buildRankResult(candidate, match, evaluateRecall(candidate.createdAt, match.signal, ranker.now)))
	}
	return results, nil
}

type candidateMatch struct {
	lexical  LexicalMatch
	metadata metadataMatch
	graph    graphMatch
	signal   RecallSignal
}

func (ranker *Ranker) matchCandidate(sourcePath, candidatePath string) (candidateMatch, error) {
	lexical, _ := ranker.lexical.Compare(sourcePath, candidatePath)
	match := candidateMatch{
		lexical:  lexical,
		metadata: compareMetadata(ranker.metadata[sourcePath], ranker.metadata[candidatePath]),
		graph:    ranker.graph.compare(sourcePath, candidatePath),
	}
	if ranker.recall == nil {
		return match, nil
	}
	signal, err := ranker.recall.Signal(sourcePath, candidatePath)
	if err != nil {
		return candidateMatch{}, fmt.Errorf(
			"recall: rank %q against %q: load recall signal: %w",
			sourcePath,
			candidatePath,
			err,
		)
	}
	match.signal = signal
	return match, nil
}

func (ranker *Ranker) scoreCandidate(sourcePath, candidatePath string, mode Mode) (Result, bool, error) {
	candidate := ranker.documents[candidatePath]
	match, err := ranker.matchCandidate(sourcePath, candidatePath)
	if err != nil {
		return Result{}, false, err
	}
	recall := evaluateRecall(candidate.createdAt, match.signal, ranker.now)
	if recall.suppressed {
		return Result{}, false, nil
	}
	if !candidateEligible(mode, matchScores(match, recall), match.graph.relationship, recall) {
		return Result{}, false, nil
	}
	return buildRankResult(candidate, match, recall), true, nil
}

func matchScores(match candidateMatch, recall recallMatch) SignalScores {
	return SignalScores{
		Text:     match.lexical.Score,
		Graph:    match.graph.score,
		Metadata: match.metadata.score,
		Recall:   recall.score,
	}
}

func buildRankResult(candidate rankDocument, match candidateMatch, recall recallMatch) Result {
	scores := matchScores(match, recall)
	result := Result{
		Score: scores.Text*TextScoreWeight +
			scores.Graph*GraphScoreWeight +
			scores.Metadata*MetadataScoreWeight +
			scores.Recall*RecallScoreWeight,
		Path:         candidate.path,
		Title:        candidate.title,
		Relationship: match.graph.relationship,
		Signals:      scores,
	}
	if match.graph.relationship == RelationshipTwoHop || match.graph.relationship == RelationshipSharedNeighbor {
		result.BridgePath = match.graph.bridge.path
		result.BridgeTitle = match.graph.bridge.title
	}
	result.Reasons = buildReasons(match.lexical, match.metadata, match.graph, recall)
	return result
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
