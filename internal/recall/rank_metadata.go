package recall

import (
	"math"
	"sort"
)

func buildMetadataIndex(documents []Document) map[string]metadataVector {
	documentFrequency := make(map[MetadataValue]int)
	uniquePairs := make(map[string][]MetadataValue, len(documents))
	for _, document := range documents {
		seen := make(map[MetadataValue]struct{}, len(document.Metadata))
		for _, pair := range document.Metadata {
			if pair.Property == "" || pair.Value == "" {
				continue
			}
			seen[pair] = struct{}{}
		}
		pairs := sortedMetadataPairs(seen)
		uniquePairs[document.Path] = pairs
		for _, pair := range pairs {
			documentFrequency[pair]++
		}
	}

	corpusSize := float64(len(documents))
	inverseFrequency := make(map[MetadataValue]float64, len(documentFrequency))
	for pair, frequency := range documentFrequency {
		inverseFrequency[pair] = math.Log((1+corpusSize)/(1+float64(frequency))) + 1
	}

	vectors := make(map[string]metadataVector, len(documents))
	for _, document := range documents {
		pairs := uniquePairs[document.Path]
		weights := make(map[MetadataValue]float64, len(pairs))
		squaredNorm := 0.0
		for _, pair := range pairs {
			weight := inverseFrequency[pair]
			weights[pair] = weight
			squaredNorm += weight * weight
		}
		vectors[document.Path] = metadataVector{
			weights: weights,
			pairs:   pairs,
			norm:    math.Sqrt(squaredNorm),
		}
	}
	return vectors
}

func sortedMetadataPairs(values map[MetadataValue]struct{}) []MetadataValue {
	if len(values) == 0 {
		return nil
	}
	pairs := make([]MetadataValue, 0, len(values))
	for pair := range values {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Property != pairs[j].Property {
			return pairs[i].Property < pairs[j].Property
		}
		return pairs[i].Value < pairs[j].Value
	})
	return pairs
}

func compareMetadata(source, candidate metadataVector) metadataMatch {
	if source.norm == 0 || candidate.norm == 0 {
		return metadataMatch{}
	}

	denominator := source.norm * candidate.norm
	contributions := make([]metadataContribution, 0, min(len(source.pairs), len(candidate.pairs)))
	score := 0.0
	for sourceIndex, candidateIndex := 0, 0; sourceIndex < len(source.pairs) && candidateIndex < len(candidate.pairs); {
		sourcePair := source.pairs[sourceIndex]
		candidatePair := candidate.pairs[candidateIndex]
		switch compareMetadataPair(sourcePair, candidatePair) {
		case -1:
			sourceIndex++
		case 1:
			candidateIndex++
		default:
			contribution := source.weights[sourcePair] * candidate.weights[candidatePair] / denominator
			score += contribution
			contributions = append(contributions, metadataContribution{
				pair:         sourcePair,
				contribution: contribution,
			})
			sourceIndex++
			candidateIndex++
		}
	}
	if score > 1 && score < 1+1e-12 {
		score = 1
	}
	sort.SliceStable(contributions, func(i, j int) bool {
		if contributions[i].contribution != contributions[j].contribution {
			return contributions[i].contribution > contributions[j].contribution
		}
		return compareMetadataPair(contributions[i].pair, contributions[j].pair) < 0
	})
	return metadataMatch{score: score, contributions: contributions}
}

func compareMetadataPair(left, right MetadataValue) int {
	if left.Property < right.Property {
		return -1
	}
	if left.Property > right.Property {
		return 1
	}
	if left.Value < right.Value {
		return -1
	}
	if left.Value > right.Value {
		return 1
	}
	return 0
}
