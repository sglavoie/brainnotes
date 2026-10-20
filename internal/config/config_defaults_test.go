package config

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoad_EmptyConfigFileUsesDefaults(t *testing.T) {
	// An empty config file is valid TOML; all defaults should be applied.
	root := newVault(t, "")

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(root, FileName); cfg.SourcePath != want {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, want)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("Templates.Dir: got %q, want %q", cfg.Templates.Dir, "Templates")
	}
	for _, want := range DefaultIgnore {
		if !slices.Contains(cfg.Ignore, want) {
			t.Errorf("default ignore %q missing: %v", want, cfg.Ignore)
		}
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	cfg, err := loadVault(t, newVault(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.Dir != "Templates" {
		t.Errorf("Templates.Dir: got %q, want %q", cfg.Templates.Dir, "Templates")
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("Templates.DefaultExtension: got %q, want %q", cfg.Templates.DefaultExtension, ".md")
	}
	if cfg.Serve.Addr != DefaultServeAddr {
		t.Errorf("Serve.Addr: got %q, want %q", cfg.Serve.Addr, DefaultServeAddr)
	}
	if !cfg.Serve.BrowserEnabled() {
		t.Errorf("Serve.BrowserEnabled: got false, want true")
	}
	if len(cfg.Frontmatter.Properties) != 0 {
		t.Errorf("Properties should have no implicit runtime defaults, got %+v", cfg.Frontmatter.Properties)
	}
	if !slices.Equal(cfg.Related.MetadataProperties, DefaultRelatedMetadataProperties) {
		t.Errorf(
			"Related.MetadataProperties: got %v, want %v",
			cfg.Related.MetadataProperties,
			DefaultRelatedMetadataProperties,
		)
	}
	if cfg.Brief.ExcludeFolders == nil || len(cfg.Brief.ExcludeFolders) != 0 {
		t.Errorf("Brief.ExcludeFolders: got %#v, want empty non-nil slice", cfg.Brief.ExcludeFolders)
	}
	if cfg.Brief.ExcludeTypes == nil || len(cfg.Brief.ExcludeTypes) != 0 {
		t.Errorf("Brief.ExcludeTypes: got %#v, want empty non-nil slice", cfg.Brief.ExcludeTypes)
	}
	if cfg.Agent.Command == nil || len(cfg.Agent.Command) != 0 {
		t.Errorf("Agent.Command: got %#v, want empty non-nil slice", cfg.Agent.Command)
	}
	if cfg.Agent.TimeoutSeconds != DefaultAgentTimeoutSeconds {
		t.Errorf("Agent.TimeoutSeconds: got %d, want %d", cfg.Agent.TimeoutSeconds, DefaultAgentTimeoutSeconds)
	}
	if cfg.Capture.InboxNote != DefaultCaptureInboxNote {
		t.Errorf("Capture.InboxNote: got %q, want %q", cfg.Capture.InboxNote, DefaultCaptureInboxNote)
	}
	if cfg.Capture.Heading != DefaultCaptureHeading {
		t.Errorf("Capture.Heading: got %q, want %q", cfg.Capture.Heading, DefaultCaptureHeading)
	}
	// Defaults merged into Ignore.
	for _, want := range DefaultIgnore {
		if !slices.Contains(cfg.Ignore, want) {
			t.Errorf("Ignore missing default %q (got %v)", want, cfg.Ignore)
		}
	}
}

func TestDefaultTemplate_OnlyActivatesPropertyDefinitions(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(DefaultTemplate(), &cfg); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	want := Config{}
	if cfg.Vault != want.Vault {
		t.Errorf("Vault: got %q, want %q", cfg.Vault, want.Vault)
	}
	if len(cfg.Ignore) != 0 {
		t.Errorf("Ignore should be empty, got %v", cfg.Ignore)
	}
	if cfg.Templates.Dir != "" || cfg.Templates.DefaultExtension != "" {
		t.Errorf("Templates should be zero, got %+v", cfg.Templates)
	}
	if len(cfg.Fzf.Args) != 0 {
		t.Errorf("Fzf.Args should be empty, got %v", cfg.Fzf.Args)
	}
	if len(cfg.New.Routes) != 0 {
		t.Errorf("New.Routes should be empty, got %v", cfg.New.Routes)
	}
	if len(cfg.Brief.ExcludeFolders) != 0 || len(cfg.Brief.ExcludeTypes) != 0 {
		t.Errorf("Brief should be zero, got %+v", cfg.Brief)
	}
	if cfg.Serve.Addr != "" || cfg.Serve.OpenBrowser != nil {
		t.Errorf("Serve should be zero, got %+v", cfg.Serve)
	}
	if cfg.Capture.InboxNote != "" || cfg.Capture.Heading != "" {
		t.Errorf("Capture should be zero, got %+v", cfg.Capture)
	}
}

func TestLoad_CaptureInboxNoteNormalization(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "trimmed", value: `"  Inbox  "`, want: "Inbox.md"},
		{name: "md suffix appended", value: `"Notes/Inbox"`, want: "Notes/Inbox.md"},
		{name: "existing suffix kept", value: `"Notes/Inbox.MD"`, want: "Notes/Inbox.MD"},
		{name: "backslashes converted", value: `"Notes\\Inbox.md"`, want: "Notes/Inbox.md"},
		{name: "cleaned", value: `"./Notes//Inbox.md"`, want: "Notes/Inbox.md"},
		{name: "absolute falls back", value: `"/tmp/Inbox.md"`, want: DefaultCaptureInboxNote},
		{name: "parent traversal falls back", value: `"../Inbox.md"`, want: DefaultCaptureInboxNote},
		{name: "empty falls back", value: `"   "`, want: DefaultCaptureInboxNote},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadTOML(t, "[capture]\ninbox_note = "+tc.value+"\n")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Capture.InboxNote != tc.want {
				t.Fatalf("Capture.InboxNote = %q, want %q", cfg.Capture.InboxNote, tc.want)
			}
		})
	}
}

