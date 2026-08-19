package recall

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func buildReasons(
	lexical LexicalMatch,
	metadata metadataMatch,
	graph graphMatch,
	recall recallMatch,
) []Reason {
	reasons := make([]Reason, 0, 4)
	if lexical.Score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonText,
			Contribution: lexical.Score * TextScoreWeight,
			Detail:       lexicalReasonDetail(lexical.Terms),
		})
	}
	if graph.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonGraph,
			Contribution: graph.score * GraphScoreWeight,
			Detail:       graphReasonDetail(graph),
		})
	}
	if metadata.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonMetadata,
			Contribution: metadata.score * MetadataScoreWeight,
			Detail:       metadataReasonDetail(metadata.contributions),
		})
	}
	if recall.score > 0 {
		reasons = append(reasons, Reason{
			Kind:         ReasonRecall,
			Contribution: recall.score * RecallScoreWeight,
			Detail:       recallReasonDetail(recall),
		})
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Contribution != reasons[j].Contribution {
			return reasons[i].Contribution > reasons[j].Contribution
		}
		if reasonOrder(reasons[i].Kind) != reasonOrder(reasons[j].Kind) {
			return reasonOrder(reasons[i].Kind) < reasonOrder(reasons[j].Kind)
		}
		return reasons[i].Detail < reasons[j].Detail
	})
	if len(reasons) > 3 {
		reasons = reasons[:3]
	}
	return reasons
}

func reasonOrder(kind ReasonKind) int {
	switch kind {
	case ReasonText:
		return 0
	case ReasonGraph:
		return 1
	case ReasonMetadata:
		return 2
	case ReasonRecall:
		return 3
	default:
		return 4
	}
}

func lexicalReasonDetail(terms []TermContribution) string {
	values := make([]string, 0, min(3, len(terms)))
	for _, term := range terms[:min(3, len(terms))] {
		values = append(values, term.Term)
	}
	return "shared terms: " + strings.Join(values, ", ")
}

func graphReasonDetail(match graphMatch) string {
	if match.relationship == RelationshipDirect {
		return "direct link"
	}
	bridge := match.bridge.title
	if bridge == "" {
		bridge = match.bridge.path
	}
	if match.relationship == RelationshipTwoHop && match.shared == 1 {
		return "two hops via " + bridge
	}
	if match.relationship == RelationshipTwoHop {
		return fmt.Sprintf("two hops via %s (%d shared neighbors)", bridge, match.shared)
	}
	if match.shared == 1 {
		return "shared neighbor " + bridge
	}
	return fmt.Sprintf("shared neighbor %s (%d shared neighbors)", bridge, match.shared)
}

func metadataReasonDetail(contributions []metadataContribution) string {
	values := make([]string, 0, min(3, len(contributions)))
	for _, contribution := range contributions[:min(3, len(contributions))] {
		values = append(values, contribution.pair.Property+"="+contribution.pair.Value)
	}
	return "shared metadata: " + strings.Join(values, ", ")
}

func recallReasonDetail(match recallMatch) string {
	parts := make([]string, 0, 3)
	if match.useful {
		parts = append(parts, "useful feedback")
	}
	if match.age > 0 {
		parts = append(parts, fmt.Sprintf("created %d days ago", int(match.age/(24*time.Hour))))
	}
	if match.recentOpenAge > 0 && match.recentOpenAge < RecentOpenWindow {
		parts = append(parts, fmt.Sprintf("opened %d days ago", int(match.recentOpenAge/(24*time.Hour))))
	}
	return strings.Join(parts, "; ")
}
