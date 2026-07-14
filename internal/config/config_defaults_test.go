package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoad_EmptyConfigFileUsesDefaults(t *testing.T) {
	// An empty config file is valid TOML; all defaults should be applied.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "")

	cfg, err := Load(vault, cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourcePath != cfgPath {
		t.Errorf("SourcePath: got %q, want %q", cfg.SourcePath, cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()

	cfg, err := Load(vault, "")
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
	wantWiki := []string{"categories", "org", "parent", "area"}
	if !equalSlices(cfg.Frontmatter.WikilinkProperties, wantWiki) {
		t.Errorf("WikilinkProperties: got %v, want %v", cfg.Frontmatter.WikilinkProperties, wantWiki)
	}
	wantList := []string{"categories", "tags", "topics", "org", "type"}
	if !equalSlices(cfg.Frontmatter.ListProperties, wantList) {
		t.Errorf("ListProperties: got %v, want %v", cfg.Frontmatter.ListProperties, wantList)
	}
	if !equalSlices(cfg.Related.MetadataProperties, DefaultRelatedMetadataProperties) {
		t.Errorf(
			"Related.MetadataProperties: got %v, want %v",
			cfg.Related.MetadataProperties,
			DefaultRelatedMetadataProperties,
		)
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

func TestDefaultTemplate_ParsesAsEmptyConfig(t *testing.T) {
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
	if len(cfg.Frontmatter.WikilinkProperties) != 0 || len(cfg.Frontmatter.ListProperties) != 0 {
		t.Errorf("Frontmatter should be zero, got %+v", cfg.Frontmatter)
	}
	if len(cfg.Fzf.Args) != 0 {
		t.Errorf("Fzf.Args should be empty, got %v", cfg.Fzf.Args)
	}
	if len(cfg.New.Routes) != 0 {
		t.Errorf("New.Routes should be empty, got %v", cfg.New.Routes)
	}
	if cfg.Serve.Addr != "" || cfg.Serve.OpenBrowser != nil {
		t.Errorf("Serve should be zero, got %+v", cfg.Serve)
	}
}

func TestDefaultTemplate_Mentions_AllKnownKeys(t *testing.T) {
	tmpl := DefaultTemplate()
	for _, want := range []string{
		"vault", "ignore", "[templates]", "[frontmatter]", "[fzf]", "[serve]", "[new.routes]", "[related]", "[agent]",
		"wikilink_properties", "list_properties", "metadata_properties", "command", "timeout_seconds",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q", want)
		}
	}
}

func TestLoad_RelatedMetadataPropertiesExplicitEmptyDisablesDefaults(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[related]
metadata_properties = []
`)

	cfg, err := Load("", cfgPath)
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
	resetEnv(t)
	vault := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[related]
metadata_properties = [" tags ", "topics", "tags", ""]
`)

	cfg, err := Load("", cfgPath)
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