func TestLoad_CaptureUncommentedTemplateBlockIsKnown(t *testing.T) {
	cfg, err := loadTOML(t, "[capture]\ninbox_note = \"Inbox.md\"\nheading = \"## Captures\"\n\n[cockpit]\nmax_captures = 20\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Capture.InboxNote != "Inbox.md" || cfg.Capture.Heading != "## Captures" {
		t.Fatalf("Capture = %+v, want the configured values", cfg.Capture)
	}
	if cfg.Cockpit.MaxCaptures != 20 {
		t.Fatalf("Cockpit.MaxCaptures = %d, want 20", cfg.Cockpit.MaxCaptures)
	}
}

func TestLoad_CaptureHeadingValidation(t *testing.T) {
	t.Run("accepts every ATX level", func(t *testing.T) {
		for _, heading := range []string{"# Captures", "### Captures", "###### Deep"} {
			cfg, err := loadTOML(t, "[capture]\nheading = "+strconv.Quote(heading)+"\n")
			if err != nil {
				t.Fatalf("Load(%q): %v", heading, err)
			}
			if cfg.Capture.Heading != heading {
				t.Fatalf("Capture.Heading = %q, want %q", cfg.Capture.Heading, heading)
			}
		}
	})

	for _, tc := range []struct{ name, heading string }{
		{name: "no hash", heading: "Captures"},
		{name: "no title", heading: "##"},
		{name: "no space", heading: "##Captures"},
		{name: "too deep", heading: "####### Captures"},
		{name: "embedded newline", heading: "## Captures\nmore"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadTOML(t, "[capture]\nheading = "+strconv.Quote(tc.heading)+"\n")
			if err == nil {
				t.Fatalf("Load(%q): got nil error, want a capture.heading error", tc.heading)
			}
			if !strings.Contains(err.Error(), "capture.heading") {
				t.Fatalf("Load error = %v, want mention of capture.heading", err)
			}
		})
	}
}

func TestLoad_CaptureInboxRejectsUnwalkedLocations(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{name: "templates dir", body: "[capture]\ninbox_note = \"Templates/Inbox.md\"\n"},
		{name: "custom templates dir", body: "[templates]\ndir = \"Tpl\"\n\n[capture]\ninbox_note = \"Tpl/Inbox.md\"\n"},
		{name: "templates dir case variant", body: "[capture]\ninbox_note = \"templates/Inbox.md\"\n"},
		{name: "templates dir itself case variant", body: "[templates]\ndir = \"inbox.md\"\n\n[capture]\ninbox_note = \"Inbox.md\"\n"},
		{name: "nested templates dir case variant", body: "[templates]\ndir = \"Meta/Tpl\"\n\n[capture]\ninbox_note = \"meta/TPL/Deep/Inbox.md\"\n"},
		{name: "default ignore", body: "[capture]\ninbox_note = \"Archive/Inbox.md\"\n"},
		{name: "configured ignore", body: "ignore = [\"Drafts\"]\n\n[capture]\ninbox_note = \"Drafts/Inbox.md\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadTOML(t, tc.body)
			if err == nil {
				t.Fatal("Load: got nil error, want a capture.inbox_note error")
			}
			if !strings.Contains(err.Error(), "capture.inbox_note") {
				t.Fatalf("Load error = %v, want mention of capture.inbox_note", err)
			}
		})
	}
}

