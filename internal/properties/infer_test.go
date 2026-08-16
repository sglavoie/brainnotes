package properties

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func proposalFor(t *testing.T, files map[string]string, name string) *Proposal {
	t.Helper()

	root, notes := testVault(t, files)
	index := Build(root, notes, frontmatterConfig(nil))
	return propertyByName(t, index, name).Proposal
}

func TestProposal_InfersUnanimousScalarKind(t *testing.T) {
	proposal := proposalFor(t, map[string]string{
		"Notes/Alpha.md": "---\nrank: 1\n---\n",
		"Notes/Beta.md":  "---\nrank: 2\n---\n",
	}, "rank")

	if proposal == nil {
		t.Fatalf("no proposal for a property whose values are unanimously int")
	}
	if proposal.Type != config.KindInt || proposal.Items != "" {
		t.Fatalf("proposal = {type:%q items:%q}, want int with no items", proposal.Type, proposal.Items)
	}
	if proposal.Notes != 2 {
		t.Fatalf("Notes = %d, want 2", proposal.Notes)
	}
	if !strings.Contains(proposal.TOML, "[frontmatter.properties.rank]") {
		t.Fatalf("TOML = %q, want a canonical definition block", proposal.TOML)
	}
	if !strings.Contains(proposal.TOML, `type = "int"`) {
		t.Fatalf("TOML = %q, want the inferred type", proposal.TOML)
	}
	if strings.Contains(proposal.TOML, "required") || strings.Contains(proposal.TOML, "allowed") {
		t.Fatalf("TOML = %q, must never infer required or allowed", proposal.TOML)
	}
}

func TestProposal_InfersListWithElementKind(t *testing.T) {
	proposal := proposalFor(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - go\n  - web\n---\n",
		"Notes/Beta.md":  "---\ntags:\n  - go\n---\n",
	}, "tags")

	if proposal == nil {
		t.Fatalf("no proposal for a property whose values are unanimously lists of strings")
	}
	if proposal.Type != config.KindList || proposal.Items != config.KindString {
		t.Fatalf("proposal = {type:%q items:%q}, want list of string", proposal.Type, proposal.Items)
	}
	if !strings.Contains(proposal.TOML, `items = "string"`) {
		t.Fatalf("TOML = %q, want the element kind", proposal.TOML)
	}
}

func TestProposal_RefinesCompatibleKinds(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		property  string
		wantType  config.PropertyKind
		wantItems config.PropertyKind
	}{
		{
			name: "wikilink and string collapse to string",
			files: map[string]string{
				"Notes/Alpha.md": "---\nparent: \"[[Target]]\"\n---\n",
				"Notes/Beta.md":  "---\nparent: plain\n---\n",
			},
			property: "parent",
			wantType: config.KindString,
		},
		{
			name: "date and datetime collapse to datetime",
			files: map[string]string{
				"Notes/Alpha.md": "---\nreviewed: 2026-01-02\n---\n",
				"Notes/Beta.md":  "---\nreviewed: 2026-01-02T03:04:05Z\n---\n",
			},
			property: "reviewed",
			wantType: config.KindDatetime,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			proposal := proposalFor(t, testCase.files, testCase.property)
			if proposal == nil {
				t.Fatalf("no proposal; the narrower kind's values also satisfy the wider one")
			}
			if proposal.Type != testCase.wantType || proposal.Items != testCase.wantItems {
				t.Fatalf("proposal = {type:%q items:%q}, want {type:%q items:%q}",
					proposal.Type, proposal.Items, testCase.wantType, testCase.wantItems)
			}
		})
	}
}

func TestProposal_BlockedByAmbiguousData(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		property string
	}{
		{
			name: "mixed incompatible scalar kinds",
			files: map[string]string{
				"Notes/Alpha.md": "---\nrank: 1\n---\n",
				"Notes/Beta.md":  "---\nrank: high\n---\n",
			},
			property: "rank",
		},
		{
			name: "mixed scalar and list shapes",
			files: map[string]string{
				"Notes/Alpha.md": "---\ntags: go\n---\n",
				"Notes/Beta.md":  "---\ntags:\n  - go\n---\n",
			},
			property: "tags",
		},
		{
			name: "any unsupported value",
			files: map[string]string{
				"Notes/Alpha.md": "---\nmeta: text\n---\n",
				"Notes/Beta.md":  "---\nmeta:\n  nested: 1\n---\n",
			},
			property: "meta",
		},
		{
			name: "empty list observes no element kind",
			files: map[string]string{
				"Notes/Alpha.md": "---\ntags: []\n---\n",
			},
			property: "tags",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if proposal := proposalFor(t, testCase.files, testCase.property); proposal != nil {
				t.Fatalf("proposed %+v; ambiguous data must block inference", proposal)
			}
		})
	}
}

func TestProposal_NotOfferedForConfiguredProperty(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nrank: 1\n---\n",
	})

	index := Build(root, notes, frontmatterConfig(map[string]config.PropertyDefinition{
		"rank": {Type: config.KindString},
	}))

	if proposal := propertyByName(t, index, "rank").Proposal; proposal != nil {
		t.Fatalf("proposed %+v for an already configured property", proposal)
	}
	if got, want := index.Totals.Proposals, 0; got != want {
		t.Fatalf("Totals.Proposals = %d, want %d", got, want)
	}
}

func TestProposal_NotOfferedWithoutNotes(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nstatus: active\n---\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	if _, ok := index.Property("absent"); ok {
		t.Fatalf("an unused, unconfigured property should not be indexed at all")
	}

	state := inferenceState{}
	if proposal := state.proposal("absent", 0); proposal != nil {
		t.Fatalf("proposed %+v from zero observations", proposal)
	}
}

func TestProposal_DefinitionRoundTrips(t *testing.T) {
	proposal := proposalFor(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - \"[[Target]]\"\n---\n",
	}, "tags")

	if proposal == nil {
		t.Fatalf("no proposal for a unanimous list of wikilinks")
	}
	def := proposal.Definition()
	if def.Type != config.KindList || def.Items != config.KindWikilink {
		t.Fatalf("Definition() = %+v, want list of wikilink", def)
	}
	if def.Required || len(def.Allowed) != 0 {
		t.Fatalf("Definition() = %+v, must never carry inferred required or allowed", def)
	}
}

func TestObservedKinds_IncludesListShapeAndElementKinds(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - go\n  - 1\n---\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	kinds := kindSet(propertyByName(t, index, "tags").ObservedKinds)

	for _, want := range []Kind{config.KindList, config.KindString, config.KindInt} {
		if !kinds[want] {
			t.Fatalf("ObservedKinds missing %q; got %v", want, kinds)
		}
	}
}
