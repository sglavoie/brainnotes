package recall

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func selectedRankerFixture(t *testing.T, now time.Time) *Ranker {
	t.Helper()
	documents := []Document{
		rankDocumentFixture("Source.md", "Source", "shared lexical focus"),
		{Path: "Dismissed.md", Title: "Dismissed"},
		{Path: "Snoozed.md", Title: "Snoozed"},
		rankDocumentFixture("TextOnly.md", "Text Only", "shared lexical focus"),
	}
	graph := rankGraphFixture(t, t.TempDir(), documents, map[string][]string{
		"source": {"dismissed"},
	})
	signals := map[string]RecallSignal{
		"Dismissed.md": {Dismissed: true, Useful: true},
		"Snoozed.md":   {SnoozedUntil: now.Add(24 * time.Hour)},
	}
	ranker, err := BuildRanker(documents, graph, RankOptions{
		Now: func() time.Time { return now },
		Recall: RecallSignalFunc(func(_, candidatePath string) (RecallSignal, error) {
			return signals[candidatePath], nil
		}),
	})
	if err != nil {
		t.Fatalf("BuildRanker: %v", err)
	}
	return ranker
}

func TestRankSelectedPreservesOrderAndBypassesSuppression(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	ranker := selectedRankerFixture(t, now)

	balanced, err := ranker.Rank("Source.md", ModeBalanced)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	byPath := resultsByPath(balanced)
	for _, suppressed := range []string{"Dismissed.md", "Snoozed.md"} {
		if _, ok := byPath[suppressed]; ok {
			t.Fatalf("%s was not suppressed by the ordinary rank path", suppressed)
		}
	}

	wanted := []string{"Snoozed.md", "Dismissed.md", "TextOnly.md"}
	selected, err := ranker.RankSelected("Source.md", wanted)
	if err != nil {
		t.Fatalf("RankSelected: %v", err)
	}
	if got := resultPaths(selected); !reflect.DeepEqual(got, wanted) {
		t.Fatalf("selected paths = %v, want caller order %v", got, wanted)
	}

	snoozed := selected[0]
	if snoozed.Score != 0 || len(snoozed.Reasons) != 0 {
		t.Errorf("signal-free selection = %#v, want zero score and no reasons", snoozed)
	}

	dismissed := selected[1]
	if dismissed.Relationship != RelationshipDirect {
		t.Errorf("dismissed relationship = %q, want %q", dismissed.Relationship, RelationshipDirect)
	}
	assertClose(t, "dismissed graph signal", dismissed.Signals.Graph, 1)
	assertClose(t, "dismissed useful recall", dismissed.Signals.Recall, UsefulRecallBoost)
	assertClose(
		t,
		"dismissed score",
		dismissed.Score,
		GraphScoreWeight+UsefulRecallBoost*RecallScoreWeight,
	)
	if got := reasonKinds(dismissed.Reasons); !reflect.DeepEqual(got, []ReasonKind{ReasonGraph, ReasonRecall}) {
		t.Errorf("dismissed reason kinds = %v, want graph then recall", got)
	}
	if got := dismissed.Reasons[0].Detail; got != "direct link" {
		t.Errorf("dismissed graph detail = %q, want %q", got, "direct link")
	}
	if got := dismissed.Reasons[1].Detail; got != "useful feedback" {
		t.Errorf("dismissed recall detail = %q, want %q", got, "useful feedback")
	}

	if got, want := selected[2], byPath["TextOnly.md"]; !reflect.DeepEqual(got, want) {
		t.Errorf("unsuppressed selection = %#v, want the ordinary ranked result %#v", got, want)
	}

	empty, err := ranker.RankSelected("Source.md", nil)
	if err != nil {
		t.Fatalf("RankSelected with no candidates: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty selection = %#v, want no results", empty)
	}
}

func TestRankSelectedValidatesIdentities(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	ranker := selectedRankerFixture(t, now)

	tests := []struct {
		name       string
		source     string
		candidates []string
		want       string
	}{
		{
			name:       "unknown source",
			source:     "Missing.md",
			candidates: []string{"TextOnly.md"},
			want:       `source path "Missing.md" is not indexed`,
		},
		{
			name:       "unknown candidate",
			source:     "Source.md",
			candidates: []string{"TextOnly.md", "Missing.md"},
			want:       `candidate path "Missing.md" is not indexed`,
		},
		{
			name:       "duplicate candidate",
			source:     "Source.md",
			candidates: []string{"TextOnly.md", "TextOnly.md"},
			want:       `duplicate candidate path "TextOnly.md"`,
		},
		{
			name:       "source as candidate",
			source:     "Source.md",
			candidates: []string{"TextOnly.md", "Source.md"},
			want:       `candidate 1 repeats source path "Source.md"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ranker.RankSelected(test.source, test.candidates)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	var nilRanker *Ranker
	if _, err := nilRanker.RankSelected("Source.md", nil); err == nil {
		t.Error("nil ranker error = nil, want failure")
	}

	wantErr := errors.New("corrupt recall state")
	failing, err := BuildRanker(
		[]Document{{Path: "Source.md", Title: "Source"}, {Path: "Other.md", Title: "Other"}},
		nil,
		RankOptions{
			Now: fixedRankTime,
			Recall: RecallSignalFunc(func(_, _ string) (RecallSignal, error) {
				return RecallSignal{}, wantErr
			}),
		},
	)
	if err != nil {
		t.Fatalf("BuildRanker with failing provider: %v", err)
	}
	if _, err := failing.RankSelected("Source.md", []string{"Other.md"}); !errors.Is(err, wantErr) {
		t.Fatalf("RankSelected error = %v, want wrapped provider error", err)
	}
}
