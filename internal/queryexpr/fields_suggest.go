package queryexpr

import (
	"sort"
	"strings"
)

// suggestFields returns up to three did-you-mean candidates for an unknown
// field, drawn from the reserved names and the vault's declared frontmatter
// properties. Candidates are within Damerau-Levenshtein distance 2 and ordered
// by distance then name, so the list is deterministic for a given vault.
func suggestFields(name string, env Env) []string {
	const maxDistance = 2
	const maxSuggestions = 3

	candidates := reservedFieldNames()
	if env.Frontmatter != nil {
		candidates = append(candidates, env.Frontmatter.PropertyNames()...)
	}

	want := strings.ToLower(strings.TrimSpace(name))
	type scored struct {
		name string
		dist int
	}
	var hits []scored
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		dist := damerauLevenshtein(want, strings.ToLower(candidate))
		if dist <= maxDistance {
			hits = append(hits, scored{name: candidate, dist: dist})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > maxSuggestions {
		hits = hits[:maxSuggestions]
	}
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.name)
	}
	return out
}

// damerauLevenshtein returns the optimal string alignment distance between a
// and b, counting a transposition of two adjacent runes as one edit.
//
// It is spelled out here rather than pulled in: go.mod has nine small direct
// dependencies and a fifty-line matrix over a pair of short field names is not
// worth a tenth. Comparison is over runes, so an accented property name costs
// one edit rather than two.
func damerauLevenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	rows := make([][]int, len(ar)+1)
	for i := range rows {
		rows[i] = make([]int, len(br)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(br); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			best := minInt(rows[i-1][j]+1, rows[i][j-1]+1)
			best = minInt(best, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				best = minInt(best, rows[i-2][j-2]+1)
			}
			rows[i][j] = best
		}
	}
	return rows[len(ar)][len(br)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
