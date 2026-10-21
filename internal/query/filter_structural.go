package query

import (
	"path"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/pins"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// Folder matches notes that live in folder or one of its descendants. folder is
// vault-relative with forward slashes; an empty folder matches every note.
// root is the absolute vault root (used to compute each note's folder).
//
// Leading/trailing slashes, backslashes, and `./` segments in the user-supplied
// folder are normalized: `--folder /References`, `--folder References/`,
// `--folder ./References`, and `--folder References\Books` all behave like
// `References` (or `References/Books`).
//
// The comparison goes through comparisonKey, like tag, category, status and
// type: case-insensitive and NFC-normalized on both sides, so `folder:café`
// matches a `Café/` directory macOS stored decomposed.
func Folder(folder, root string) Predicate {
	folder = strings.TrimSpace(folder)
	want := strings.Trim(strings.ReplaceAll(folder, "\\", "/"), "/")
	want = path.Clean(want)
	if want == "." {
		want = ""
	}
	want = comparisonKey(want)
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		if want == "" {
			return true
		}
		nf, err := n.Folder(root)
		if err != nil {
			return false
		}
		nf = comparisonKey(nf)
		return nf == want || strings.HasPrefix(nf, want+"/")
	}
}

// Orphan matches notes that no *other* note in the vault links to. The graph
// is built once per find run via BuildGraph.
//
// Self-references are excluded so a note that only links to itself still
// counts as an orphan — this matches `bn backlinks`, which also excludes the
// target's own path from its referrers list.
func Orphan(g *Graph) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		for _, p := range g.BackLinksForNote(n) {
			if p != n.Path {
				return false
			}
		}
		return true
	}
}

// Pinned matches notes the user has pinned, reading the raw `pinned`
// frontmatter value through pins.Classify. It needs no
// [frontmatter.properties.pinned] declaration, so it works in any vault, and it
// matches unranked pins (a hand-written `pinned: true`) as well as ranked ones.
func Pinned() Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, present := fm.Get(pins.PropertyName)
		_, kind := pins.Classify(raw, present)
		return kind != pins.NotPinned
	}
}

// MinBacklinks matches notes with at least n inbound links from other notes.
func MinBacklinks(g *Graph, n int) Predicate {
	return func(note *vault.Note) bool {
		if note == nil {
			return false
		}
		return g.InDegreeForNote(note) >= n
	}
}

// MaxBacklinks matches notes with at most n inbound links from other notes.
func MaxBacklinks(g *Graph, n int) Predicate {
	return func(note *vault.Note) bool {
		if note == nil {
			return false
		}
		return g.InDegreeForNote(note) <= n
	}
}

// MinLinks matches notes with at least n outgoing links to other notes.
func MinLinks(g *Graph, n int) Predicate {
	return func(note *vault.Note) bool {
		if note == nil {
			return false
		}
		return g.OutDegreeForNote(note) >= n
	}
}

// MaxLinks matches notes with at most n outgoing links to other notes.
func MaxLinks(g *Graph, n int) Predicate {
	return func(note *vault.Note) bool {
		if note == nil {
			return false
		}
		return g.OutDegreeForNote(note) <= n
	}
}

// DeadEnd matches notes with zero outgoing links to other notes, unless the
// note explicitly opts out with the YAML boolean `dead_end: false`. Values of
// any other type, including the string "false", do not exempt a note.
func DeadEnd(g *Graph) Predicate {
	return func(note *vault.Note) bool {
		if note == nil || g.OutDegreeForNote(note) != 0 {
			return false
		}

		fm, err := note.Front()
		if err != nil {
			return true
		}
		value, ok := fm.Get("dead_end")
		if !ok {
			return true
		}
		deadEndValue, isBool := value.(bool)
		return !isBool || deadEndValue
	}
}

// Age matches notes whose date at prop satisfies the comparison. op is one of
// ">", "<", or "=":
//   - ">": note is older than dur.
//   - "<": note is younger than dur.
//   - "=": note's age in days equals dur in days (e.g. =0d → today).
//
// Ages are measured on calendar dates in the local time zone. This matters
// because YAML date scalars like `2026-05-19` parse as midnight UTC, so a naive
// `time.Since` measurement can land on the wrong side of a day boundary when the
// local zone differs from UTC. The frontmatter date is interpreted as a
// local-time date when its time-of-day component is zero and the zone is UTC
// (the YAML-date convention).
//
// "=" and whole-day ">"/"<" durations compare local day numbers, so for any
// whole number of days exactly one of the three operators matches. A ">" or "<"
// duration with a sub-day remainder (e.g. `<36h`) compares elapsed time since
// the local reading of the value instead, which keeps hour-level queries on
// timestamped properties meaningful.
//
// Notes without a parseable timestamp at prop never match.
func Age(op string, dur time.Duration, prop string) Predicate {
	return ageAt(op, dur, prop, time.Now)
}

// ageAt is Age with an injectable clock; the clock's location is the local zone
// day boundaries are evaluated in.
func ageAt(op string, dur time.Duration, prop string, now func() time.Time) Predicate {
	return func(n *vault.Note) bool {
		fm, err := n.Front()
		if err != nil {
			return false
		}
		t, ok := fm.GetTime(prop)
		if !ok {
			return false
		}
		return compareAge(op, dur, t, now())
	}
}

// compareAge applies op to t's age as of now, per the rules documented on Age.
func compareAge(op string, dur time.Duration, t, now time.Time) bool {
	if op == "=" {
		return LocalDayAge(t, now) == wholeDays(dur)
	}
	if dur%ageDay != 0 {
		elapsed := now.Sub(localDate(t, now.Location()))
		return (op == ">" && elapsed > dur) || (op == "<" && elapsed < dur)
	}
	age := LocalDayAge(t, now)
	return (op == ">" && age > wholeDays(dur)) || (op == "<" && age < wholeDays(dur))
}

const ageDay = 24 * time.Hour

// wholeDays truncates dur to a whole number of days, so `=23h` means `=0d`.
func wholeDays(dur time.Duration) int {
	return int(dur / ageDay)
}

// LocalDayAge returns how many calendar days t lies before now, with both read
// as dates in now's location. A future date is negative. A YAML date scalar
// (midnight UTC, zero time-of-day) keeps its calendar date, per LocalDate, so a
// `2026-05-19` value is one day old on 2026-05-20 at any hour in any zone.
func LocalDayAge(t, now time.Time) int {
	ny, nm, nd := now.Date()
	ty, tm, td := localDate(t, now.Location()).Date()
	// Subtracting UTC midnights counts calendar days without a DST-shortened
	// or lengthened local day skewing the division.
	diff := time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC))
	return int(diff / ageDay)
}

// localDate returns t shifted into loc. When t has no time-of-day component
// and is in UTC — the YAML-date convention — its calendar date is preserved
// rather than converted (so a `2026-05-19` value remains 2026-05-19 in any
// local zone).
func localDate(t time.Time, loc *time.Location) time.Time {
	if t.Location() == time.UTC && t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	return t.In(loc)
}
