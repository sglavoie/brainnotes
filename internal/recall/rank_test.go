package recall

import (
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

func TestRankerGoldenSignalsAndModes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	documents := []Document{
		rankDocumentFixture("Source.md", "Source", "shared lexical focus"),
		rankDocumentFixture("A-Tie.md", "A Tie", "shared lexical focus"),
		rankDocumentFixture("B-Tie.md", "B Tie", "shared lexical focus"),
		rankDocumentFixture("TextOnly.md", "Text Only", "shared lexical focus"),
		{Path: "Direct.md", Title: "Direct"},
		{Path: "TwoHop.md", Title: "Two Hop"},
		{Path: "MetadataOnly.md", Title: "Metadata Only"},
		{Path: "Old.md", Title: "Old", CreatedAt: now.Add(-FullNoveltyAge)},
		{Path: "SignalFree.md", Title: "Signal Free"},
	}
	documents[0].Metadata = []MetadataValue{{Property: "topic", Value: "bayesian"}}
	documents[6].Metadata = []MetadataValue{{Property: "topic", Value: "bayesian"}}
	graph := rankGraphFixture(t, t.TempDir(), documents, map[string][]string{
		"source": {"direct"},
		"direct": {"twohop"},
	})

	ranker, err := BuildRanker(documents, graph, RankOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	balanced, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank balanced: %v", err)
	}
	if got, want := resultPaths(balanced), []string{
		"A-Tie.md",
		"B-Tie.md",
		"TextOnly.md",
		"Direct.md",
		"MetadataOnly.md",
		"TwoHop.md",
		"Old.md",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("balanced paths = %v, want %v", got, want)
	}

	byPath := resultsByPath(balanced)
	assertClose(t, "text-only score", byPath["TextOnly.md"].Score, TextScoreWeight)
	assertClose(t, "direct score", byPath["Direct.md"].Score, GraphScoreWeight)
	assertClose(t, "metadata score", byPath["MetadataOnly.md"].Score, MetadataScoreWeight)
	assertClose(t, "two-hop score", byPath["TwoHop.md"].Score, 0.6*GraphScoreWeight)
	assertClose(t, "old score", byPath["Old.md"].Score, RecallScoreWeight)
	if byPath["Direct.md"].Relationship != RelationshipDirect {
		t.Errorf("direct relationship = %q, want %q", byPath["Direct.md"].Relationship, RelationshipDirect)
	}
	twoHop := byPath["TwoHop.md"]
	if twoHop.Relationship != RelationshipTwoHop || twoHop.BridgePath != "Direct.md" || twoHop.BridgeTitle != "Direct" {
		t.Errorf("two-hop result = %#v, want bridge Direct.md", twoHop)
	}
	if got := byPath["MetadataOnly.md"].Reasons; len(got) != 1 || got[0].Detail != "shared metadata: topic=bayesian" {
		t.Errorf("metadata reasons = %#v", got)
	}
	if got := byPath["SignalFree.md"]; got.Path != "" {
		t.Errorf("signal-free candidate unexpectedly returned: %#v", got)
	}

	rediscover, err := ranker.Rank("Source.md", ModeRediscover)
	if err != nil {
		t.Fatalf("Rank rediscover: %v", err)
	}
	if got, want := resultPaths(rediscover), []string{"Direct.md", "TwoHop.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rediscover paths = %v, want %v", got, want)
	}
	connect, err := ranker.Rank("Source.md", ModeConnect)
	if err != nil {
		t.Fatalf("Rank connect: %v", err)
	}
	if got, want := resultPaths(connect), []string{"A-Tie.md", "B-Tie.md", "TextOnly.md", "MetadataOnly.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("connect paths = %v, want %v", got, want)
	}
	for _, result := range connect {
		if result.Relationship == RelationshipDirect {
			t.Errorf("connect returned direct result %#v", result)
		}
	}

	reversed := append([]Document(nil), documents...)
	sort.SliceStable(reversed, func(i, j int) bool { return reversed[i].Path > reversed[j].Path })
	rebuilt, err := BuildRanker(reversed, graph, RankOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("rebuilt BuildRanker: %v", err)
	}
	rebuiltResults, err := rebuilt.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("rebuilt Rank: %v", err)
	}
	if !reflect.DeepEqual(balanced, rebuiltResults) {
		t.Errorf("repeated ranking differs\nfirst:   %#v\nrebuilt: %#v", balanced, rebuiltResults)
	}
}

