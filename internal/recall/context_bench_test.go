package recall

import (
	"fmt"
	"testing"
)

var benchmarkContextBundle ContextBundle

func BenchmarkContextBundle(b *testing.B) {
	for _, size := range []int{500, 5_000} {
		documents := syntheticContextCorpus(size)
		ranker, err := BuildRanker(documents, nil, RankOptions{Now: fixedRankTime})
		if err != nil {
			b.Fatal(err)
		}
		results, err := ranker.Rank(documents[0].Path, ModeBalanced)
		if err != nil {
			b.Fatal(err)
		}
		options := DefaultContextBundleOptions()

		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bundle, err := BuildContextBundle(documents[0], documents, results, options)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkContextBundle = bundle
			}
		})
	}
}

func syntheticContextCorpus(size int) []Document {
	documents := make([]Document, 0, size)
	for index := range size {
		body := fmt.Sprintf(
			"# Research topic %02d\n\nEvidence synthesis for cluster %02d with method observation analysis reference %03d.\n",
			index%50,
			index%20,
			index%200,
		)
		documents = append(documents, contextDocumentFixture(
			fmt.Sprintf("note-%05d.md", index),
			fmt.Sprintf("Research topic %02d artifact %05d", index%50, index),
			body,
		))
	}
	return documents
}
