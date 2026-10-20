package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoadAgentConfigPreservesArgvAndTimeout(t *testing.T) {
	cfg, err := loadTOML(t, `[agent]
command = ["my-agent", "--prompt", "literal argument with spaces", ""]
timeout_seconds = 17
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"my-agent", "--prompt", "literal argument with spaces", ""}
	if !slices.Equal(cfg.Agent.Command, want) {
		t.Fatalf("Agent.Command = %#v, want %#v", cfg.Agent.Command, want)
	}
	if cfg.Agent.TimeoutSeconds != 17 {
		t.Fatalf("Agent.TimeoutSeconds = %d, want 17", cfg.Agent.TimeoutSeconds)
	}
}

func TestLoadAgentConfigRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "blank executable", body: "[agent]\ncommand = [\"  \", \"arg\"]\n", want: "agent.command[0]"},
		{name: "negative timeout", body: "[agent]\ntimeout_seconds = -1\n", want: "agent.timeout_seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadTOML(t, test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestDefaultTemplateAgentSectionParsesToZero(t *testing.T) {
	template := DefaultTemplate()
	if !strings.Contains(template, "[agent]") {
		t.Fatal("default template missing [agent]")
	}
	var cfg Config
	if _, err := toml.Decode(template, &cfg); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	if len(cfg.Agent.Command) != 0 || cfg.Agent.TimeoutSeconds != 0 {
		t.Fatalf("template [agent] should decode to zero config, got %#v", cfg.Agent)
	}
}
