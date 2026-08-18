package config

import (
	"reflect"
	"testing"
)

func TestCloneIsDeepAndIndependent(t *testing.T) {
	openBrowser := true
	original := &Config{
		Vault:      "/vault",
		SourcePath: "/vault/brainnotes.toml",
		Ignore:     []string{"Archive"},
		Frontmatter: FrontmatterConfig{Properties: map[string]PropertyDefinition{
			"tags": {Type: KindList, Items: KindString, Allowed: []string{"a", "b"}},
		}},
		Fzf:     FzfConfig{Args: []string{"--height", "40%"}},
		Serve:   ServeConfig{Addr: "127.0.0.1:8080", OpenBrowser: &openBrowser},
		New:     NewConfig{Routes: map[string]string{"note": "Notes"}},
		Related: RelatedConfig{MetadataProperties: []string{"tags"}},
		Brief: BriefConfig{
			ExcludeFolders: []string{"Archive"},
			ExcludeTypes:   []string{"log"},
		},
		Capture: CaptureConfig{InboxNote: "Inbox.md", Heading: "## Captures"},
		Agent:   AgentConfig{Command: []string{"echo", "hi"}},
		Cockpit: CockpitConfig{
			DisabledRules:            []string{"orphan"},
			Priority:                 []string{"broken_link"},
			LongTailIgnoreProperties: []string{"created"},
			ReviewStatuses:           []string{"active"},
			WIPStatuses:              []string{"doing"},
			MaxCaptures:              20,
		},
	}

	clone := original.Clone()
	if !reflect.DeepEqual(original, clone) {
		t.Fatal("clone should equal the original")
	}

	clone.Ignore[0] = "mutated"
	clone.Frontmatter.Properties["tags"] = PropertyDefinition{Type: KindString}
	clone.Frontmatter.Properties["new"] = PropertyDefinition{Type: KindString}
	clone.Fzf.Args[0] = "mutated"
	clone.New.Routes["note"] = "mutated"
	clone.Related.MetadataProperties[0] = "mutated"
	clone.Brief.ExcludeFolders[0] = "mutated"
	clone.Brief.ExcludeTypes[0] = "mutated"
	clone.Agent.Command[0] = "mutated"
	clone.Cockpit.DisabledRules[0] = "mutated"
	clone.Cockpit.Priority[0] = "mutated"
	clone.Cockpit.LongTailIgnoreProperties[0] = "mutated"
	clone.Cockpit.ReviewStatuses[0] = "mutated"
	clone.Cockpit.WIPStatuses[0] = "mutated"
	clone.Capture.InboxNote = "mutated"
	clone.Capture.Heading = "mutated"
	clone.Cockpit.MaxCaptures = 99
	*clone.Serve.OpenBrowser = false

	if original.Ignore[0] != "Archive" {
		t.Fatal("Ignore leaked")
	}
	if def := original.Frontmatter.Properties["tags"]; def.Type != KindList {
		t.Fatal("Properties value leaked")
	}
	if _, ok := original.Frontmatter.Properties["new"]; ok {
		t.Fatal("Properties map leaked")
	}
	if original.Fzf.Args[0] != "--height" {
		t.Fatal("Fzf.Args leaked")
	}
	if original.New.Routes["note"] != "Notes" {
		t.Fatal("New.Routes leaked")
	}
	if original.Related.MetadataProperties[0] != "tags" {
		t.Fatal("Related.MetadataProperties leaked")
	}
	if original.Brief.ExcludeFolders[0] != "Archive" || original.Brief.ExcludeTypes[0] != "log" {
		t.Fatal("Brief exclusions leaked")
	}
	if original.Agent.Command[0] != "echo" {
		t.Fatal("Agent.Command leaked")
	}
	if original.Cockpit.DisabledRules[0] != "orphan" ||
		original.Cockpit.Priority[0] != "broken_link" ||
		original.Cockpit.LongTailIgnoreProperties[0] != "created" ||
		original.Cockpit.ReviewStatuses[0] != "active" ||
		original.Cockpit.WIPStatuses[0] != "doing" {
		t.Fatal("Cockpit slices leaked")
	}
	if original.Capture.InboxNote != "Inbox.md" || original.Capture.Heading != "## Captures" {
		t.Fatal("Capture fields leaked")
	}
	if original.Cockpit.MaxCaptures != 20 {
		t.Fatal("Cockpit.MaxCaptures leaked")
	}
	if !*original.Serve.OpenBrowser {
		t.Fatal("Serve.OpenBrowser pointer leaked")
	}
}

func TestClonePropertyAllowedIsIndependent(t *testing.T) {
	original := PropertyDefinition{Type: KindString, Allowed: []string{"a"}}
	clone := original.Clone()
	clone.Allowed[0] = "mutated"
	if original.Allowed[0] != "a" {
		t.Fatal("Allowed leaked")
	}
}

func TestCloneNilConfigIsNil(t *testing.T) {
	var cfg *Config
	if cfg.Clone() != nil {
		t.Fatal("cloning a nil config should return nil")
	}
}
