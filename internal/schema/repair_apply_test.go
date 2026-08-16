package schema

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

func TestApplyRepairActionPreservesTimestampSpelling(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		definitions map[string]config.PropertyDefinition
		property    string
		want        string
		avoid       string
	}{
		{
			name:        "declared datetime scalar",
			source:      "---\npublished: '2026-01-02T00:00:00Z'\n---\n",
			definitions: map[string]config.PropertyDefinition{"published": {Type: config.KindDatetime}},
			property:    "published",
			want:        "published: 2026-01-02T00:00:00Z",
			avoid:       "published: 2026-01-02\n",
		},
		{
			name:        "declared datetime list dedup",
			source:      "---\ntimeline:\n  - 2026-01-02T00:00:00Z\n  - 2026-01-02T00:00:00Z\n---\n",
			definitions: map[string]config.PropertyDefinition{"timeline": {Type: config.KindList, Items: config.KindDatetime}},
			property:    "timeline",
			want:        "- 2026-01-02T00:00:00Z",
			avoid:       "- 2026-01-02\n",
		},
		{
			name:        "declared date control",
			source:      "---\nreviewed: '2026-01-02'\n---\n",
			definitions: map[string]config.PropertyDefinition{"reviewed": {Type: config.KindDate}},
			property:    "reviewed",
			want:        "reviewed: 2026-01-02\n",
			avoid:       "reviewed: 2026-01-02T00:00:00Z",
		},
		{
			name:     "undeclared dedup keeps first equal timestamp spelling",
			source:   "---\ntimeline:\n  - 2026-01-02T00:00:00Z\n  - 2026-01-02\n---\n",
			property: "timeline",
			want:     "- 2026-01-02T00:00:00Z",
			avoid:    "- 2026-01-02\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, err := frontmatter.Parse([]byte(test.source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			result := PlanRepairs(&doc.Front, test.definitions)
			var action *RepairAction
			for index := range result.Actions {
				if result.Actions[index].Property == test.property {
					action = &result.Actions[index]
					break
				}
			}
			if action == nil {
				t.Fatalf("actions = %#v, want repair for %s", result.Actions, test.property)
			}
			ApplyRepairAction(&doc.Front, *action, test.definitions)
			encoded, err := frontmatter.Marshal(doc, nil)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := string(encoded)
			if !strings.Contains(got, test.want) || strings.Contains(got, test.avoid) {
				t.Fatalf("timestamp spelling mismatch:\n%s", got)
			}
		})
	}
}
