package pins

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// ErrNeighborMissing is returned when a drag names a neighbour that is no
// longer pinned, which means the client's view of the list is stale.
var ErrNeighborMissing = errors.New("pins: neighbour note is no longer pinned")

// ErrTargetMissing is returned when PlanMove is asked to move a note that is
// not currently pinned.
var ErrTargetMissing = errors.New("pins: target note is not pinned")

// ErrStaleGap is returned when both named neighbours are still pinned but no
// longer sit either side of one gap, which means the client's view of the list
// is stale even though every note it named still exists.
var ErrStaleGap = errors.New("pins: neighbours no longer describe a gap in the pinned list")

// ErrEmptyTarget is returned when no target note was named.
var ErrEmptyTarget = errors.New("pins: empty target note")

var errOutsideRoot = errors.New("pins: path outside vault root")

// Assignment is one note whose `pinned` property must be written to Rank.
type Assignment struct {
	RelPath string
	Rank    float64
}

// Plan is the set of writes that realize a pin operation. A plan with no
// assignments is a no-op. Renormalized reports that the float gap collapsed
// and the whole list was rewritten to 1.0..N.0 instead of a single note being
// touched; assignments whose rank is already correct are omitted even then, so
// a renormalize still writes as few files as it can.
type Plan struct {
	Assignments  []Assignment
	Renormalized bool
}

// PlanAppend places target at the end of the pinned list, at max(existing)+1.0
// or at 1.0 when nothing is ranked yet. target may already be pinned (an
// unranked pin being re-pinned), in which case it is moved to the end.
func PlanAppend(entries []Entry, target string) (Plan, error) {
	t := key(target)
	if t == "" {
		return Plan{}, ErrEmptyTarget
	}
	ordered := sortedCopy(entries)
	current, _ := find(ordered, t)
	rest := remove(ordered, t)

	top, ok := highestRank(rest)
	if !ok {
		return single(current, target, 1.0), nil
	}
	next, ok := nextRank(top)
	if !ok {
		return renormalize(append(rest, targetEntry(current, target))), nil
	}
	return single(current, target, next), nil
}

// PlanMove moves the already-pinned target into the gap the caller identified
// by its two neighbours: before is the pin that will immediately precede
// target and after is the pin that will immediately follow it. Either is empty
// at the ends of the list.
//
// The named pair must still describe one gap in the current list: adjacent in
// the list the target was removed from, with an empty before or after naming the
// real head or tail, and both empty only when the target is the sole pin.
// Anything else is ErrStaleGap.
//
// The move normally rewrites exactly one note, at the midpoint of the two
// neighbouring ranks. It falls back to a full renormalize when no float64 fits
// between them (the gap collapsed, or the neighbours share a rank) or when a
// neighbour is an unranked pin with no rank to interpolate against.
func PlanMove(entries []Entry, target, before, after string) (Plan, error) {
	t := key(target)
	if t == "" {
		return Plan{}, ErrEmptyTarget
	}
	ordered := sortedCopy(entries)
	current, ok := find(ordered, t)
	if !ok {
		return Plan{}, ErrTargetMissing
	}
	rest := remove(ordered, t)

	b, a := key(before), key(after)
	if b == t || a == t {
		return Plan{}, fmt.Errorf("pins: neighbour is the move target %q", target)
	}
	prev, prevIdx, err := neighbor(rest, b)
	if err != nil {
		return Plan{}, err
	}
	next, nextIdx, err := neighbor(rest, a)
	if err != nil {
		return Plan{}, err
	}
	if err := checkGap(rest, b, a, prevIdx, nextIdx); err != nil {
		return Plan{}, err
	}

	insertAt := len(rest)
	switch {
	case b != "":
		insertAt = prevIdx + 1
	case a != "":
		insertAt = nextIdx
	}
	full := insert(rest, current, insertAt)

	switch {
	case b == "" && a == "":
		if current.Kind == Ranked {
			return Plan{}, nil
		}
		return single(current, target, 1.0), nil
	case b == "":
		if next.Kind != Ranked {
			return renormalize(full), nil
		}
		head := next.Rank - 1.0
		if !isFinite(head) || head >= next.Rank {
			return renormalize(full), nil
		}
		return single(current, target, head), nil
	case a == "":
		if prev.Kind != Ranked {
			return renormalize(full), nil
		}
		tail, ok := nextRank(prev.Rank)
		if !ok {
			return renormalize(full), nil
		}
		return single(current, target, tail), nil
	default:
		if prev.Kind != Ranked || next.Kind != Ranked {
			return renormalize(full), nil
		}
		mid, ok := midpoint(prev.Rank, next.Rank)
		if !ok {
			return renormalize(full), nil
		}
		return single(current, target, mid), nil
	}
}