func TestRankerGraphOverlapIncreasesScoreAndChoosesStableBridge(t *testing.T) {
	t.Parallel()

	documents := []Document{
		{Path: "Source.md", Title: "Source"},
		{Path: "BridgeA.md", Title: "Bridge A"},
		{Path: "BridgeB.md", Title: "Bridge B"},
		{Path: "CandidateOne.md", Title: "Candidate One"},
		{Path: "CandidateTwo.md", Title: "Candidate Two"},
	}
	graph := rankGraphFixture(t, t.TempDir(), documents, map[string][]string{
		"source":       {"bridgea", "bridgeb"},
		"candidateone": {"bridgea"},
		"candidatetwo": {"bridgeb", "bridgea"},
	})
	ranker, err := BuildRanker(documents, graph, RankOptions{Now: fixedRankTime})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	results, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	byPath := resultsByPath(results)
	one := byPath["CandidateOne.md"]
	two := byPath["CandidateTwo.md"]
	assertClose(t, "one shared neighbor", one.Signals.Graph, 0.6)
	assertClose(t, "two shared neighbors", two.Signals.Graph, 0.8)
	if two.Signals.Graph <= one.Signals.Graph {
		t.Errorf("two-overlap graph score %v <= one-overlap %v", two.Signals.Graph, one.Signals.Graph)
	}
	if one.Relationship != RelationshipSharedNeighbor || two.Relationship != RelationshipSharedNeighbor {
		t.Errorf("shared-neighbor relationships = %q, %q", one.Relationship, two.Relationship)
	}
	if two.BridgePath != "BridgeA.md" || two.BridgeTitle != "Bridge A" {
		t.Errorf("stable bridge = %q/%q, want BridgeA.md/Bridge A", two.BridgePath, two.BridgeTitle)
	}
	if got := two.Reasons[0].Detail; got != "shared neighbor Bridge A (2 shared neighbors)" {
		t.Errorf("graph reason = %q", got)
	}
	rediscover, err := ranker.Rank("Source.md", ModeRediscover)
	if err != nil {
		t.Fatalf("Rank rediscover: %v", err)
	}
	rediscoverByPath := resultsByPath(rediscover)
	if _, ok := rediscoverByPath["CandidateOne.md"]; ok {
		t.Error("rediscover returned a shared-neighbor-only candidate")
	}
	if _, ok := rediscoverByPath["CandidateTwo.md"]; ok {
		t.Error("rediscover returned a shared-neighbor-only candidate")
	}
}

func TestRankerMetadataUsesInverseFrequency(t *testing.T) {
	t.Parallel()

	common := MetadataValue{Property: "type", Value: "note"}
	rare := MetadataValue{Property: "topic", Value: "rare-system"}
	documents := []Document{
		{Path: "Source.md", Title: "Source", Metadata: []MetadataValue{common, rare}},
		{Path: "Common.md", Title: "Common", Metadata: []MetadataValue{common}},
		{Path: "Rare.md", Title: "Rare", Metadata: []MetadataValue{rare}},
		{Path: "FillerA.md", Title: "Filler A", Metadata: []MetadataValue{common}},
		{Path: "FillerB.md", Title: "Filler B", Metadata: []MetadataValue{common}},
	}
	ranker, err := BuildRanker(documents, nil, RankOptions{Now: fixedRankTime})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	results, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	byPath := resultsByPath(results)
	if byPath["Rare.md"].Signals.Metadata <= byPath["Common.md"].Signals.Metadata {
		t.Errorf(
			"rare metadata score %v <= common metadata score %v",
			byPath["Rare.md"].Signals.Metadata,
			byPath["Common.md"].Signals.Metadata,
		)
	}
	if got := byPath["Rare.md"].Reasons[0].Detail; got != "shared metadata: topic=rare-system" {
		t.Errorf("rare metadata reason = %q", got)
	}
}

