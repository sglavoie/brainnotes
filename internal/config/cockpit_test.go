package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoad_CockpitDefaults(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	cfg, err := Load(vault, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cockpit.LongTailLimit != DefaultCockpitLongTailLimit {
		t.Errorf("Cockpit.LongTailLimit: got %d, want %d", cfg.Cockpit.LongTailLimit, DefaultCockpitLongTailLimit)
	}
	if cfg.Cockpit.LongTailMaxCount != DefaultCockpitLongTailMaxCount {
		t.Errorf("Cockpit.LongTailMaxCount: got %d, want %d", cfg.Cockpit.LongTailMaxCount, DefaultCockpitLongTailMaxCount)
	}
	if cfg.Cockpit.LongTailHeadFactor != DefaultCockpitLongTailHeadFactor {
		t.Errorf("Cockpit.LongTailHeadFactor: got %d, want %d", cfg.Cockpit.LongTailHeadFactor, DefaultCockpitLongTailHeadFactor)
	}
	if cfg.Cockpit.DisabledRules == nil || len(cfg.Cockpit.DisabledRules) != 0 {
		t.Errorf("Cockpit.DisabledRules: got %#v, want empty non-nil slice", cfg.Cockpit.DisabledRules)
	}
	if cfg.Cockpit.Priority == nil || len(cfg.Cockpit.Priority) != 0 {
		t.Errorf("Cockpit.Priority: got %#v, want empty non-nil slice", cfg.Cockpit.Priority)
	}
}

func TestLoad_CockpitExplicitValues(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `[cockpit]
disabled_rules = ["long_tail_value", "dead_end_note"]
priority = ["broken_target", "schema_violation"]
long_tail_limit = 3
long_tail_max_count = 4
long_tail_head_factor = 6
`)

	cfg, err := Load(vault, cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cockpit.LongTailLimit != 3 {
		t.Errorf("Cockpit.LongTailLimit: got %d, want 3", cfg.Cockpit.LongTailLimit)
	}
	if cfg.Cockpit.LongTailMaxCount != 4 {
		t.Errorf("Cockpit.LongTailMaxCount: got %d, want 4", cfg.Cockpit.LongTailMaxCount)
	}
	if cfg.Cockpit.LongTailHeadFactor != 6 {
		t.Errorf("Cockpit.LongTailHeadFactor: got %d, want 6", cfg.Cockpit.LongTailHeadFactor)
	}
	wantDisabled := []string{"long_tail_value", "dead_end_note"}
	if !equalSlices(cfg.Cockpit.DisabledRules, wantDisabled) {
		t.Errorf("Cockpit.DisabledRules: got %v, want %v", cfg.Cockpit.DisabledRules, wantDisabled)
	}
	wantPriority := []string{"broken_target", "schema_violation"}
	if !equalSlices(cfg.Cockpit.Priority, wantPriority) {
		t.Errorf("Cockpit.Priority: got %v, want %v", cfg.Cockpit.Priority, wantPriority)
	}
}

func TestLoad_CockpitValidationRejectsBadValues(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name:    "negative long_tail_limit",
			body:    "[cockpit]\nlong_tail_limit = -1\n",
			wantSub: "long_tail_limit",
		},
		{
			name:    "negative long_tail_max_count",
			body:    "[cockpit]\nlong_tail_max_count = -1\n",
			wantSub: "long_tail_max_count",
		},
		{
			name:    "negative long_tail_head_factor",
			body:    "[cockpit]\nlong_tail_head_factor = -1\n",
			wantSub: "long_tail_head_factor",
		},
		{
			name:    "empty disabled_rules entry",
			body:    "[cockpit]\ndisabled_rules = [\"orphan_note\", \"  \"]\n",
			wantSub: "disabled_rules[1]",
		},
		{
			name:    "empty priority entry",
			body:    "[cockpit]\npriority = [\"\"]\n",
			wantSub: "priority[0]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)
			vault := t.TempDir()
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, tc.body)

			_, err := Load(vault, cfgPath)
			if err == nil {
				t.Fatalf("Load: got nil error, want error mentioning %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Load error = %v, want mention of %q", err, tc.wantSub)
			}
		})
	}
}

func TestDefaultTemplate_CockpitSectionParsesToZero(t *testing.T) {
	tmpl := DefaultTemplate()
	if !strings.Contains(tmpl, "[cockpit]") {
		t.Fatalf("template missing [cockpit] section")
	}
	for _, want := range []string{"disabled_rules", "priority", "long_tail_limit", "long_tail_max_count", "long_tail_head_factor"} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing cockpit key %q", want)
		}
	}

	var cfg Config
	if _, err := toml.Decode(tmpl, &cfg); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	if cfg.Cockpit.LongTailLimit != 0 || len(cfg.Cockpit.DisabledRules) != 0 || len(cfg.Cockpit.Priority) != 0 {
		t.Errorf("template [cockpit] should parse to zero CockpitConfig, got %+v", cfg.Cockpit)
	}
}
