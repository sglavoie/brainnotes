package pins

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func ranked(relPath string, rank float64) Entry {
	return Entry{RelPath: relPath, Rank: rank, Kind: Ranked}
}

func unranked(relPath string) Entry {
	return Entry{RelPath: relPath, Kind: Unranked}
}

func assertAssignments(t *testing.T, got Plan, wantRenormalized bool, want ...Assignment) {
	t.Helper()
	if got.Renormalized != wantRenormalized {
		t.Fatalf("Renormalized = %v, want %v", got.Renormalized, wantRenormalized)
	}
	if len(got.Assignments) != len(want) {
		t.Fatalf("assignments = %+v, want %+v", got.Assignments, want)
	}
	for i := range want {
		if got.Assignments[i] != want[i] {
			t.Fatalf("assignments = %+v, want %+v", got.Assignments, want)
		}
	}
}

func TestPlanAppend_EmptyListStartsAtOne(t *testing.T) {
	plan, err := PlanAppend(nil, "new.md")
	if err != nil {
		t.Fatalf("PlanAppend: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "new.md", Rank: 1})
}

func TestPlanAppend_UsesMaxPlusOne(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 3.5), unranked("u.md")}
	plan, err := PlanAppend(entries, "new.md")
	if err != nil {
		t.Fatalf("PlanAppend: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "new.md", Rank: 4.5})
}

func TestPlanAppend_UnrankedListStartsAtOne(t *testing.T) {
	plan, err := PlanAppend([]Entry{unranked("u.md")}, "new.md")
	if err != nil {
		t.Fatalf("PlanAppend: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "new.md", Rank: 1})
}

func TestPlanAppend_RepinningAnUnrankedNoteGivesItARealRank(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), unranked("u.md")}
	plan, err := PlanAppend(entries, "u.md")
	if err != nil {
		t.Fatalf("PlanAppend: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "u.md", Rank: 2})
}

func TestPlanAppend_EmptyTarget(t *testing.T) {
	if _, err := PlanAppend(nil, "  "); !errors.Is(err, ErrEmptyTarget) {
		t.Fatalf("err = %v, want ErrEmptyTarget", err)
	}
}

func TestPlanAppend_RenormalizesWhenRankCannotGrow(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 5), unranked("u.md"), ranked("c.md", math.MaxFloat64)}
	plan, err := PlanAppend(entries, "new.md")
	if err != nil {
		t.Fatalf("PlanAppend: %v", err)
	}
	// a.md already holds 1.0 and is omitted; u.md sorts after every ranked
	// pin and gets a real rank on the way through.
	assertAssignments(t, plan, true,
		Assignment{RelPath: "b.md", Rank: 2},
		Assignment{RelPath: "c.md", Rank: 3},
		Assignment{RelPath: "u.md", Rank: 4},
		Assignment{RelPath: "new.md", Rank: 5},
	)
}

func TestPlanMove_MidpointBetweenNeighbours(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2), ranked("c.md", 3)}
	plan, err := PlanMove(entries, "c.md", "a.md", "b.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "c.md", Rank: 1.5})
}

func TestPlanMove_ToHeadAndTail(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2), ranked("c.md", 3)}

	head, err := PlanMove(entries, "c.md", "", "a.md")
	if err != nil {
		t.Fatalf("PlanMove head: %v", err)
	}
	assertAssignments(t, head, false, Assignment{RelPath: "c.md", Rank: 0})

	tail, err := PlanMove(entries, "a.md", "c.md", "")
	if err != nil {
		t.Fatalf("PlanMove tail: %v", err)
	}
	assertAssignments(t, tail, false, Assignment{RelPath: "a.md", Rank: 4})
}

func TestPlanMove_NoOpWhenRankAlreadyCorrect(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2), ranked("c.md", 3)}
	plan, err := PlanMove(entries, "b.md", "a.md", "c.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	assertAssignments(t, plan, false)
}

func TestPlanMove_MissingNeighbour(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2)}
	if _, err := PlanMove(entries, "b.md", "gone.md", ""); !errors.Is(err, ErrNeighborMissing) {
		t.Fatalf("err = %v, want ErrNeighborMissing", err)
	}
	if _, err := PlanMove(entries, "b.md", "", "gone.md"); !errors.Is(err, ErrNeighborMissing) {
		t.Fatalf("err = %v, want ErrNeighborMissing", err)
	}
}

func TestPlanMove_MissingTarget(t *testing.T) {
	entries := []Entry{ranked("a.md", 1)}
	if _, err := PlanMove(entries, "gone.md", "a.md", ""); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("err = %v, want ErrTargetMissing", err)
	}
}

func TestPlanMove_UnrankedNeighbourForcesRenormalize(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), unranked("u.md"), unranked("v.md")}
	plan, err := PlanMove(entries, "v.md", "a.md", "u.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	assertAssignments(t, plan, true,
		Assignment{RelPath: "v.md", Rank: 2},
		Assignment{RelPath: "u.md", Rank: 3},
	)
}

func TestPlanMove_DuplicateRanksForceRenormalize(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 1), ranked("c.md", 2)}
	plan, err := PlanMove(entries, "c.md", "a.md", "b.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	// c.md lands second and already holds 2.0, so only b.md is rewritten.
	assertAssignments(t, plan, true, Assignment{RelPath: "b.md", Rank: 3})
}