func TestRankerRecallSignalsAndRediscoverBoundaries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	documents := []Document{
		rankDocumentFixture("Source.md", "Source", "semantic overlap"),
		{Path: "Dismissed.md", Title: "Dismissed", CreatedAt: now.Add(-FullNoveltyAge)},
		{Path: "Snoozed.md", Title: "Snoozed", CreatedAt: now.Add(-FullNoveltyAge)},
		{Path: "Expired.md", Title: "Expired", CreatedAt: now.Add(-FullNoveltyAge)},
		{Path: "Useful.md", Title: "Useful"},
		{Path: "Recent.md", Title: "Recent", CreatedAt: now.Add(-FullNoveltyAge)},
		{Path: "RecentGraph.md", Title: "Recent Graph", CreatedAt: now},
		{Path: "StaleGraph.md", Title: "Stale Graph", CreatedAt: now},
		rankDocumentFixture("TextOnly.md", "Text Only", "semantic overlap"),
	}
	graph := rankGraphFixture(t, t.TempDir(), documents, map[string][]string{
		"source": {"recentgraph", "stalegraph"},
	})
	signals := map[string]RecallSignal{
		"Dismissed.md":   {Dismissed: true},
		"Snoozed.md":     {SnoozedUntil: now.Add(24 * time.Hour)},
		"Expired.md":     {SnoozedUntil: now.Add(-24 * time.Hour)},
		"Useful.md":      {Useful: true},
		"Recent.md":      {LastOpenedAt: now.Add(-RecentOpenWindow / 2)},
		"RecentGraph.md": {LastOpenedAt: now},
	}
	provider := RecallSignalFunc(func(_, candidatePath string) (RecallSignal, error) {
		return signals[candidatePath], nil
	})
	ranker, err := BuildRanker(documents, graph, RankOptions{
		Now:    func() time.Time { return now },
		Recall: provider,
	})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	balanced, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank balanced: %v", err)
	}
	byPath := resultsByPath(balanced)
	if _, ok := byPath["Dismissed.md"]; ok {
		t.Error("dismissed candidate was returned")
	}
	if _, ok := byPath["Snoozed.md"]; ok {
		t.Error("currently snoozed candidate was returned")
	}
	assertClose(t, "expired snooze recall", byPath["Expired.md"].Signals.Recall, 1)
	assertClose(t, "useful recall", byPath["Useful.md"].Signals.Recall, UsefulRecallBoost)
	assertClose(t, "recent-open recall", byPath["Recent.md"].Signals.Recall, 0.5)

	rediscover, err := ranker.Rank("Source.md", ModeRediscover)
	if err != nil {
		t.Fatalf("Rank rediscover: %v", err)
	}
	if got, want := resultPaths(rediscover), []string{"StaleGraph.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rediscover paths = %v, want %v", got, want)
	}

	wantErr := errors.New("corrupt recall state")
	failing, err := BuildRanker(documents[:2], nil, RankOptions{
		Now: fixedRankTime,
		Recall: RecallSignalFunc(func(_, _ string) (RecallSignal, error) {
			return RecallSignal{}, wantErr
		}),
	})
	if err != nil {
		t.Fatalf("BuildRanker with failing provider: %v", err)
	}
	_, err = failing.Rank("Source.md", ModeBalanced)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Rank error = %v, want wrapped provider error", err)
	}
}