// checkGap rejects a neighbour pair that no longer straddles a single gap.
// Existence alone is not enough: a pair with a note between it interpolates a
// rank that lands somewhere other than where the client showed the row, so the
// resulting canonical order would silently disagree with the browser while the
// request reported success.
func checkGap(rest []Entry, before, after string, prevIdx, nextIdx int) error {
	switch {
	case before == "" && after == "":
		if len(rest) > 0 {
			return ErrStaleGap
		}
	case before == "":
		if nextIdx != 0 {
			return ErrStaleGap
		}
	case after == "":
		if prevIdx != len(rest)-1 {
			return ErrStaleGap
		}
	default:
		if nextIdx != prevIdx+1 {
			return ErrStaleGap
		}
	}
	return nil
}

// neighbor resolves a named neighbour within the list the target was removed
// from. An empty name is the end of the list, not a missing note.
func neighbor(entries []Entry, k string) (Entry, int, error) {
	if k == "" {
		return Entry{}, -1, nil
	}
	for i, e := range entries {
		if key(e.RelPath) == k {
			return e, i, nil
		}
	}
	return Entry{}, -1, ErrNeighborMissing
}

// single builds the one-note plan, omitting the write when the note already
// holds that exact rank.
func single(current Entry, target string, rank float64) Plan {
	if current.Kind == Ranked && current.Rank == rank {
		return Plan{}
	}
	return Plan{Assignments: []Assignment{{RelPath: relPathOf(current, target), Rank: rank}}}
}

// renormalize rewrites the list to 1.0..N.0 in the given order, skipping notes
// that already hold their target rank.
func renormalize(order []Entry) Plan {
	plan := Plan{Renormalized: true}
	for i, e := range order {
		want := float64(i + 1)
		if e.Kind == Ranked && e.Rank == want {
			continue
		}
		plan.Assignments = append(plan.Assignments, Assignment{RelPath: e.RelPath, Rank: want})
	}
	return plan
}

func targetEntry(current Entry, target string) Entry {
	if current.RelPath != "" {
		return current
	}
	return Entry{RelPath: filepath.ToSlash(strings.TrimSpace(target)), Kind: NotPinned}
}

func relPathOf(current Entry, target string) string {
	return targetEntry(current, target).RelPath
}

func find(entries []Entry, k string) (Entry, bool) {
	for _, e := range entries {
		if key(e.RelPath) == k {
			return e, true
		}
	}
	return Entry{}, false
}

func remove(entries []Entry, k string) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if key(e.RelPath) == k {
			continue
		}
		out = append(out, e)
	}
	return out
}

func insert(entries []Entry, e Entry, at int) []Entry {
	if at < 0 {
		at = 0
	}
	if at > len(entries) {
		at = len(entries)
	}
	out := make([]Entry, 0, len(entries)+1)
	out = append(out, entries[:at]...)
	out = append(out, e)
	out = append(out, entries[at:]...)
	return out
}

func highestRank(entries []Entry) (float64, bool) {
	top, found := 0.0, false
	for _, e := range entries {
		if e.Kind != Ranked {
			continue
		}
		if !found || e.Rank > top {
			top, found = e.Rank, true
		}
	}
	return top, found
}

func sortedCopy(entries []Entry) []Entry {
	out := append([]Entry(nil), entries...)
	sortEntries(out)
	return out
}

// key is the comparison form of a vault-relative path: forward slashes and NFC,
// so a caller echoing a path back from a browser matches the on-disk form macOS
// hands back in NFD.
func key(p string) string {
	return textnorm.NFC(filepath.ToSlash(strings.TrimSpace(p)))
}