func TestPlanMove_CollapsedGapRenormalizesAndSkipsUnchangedNotes(t *testing.T) {
	lo, hi, splits := exhaustGap(t, 3.0, 4.0)
	if splits < 40 {
		t.Fatalf("gap collapsed after only %d splits", splits)
	}
	entries := []Entry{ranked("a.md", 1), ranked("b.md", lo), ranked("c.md", hi), ranked("d.md", 10)}

	plan, err := PlanMove(entries, "d.md", "b.md", "c.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	// a.md already holds its renormalized rank, so it is not rewritten.
	assertAssignments(t, plan, true,
		Assignment{RelPath: "b.md", Rank: 2},
		Assignment{RelPath: "d.md", Rank: 3},
		Assignment{RelPath: "c.md", Rank: 4},
	)
}

func TestPlanMove_StaleGapIsRefused(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2), ranked("c.md", 3), ranked("d.md", 4)}

	cases := []struct {
		name          string
		target        string
		before, after string
	}{
		{name: "note between the named pair", target: "d.md", before: "a.md", after: "c.md"},
		{name: "pair in reverse order", target: "d.md", before: "c.md", after: "b.md"},
		{name: "head sentinel with a pin above the named neighbour", target: "d.md", before: "", after: "b.md"},
		{name: "tail sentinel with a pin below the named neighbour", target: "d.md", before: "b.md", after: ""},
		{name: "both sentinels while other pins remain", target: "d.md", before: "", after: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanMove(entries, tc.target, tc.before, tc.after); !errors.Is(err, ErrStaleGap) {
				t.Fatalf("err = %v, want ErrStaleGap", err)
			}
		})
	}
}

func TestPlanMove_BothSentinelsValidForTheOnlyPin(t *testing.T) {
	plan, err := PlanMove([]Entry{unranked("u.md")}, "u.md", "", "")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	assertAssignments(t, plan, false, Assignment{RelPath: "u.md", Rank: 1})

	keptRank, err := PlanMove([]Entry{ranked("a.md", 7)}, "a.md", "", "")
	if err != nil {
		t.Fatalf("PlanMove ranked: %v", err)
	}
	assertAssignments(t, keptRank, false)
}

func TestPlanMove_NeighbourIsTarget(t *testing.T) {
	entries := []Entry{ranked("a.md", 1), ranked("b.md", 2)}
	if _, err := PlanMove(entries, "b.md", "b.md", ""); err == nil {
		t.Fatal("expected an error when a neighbour is the move target")
	}
}

// TestPlanMove_RenormalizeSpansAListPastTheOldWriteCap is the planning half of
// the large-list case: a renormalize emits one assignment per pin whose rank
// moves, which for a long list is far more writes than the fixed 256 the atomic
// writer used to accept. The plan must describe the whole reordering rather than
// trim itself to fit a writer's limit.
func TestPlanMove_RenormalizeSpansAListPastTheOldWriteCap(t *testing.T) {
	const count = 300
	entries := make([]Entry, 0, count)
	for i := 0; i < count; i++ {
		// One shared rank, so no float fits between any two neighbours.
		entries = append(entries, ranked(fmt.Sprintf("n%03d.md", i), 1))
	}

	plan, err := PlanMove(entries, "n299.md", "n000.md", "n001.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	if !plan.Renormalized {
		t.Fatal("Renormalized = false, want a whole-list renumber")
	}
	// n000 already holds rank 1.0, so it is the one note the plan leaves alone.
	if len(plan.Assignments) != count-1 {
		t.Fatalf("assignments = %d, want %d", len(plan.Assignments), count-1)
	}
	want := []Assignment{
		{RelPath: "n299.md", Rank: 2},
		{RelPath: "n001.md", Rank: 3},
		{RelPath: "n002.md", Rank: 4},
	}
	for i, w := range want {
		if plan.Assignments[i] != w {
			t.Fatalf("assignment %d = %+v, want %+v", i, plan.Assignments[i], w)
		}
	}
	if last := plan.Assignments[len(plan.Assignments)-1]; last != (Assignment{RelPath: "n298.md", Rank: count}) {
		t.Fatalf("last assignment = %+v, want n298.md at %d", last, count)
	}
}

// TestPlanMove_RenormalizeSpansAListBeyondLockedPathCeiling is the planning
// side of the uncapped list. The package deliberately knows nothing about how
// many paths a writer can lock, so a list past that boundary still gets a plan
// that describes the whole reordering; trimming it to fit would write an order
// the browser never showed.
func TestPlanMove_RenormalizeSpansAListBeyondLockedPathCeiling(t *testing.T) {
	count := vault.MaxLockedPaths() + 8
	entries := make([]Entry, 0, count)
	for i := 0; i < count; i++ {
		// One shared rank, so no float fits between any two neighbours.
		entries = append(entries, ranked(fmt.Sprintf("n%05d.md", i), 1))
	}
	last := fmt.Sprintf("n%05d.md", count-1)

	plan, err := PlanMove(entries, last, "n00000.md", "n00001.md")
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	if !plan.Renormalized {
		t.Fatal("Renormalized = false, want a whole-list renumber")
	}
	// n00000 already holds rank 1.0, so it is the one note the plan leaves alone.
	if len(plan.Assignments) != count-1 {
		t.Fatalf("assignments = %d, want %d", len(plan.Assignments), count-1)
	}
	if first := plan.Assignments[0]; first != (Assignment{RelPath: last, Rank: 2}) {
		t.Fatalf("first assignment = %+v, want %s at rank 2", first, last)
	}
	wantLast := Assignment{RelPath: fmt.Sprintf("n%05d.md", count-2), Rank: float64(count)}
	if got := plan.Assignments[len(plan.Assignments)-1]; got != wantLast {
		t.Fatalf("last assignment = %+v, want %+v", got, wantLast)
	}
}