func TestRankerReturnsTopThreeReasonsAndValidatesInputs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	pair := MetadataValue{Property: "topic", Value: "systems"}
	documents := []Document{
		rankDocumentFixture("Source.md", "Source", "shared concept"),
		rankDocumentFixture("Candidate.md", "Candidate", "shared concept"),
	}
	documents[0].Metadata = []MetadataValue{pair}
	documents[1].Metadata = []MetadataValue{pair}
	documents[1].CreatedAt = now.Add(-FullNoveltyAge)
	graph := rankGraphFixture(t, t.TempDir(), documents, map[string][]string{"source": {"candidate"}})
	ranker, err := BuildRanker(documents, graph, RankOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	results, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %#v, want one", results)
	}
	assertClose(t, "all-signal score", results[0].Score, 1)
	if got, want := reasonKinds(results[0].Reasons), []ReasonKind{ReasonText, ReasonGraph, ReasonMetadata}; !reflect.DeepEqual(got, want) {
		t.Errorf("reason kinds = %v, want %v", got, want)
	}

	if got := ranker.Weights(); got != RankingWeights() {
		t.Errorf("Weights = %#v, want %#v", got, RankingWeights())
	}
	for _, value := range []string{"balanced", "rediscover", "connect"} {
		if mode, err := ParseMode(value); err != nil || string(mode) != value {
			t.Errorf("ParseMode(%q) = %q, %v", value, mode, err)
		}
	}
	if _, err := ParseMode("search"); err == nil {
		t.Error("ParseMode(search) succeeded")
	}
	if _, err := ranker.Rank("Missing.md", ModeBalanced); err == nil || !strings.Contains(err.Error(), "not indexed") {
		t.Errorf("missing-source Rank error = %v", err)
	}
	if _, err := ranker.Rank("Source.md", Mode("search")); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("unknown-mode Rank error = %v", err)
	}
	var nilRanker *Ranker
	if _, err := nilRanker.Rank("Source.md", ModeBalanced); err == nil || !strings.Contains(err.Error(), "nil ranker") {
		t.Errorf("nil Rank error = %v", err)
	}
	single, err := BuildRanker(documents[:1], nil, RankOptions{Now: fixedRankTime})
	if err != nil {
		t.Fatalf("single-document BuildRanker: %v", err)
	}
	empty, err := single.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("single-document Rank: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("single-document results = %#v, want non-nil empty slice", empty)
	}
}

func fixedRankTime() time.Time {
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
}

func rankDocumentFixture(path, title, prose string) Document {
	return Document{
		Path:  path,
		Title: title,
		Fields: []TextField{{
			Kind:       TextProse,
			Text:       prose,
			Weight:     ProseWeight,
			BlockIndex: 0,
		}},
	}
}

func rankGraphFixture(t *testing.T, root string, documents []Document, forward map[string][]string) *query.Graph {
	t.Helper()
	graph := &query.Graph{
		Forward:         make(map[string][]string, len(forward)),
		Backward:        make(map[string][]string),
		Titles:          make(map[string]struct{}, len(documents)),
		PathsByTitle:    make(map[string]string, len(documents)),
		OriginalTitles:  make(map[string]string, len(documents)),
		NoteKeysByTitle: make(map[string][]string),
		Root:            root,
	}
	for _, document := range documents {
		key := strings.ToLower(strings.TrimSuffix(filepath.ToSlash(document.Path), filepath.Ext(document.Path)))
		graph.Titles[key] = struct{}{}
		graph.PathsByTitle[key] = filepath.Join(root, filepath.FromSlash(document.Path))
		graph.OriginalTitles[key] = document.Title
	}
	for source, targets := range forward {
		graph.Forward[source] = append([]string(nil), targets...)
		sort.Strings(graph.Forward[source])
	}
	return graph
}

func resultPaths(results []Result) []string {
	paths := make([]string, len(results))
	for index, result := range results {
		paths[index] = result.Path
	}
	return paths
}

func resultsByPath(results []Result) map[string]Result {
	byPath := make(map[string]Result, len(results))
	for _, result := range results {
		byPath[result.Path] = result
	}
	return byPath
}

func reasonKinds(reasons []Reason) []ReasonKind {
	kinds := make([]ReasonKind, len(reasons))
	for index, reason := range reasons {
		kinds[index] = reason.Kind
	}
	return kinds
}

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("%s = %.17g, want %.17g", name, got, want)
	}
}
