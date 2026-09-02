package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

// writeRelatedHuman is the default rendering: a numbered line per result with
// its relationship class, followed by at most three reason lines.
func writeRelatedHuman(w io.Writer, results []recall.Result) error {
	for index, result := range results {
		relationship := string(result.Relationship)
		if relationship == "" {
			relationship = "none"
		}
		if _, err := fmt.Fprintf(w, "%d. [%s] %s\n", index+1, relationship, result.Path); err != nil {
			return err
		}
		for reasonIndex, reason := range result.Reasons {
			if reasonIndex == 3 {
				break
			}
			if _, err := fmt.Fprintf(w, "   %s: %s\n", reason.Kind, reason.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeRelatedPaths emits absolute paths through the CLI's single `-0` writer.
// `related --paths` prints absolute paths while `find` and `search` print the
// paths their vaults walked, but the separator is one decision for all three.
func writeRelatedPaths(w io.Writer, root string, results []recall.Result, nullSep bool) error {
	paths := make([]string, len(results))
	for index, result := range results {
		paths[index] = filepath.Join(root, filepath.FromSlash(result.Path))
	}
	return writePathList(w, paths, nullSep)
}

type relatedJSONResponse struct {
	Source         relatedJSONNote     `json:"source"`
	Mode           recall.Mode         `json:"mode"`
	RankingVersion int                 `json:"ranking_version"`
	Weights        relatedJSONWeights  `json:"weights"`
	Results        []relatedJSONResult `json:"results"`
}

type relatedJSONNote struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type relatedJSONWeights struct {
	Text     float64 `json:"text"`
	Graph    float64 `json:"graph"`
	Metadata float64 `json:"metadata"`
	Recall   float64 `json:"recall"`
}

type relatedJSONResult struct {
	Rank         int                      `json:"rank"`
	Path         string                   `json:"path"`
	Title        string                   `json:"title"`
	Score        float64                  `json:"score"`
	Relationship recall.RelationshipClass `json:"relationship"`
	Bridge       *relatedJSONNote         `json:"bridge,omitempty"`
	Signals      relatedJSONSignals       `json:"signals"`
	Reasons      []relatedJSONReason      `json:"reasons"`
}

type relatedJSONSignals struct {
	Text     float64 `json:"text"`
	Graph    float64 `json:"graph"`
	Metadata float64 `json:"metadata"`
	Recall   float64 `json:"recall"`
}

type relatedJSONReason struct {
	Kind         recall.ReasonKind `json:"kind"`
	Contribution float64           `json:"contribution"`
	Detail       string            `json:"detail"`
}

func writeRelatedJSON(w io.Writer, queryResult relatedQuery) error {
	results := make([]relatedJSONResult, len(queryResult.results))
	for index, result := range queryResult.results {
		results[index] = relatedJSONResultFor(index, result)
	}

	response := relatedJSONResponse{
		Source: relatedJSONNote{
			Path:  queryResult.source.Path,
			Title: queryResult.source.Title,
		},
		Mode:           queryResult.mode,
		RankingVersion: recall.RankingVersion,
		Weights: relatedJSONWeights{
			Text:     queryResult.weights.Text,
			Graph:    queryResult.weights.Graph,
			Metadata: queryResult.weights.Metadata,
			Recall:   queryResult.weights.Recall,
		},
		Results: results,
	}

	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(response)
}

// relatedJSONResultFor projects one ranked result onto the wire shape. Bridge
// is omitted entirely unless the result carries one, which is what keeps the
// `bridge` key absent rather than null for a non-bridge relationship.
func relatedJSONResultFor(index int, result recall.Result) relatedJSONResult {
	reasons := make([]relatedJSONReason, len(result.Reasons))
	for reasonIndex, reason := range result.Reasons {
		reasons[reasonIndex] = relatedJSONReason{
			Kind:         reason.Kind,
			Contribution: reason.Contribution,
			Detail:       reason.Detail,
		}
	}

	jsonResult := relatedJSONResult{
		Rank:         index + 1,
		Path:         result.Path,
		Title:        result.Title,
		Score:        result.Score,
		Relationship: result.Relationship,
		Signals: relatedJSONSignals{
			Text:     result.Signals.Text,
			Graph:    result.Signals.Graph,
			Metadata: result.Signals.Metadata,
			Recall:   result.Signals.Recall,
		},
		Reasons: reasons,
	}
	if result.BridgePath != "" || result.BridgeTitle != "" {
		jsonResult.Bridge = &relatedJSONNote{Path: result.BridgePath, Title: result.BridgeTitle}
	}
	return jsonResult
}
