package schema

import "github.com/sglavoie/brainnotes/internal/frontmatter"

func buildFront(pairs ...any) *frontmatter.Map {
	m := frontmatter.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return &m
}
