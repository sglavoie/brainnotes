package queryexpr

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
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

// damerauLevenshtein is textnorm.EditDistance under the name this package's
// suggestion code has always used.
func damerauLevenshtein(a, b string) int {
	return textnorm.EditDistance(a, b)
}
