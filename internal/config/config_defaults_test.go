package config

import (
	"path/filepath"
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
		if !contains(cfg.Ignore, want) {
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
	if !equalSlices(cfg.Related.MetadataProperties, DefaultRelatedMetadataProperties) {
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
	// Defaults merged into Ignore.
	for _, want := range DefaultIgnore {
		if !contains(cfg.Ignore, want) {
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
	if !equalSlices(cfg.Related.MetadataProperties, want) {
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