// TestLoad_CaptureInboxAcceptsWalkedLocations pins the other side of the
// containment check: only the templates dir is folded, because Walk prunes it
// with EqualFold while it matches ignore entries by exact map lookup.
func TestLoad_CaptureInboxAcceptsWalkedLocations(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{
			name: "ignore entry case variant",
			body: "ignore = [\"Drafts\"]\n\n[capture]\ninbox_note = \"drafts/Inbox.md\"\n",
			want: "drafts/Inbox.md",
		},
		{
			name: "default ignore entry case variant",
			body: "[capture]\ninbox_note = \"archive/Inbox.md\"\n",
			want: "archive/Inbox.md",
		},
		{
			name: "templates dir name prefix only",
			body: "[capture]\ninbox_note = \"TemplatesDrafts/Inbox.md\"\n",
			want: "TemplatesDrafts/Inbox.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadTOML(t, tc.body)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Capture.InboxNote != tc.want {
				t.Fatalf("Capture.InboxNote = %q, want %q", cfg.Capture.InboxNote, tc.want)
			}
		})
	}
}

// TestDefaultTemplate_DefinesStarterProperties pins the starter configuration's
// property definitions. These were previously implicit runtime defaults; the
// starter file must now spell them out because nothing is injected at load time.
func TestDefaultTemplate_DefinesStarterProperties(t *testing.T) {
	root := newVault(t, DefaultTemplate())

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]PropertyDefinition{
		"categories": {Type: KindList, Items: KindWikilink},
		"tags":       {Type: KindList, Items: KindString},
		"topics":     {Type: KindList, Items: KindString},
		"org":        {Type: KindList, Items: KindWikilink},
		"type":       {Type: KindList, Items: KindString},
		"parent":     {Type: KindWikilink},
		"area":       {Type: KindWikilink},
		"pinned":     {Type: KindFloat},
	}
	if len(cfg.Frontmatter.Properties) != len(want) {
		t.Fatalf("Properties = %+v, want %d entries", cfg.Frontmatter.Properties, len(want))
	}
	for name, wantDef := range want {
		got, ok := cfg.Frontmatter.Properties[name]
		if !ok {
			t.Errorf("starter config missing property %q", name)
			continue
		}
		if got.Type != wantDef.Type || got.Items != wantDef.Items {
			t.Errorf("property %q = %+v, want %+v", name, got, wantDef)
		}
		if got.Required {
			t.Errorf("property %q should not be required by default", name)
		}
		if len(got.Allowed) != 0 {
			t.Errorf("property %q should have no allowed values by default, got %v", name, got.Allowed)
		}
	}

	if !cfg.Frontmatter.IsWikilink("categories") || !cfg.Frontmatter.IsList("categories") {
		t.Error("categories should be a wikilink list in the starter config")
	}
	if cfg.Frontmatter.IsWikilink("tags") {
		t.Error("tags should not be a wikilink list in the starter config")
	}
}

func TestDefaultTemplate_Mentions_AllKnownKeys(t *testing.T) {
	tmpl := DefaultTemplate()
	for _, want := range []string{
		"ignore", "[templates]", "[frontmatter", "[fzf]", "[serve]", "[new.routes]", "[related]", "[brief]", "[agent]",
		"[capture]", "inbox_note", "heading", "max_captures",
		"[frontmatter.properties.tags]", "items", "metadata_properties", "exclude_folders", "exclude_types", "command", "timeout_seconds",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q", want)
		}
	}
}

func TestLoad_RelatedMetadataPropertiesExplicitEmptyDisablesDefaults(t *testing.T) {
	cfg, err := loadTOML(t, `[related]
metadata_properties = []
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Related.MetadataProperties == nil {
		t.Fatal("explicit empty metadata_properties should remain a non-nil empty slice")
	}
	if len(cfg.Related.MetadataProperties) != 0 {
		t.Fatalf("MetadataProperties = %v, want empty", cfg.Related.MetadataProperties)
	}
}

func TestLoad_RelatedMetadataPropertiesNormalizeConfiguredValues(t *testing.T) {
	cfg, err := loadTOML(t, `[related]
metadata_properties = [" tags ", "topics", "tags", ""]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"tags", "topics"}
	if !slices.Equal(cfg.Related.MetadataProperties, want) {
		t.Fatalf("MetadataProperties = %v, want %v", cfg.Related.MetadataProperties, want)
	}
}

func TestServeConfig_BrowserEnabled_DefaultAndExplicitFalse(t *testing.T) {
	t.Run("default true when unset", func(t *testing.T) {
		var s ServeConfig
		if !s.BrowserEnabled() {
			t.Fatal("BrowserEnabled: got false, want true when open_browser is unset")
		}
	})

	t.Run("explicit false disables browser", func(t *testing.T) {
		disabled := false
		s := ServeConfig{OpenBrowser: &disabled}
		if s.BrowserEnabled() {
			t.Fatal("BrowserEnabled: got true, want false when open_browser=false")
		}
	})
}
