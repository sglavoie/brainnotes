package recall

import (
	"fmt"
	"testing"
)

var benchmarkRankResults []Result

func BenchmarkRank(b *testing.B) {
	for _, size := range []int{500, 5_000} {
		documents := syntheticLexicalCorpus(size)
		ranker, err := BuildRanker(documents, nil, RankOptions{Now: fixedRankTime})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				results, err := ranker.Rank("note-00000.md", ModeBalanced)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkRankResults = results
			}
		})
	}
}

func BenchmarkBuildRankerAndRank(b *testing.B) {
	for _, size := range []int{500, 5_000} {
		documents := syntheticLexicalCorpus(size)
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				ranker, err := BuildRanker(documents, nil, RankOptions{Now: fixedRankTime})
				if err != nil {
					b.Fatal(err)
				}
				results, err := ranker.Rank("note-00000.md", ModeBalanced)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkRankResults = results
			}
		})
	}
}
