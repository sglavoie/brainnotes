package recall

import (
	"fmt"
	"testing"
)

var (
	benchmarkLexicalIndex *LexicalIndex
	benchmarkLexicalMatch LexicalMatch
)

func BenchmarkLexical(b *testing.B) {
	for _, size := range []int{500, 5_000} {
		documents := syntheticLexicalCorpus(size)
		b.Run(fmt.Sprintf("%d-notes", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				index, err := BuildLexicalIndex(documents)
				if err != nil {
					b.Fatal(err)
				}
				match, ok := index.Compare("note-00000.md", fmt.Sprintf("note-%05d.md", size-1))
				if !ok {
					b.Fatal("benchmark comparison paths were not indexed")
				}
				benchmarkLexicalIndex = index
				benchmarkLexicalMatch = match
			}
		})
	}
}

func syntheticLexicalCorpus(size int) []Document {
	documents := make([]Document, 0, size)
	for index := range size {
		documents = append(documents, lexicalDocument(
			fmt.Sprintf("note-%05d.md", index),
			fmt.Sprintf("Research topic%02d artifact%05d", index%50, index),
			fmt.Sprintf("Evidence synthesis cluster%02d", index%20),
			fmt.Sprintf(
				"Common multilingual corpus vocabulary method observation analysis topic%02d cluster%02d reference%03d",
				index%50,
				index%20,
				index%200,
			),
		))
	}
	return documents
}
