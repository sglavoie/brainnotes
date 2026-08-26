package query

import (
	"fmt"
	"testing"
)

var (
	benchmarkGraphMetrics  *GraphMetrics
	benchmarkGraphDistance map[string]int
	benchmarkGraphPath     []string
)

// syntheticLinkGraph builds a *Graph directly so the benchmark measures metrics
// construction rather than note parsing. Every note links to four others at
// fixed strides, and each note also contributes one unresolved link target to
// Backward — the phantom-node case NewGraphMetrics must ignore.
func syntheticLinkGraph(size int) *Graph {
	g := &Graph{
		Forward:         make(map[string][]string, size),
		Backward:        make(map[string][]string, 2*size),
		Titles:          make(map[string]struct{}, size),
		PathsByTitle:    make(map[string]string, size),
		OriginalTitles:  make(map[string]string, size),
		NoteKeysByTitle: make(map[string][]string, size),
	}
	keys := make([]string, size)
	for i := range size {
		key := fmt.Sprintf("note-%05d", i)
		keys[i] = key
		g.Titles[key] = struct{}{}
		g.PathsByTitle[key] = key + ".md"
		g.OriginalTitles[key] = key
		g.NoteKeysByTitle[key] = []string{key}
	}
	for i, key := range keys {
		targets := make([]string, 0, 4)
		for _, stride := range []int{1, 7, 13, 29} {
			target := keys[(i+stride)%size]
			if target != key {
				targets = append(targets, target)
			}
		}
		g.Forward[key] = sortedUniqueStrings(targets)
		for _, target := range g.Forward[key] {
			g.Backward[target] = append(g.Backward[target], key+".md")
		}
		g.Backward[fmt.Sprintf("broken-%05d", i)] = []string{key + ".md"}
	}
	return g
}

func BenchmarkNewGraphMetrics(b *testing.B) {
	for _, size := range []int{300, 500} {
		g := syntheticLinkGraph(size)
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkGraphMetrics = NewGraphMetrics(g)
			}
		})
	}
}

func BenchmarkGraphMetricsDistances(b *testing.B) {
	for _, size := range []int{300, 500} {
		m := NewGraphMetrics(syntheticLinkGraph(size))
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkGraphDistance = m.Distances("note-00000", DirBoth, 2)
			}
		})
	}
}

func BenchmarkGraphMetricsShortestPath(b *testing.B) {
	for _, size := range []int{300, 500} {
		m := NewGraphMetrics(syntheticLinkGraph(size))
		target := fmt.Sprintf("note-%05d", size/2)
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkGraphPath = m.ShortestPath("note-00000", target, DirOut)
			}
		})
	}
}
