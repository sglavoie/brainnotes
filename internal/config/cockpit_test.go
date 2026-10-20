package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoad_CockpitDefaults(t *testing.T) {
	cfg, err := loadVault(t, newVault(t, ""))
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
	if cfg.Cockpit.LongTailIgnoreProperties == nil || len(cfg.Cockpit.LongTailIgnoreProperties) != 0 {
		t.Errorf("Cockpit.LongTailIgnoreProperties: got %#v, want empty non-nil slice", cfg.Cockpit.LongTailIgnoreProperties)
	}
	if cfg.Cockpit.DailyLimit != DefaultCockpitDailyLimit {
		t.Errorf("Cockpit.DailyLimit: got %d, want %d", cfg.Cockpit.DailyLimit, DefaultCockpitDailyLimit)
	}
	if cfg.Cockpit.ReviewDateProperty != DefaultCockpitReviewDateProperty {
		t.Errorf("Cockpit.ReviewDateProperty: got %q, want %q", cfg.Cockpit.ReviewDateProperty, DefaultCockpitReviewDateProperty)
	}
	if cfg.Cockpit.MaxWIP != DefaultCockpitMaxWIP {
		t.Errorf("Cockpit.MaxWIP: got %d, want %d", cfg.Cockpit.MaxWIP, DefaultCockpitMaxWIP)
	}
	if cfg.Cockpit.StaleStatusDays != DefaultCockpitStaleStatusDays {
		t.Errorf("Cockpit.StaleStatusDays: got %d, want %d", cfg.Cockpit.StaleStatusDays, DefaultCockpitStaleStatusDays)
	}
	if cfg.Cockpit.AgentTimeoutSeconds != DefaultCockpitAgentTimeoutSeconds {
		t.Errorf("Cockpit.AgentTimeoutSeconds: got %d, want %d", cfg.Cockpit.AgentTimeoutSeconds, DefaultCockpitAgentTimeoutSeconds)
	}
	if cfg.Cockpit.MaxCaptures != 0 {
		t.Errorf("Cockpit.MaxCaptures: got %d, want 0 (capture-backlog rule off by default)", cfg.Cockpit.MaxCaptures)
	}
	if cfg.Cockpit.SensitiveScan {
		t.Errorf("Cockpit.SensitiveScan: got true, want false by default")
	}
	if cfg.Cockpit.AgentCommand != "" {
		t.Errorf("Cockpit.AgentCommand: got %q, want empty by default", cfg.Cockpit.AgentCommand)
	}
	for name, got := range map[string][]string{
		"ReviewStatuses":          cfg.Cockpit.ReviewStatuses,
		"WIPStatuses":             cfg.Cockpit.WIPStatuses,
		"HubTypeValues":           cfg.Cockpit.HubTypeValues,
		"HubEmptyHeadings":        cfg.Cockpit.HubEmptyHeadings,
		"SensitiveIgnorePaths":    cfg.Cockpit.SensitiveIgnorePaths,
		"SensitiveIgnorePatterns": cfg.Cockpit.SensitiveIgnorePatterns,
	} {
		if got == nil || len(got) != 0 {
			t.Errorf("Cockpit.%s: got %#v, want empty non-nil slice", name, got)
		}
	}
}

func TestLoad_CockpitExplicitValues(t *testing.T) {
	cfg, err := loadTOML(t, `[cockpit]
disabled_rules = ["long_tail_value", "dead_end_note"]
priority = ["broken_target", "schema_violation"]
long_tail_limit = 3
long_tail_max_count = 4
long_tail_head_factor = 6
long_tail_ignore_properties = ["created", "tags"]
daily_limit = 7
review_date_property = "last_reviewed"
review_statuses = ["Evergreen", "Reference"]
wip_statuses = ["WIP"]
max_wip = 2
stale_status_days = 45
hub_type_values = ["moc", "hub"]
hub_empty_headings = ["## Synthesis"]
sensitive_scan = true
sensitive_ignore_paths = ["Templates"]
sensitive_ignore_patterns = ["EXAMPLE_[A-Z]+"]
agent_command = "my-agent --daily"
agent_timeout_seconds = 120
`)
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
	if !slices.Equal(cfg.Cockpit.DisabledRules, wantDisabled) {
		t.Errorf("Cockpit.DisabledRules: got %v, want %v", cfg.Cockpit.DisabledRules, wantDisabled)
	}
	wantPriority := []string{"broken_target", "schema_violation"}
	if !slices.Equal(cfg.Cockpit.Priority, wantPriority) {
		t.Errorf("Cockpit.Priority: got %v, want %v", cfg.Cockpit.Priority, wantPriority)
	}
	wantIgnore := []string{"created", "tags"}
	if !slices.Equal(cfg.Cockpit.LongTailIgnoreProperties, wantIgnore) {
		t.Errorf("Cockpit.LongTailIgnoreProperties: got %v, want %v", cfg.Cockpit.LongTailIgnoreProperties, wantIgnore)
	}
	if cfg.Cockpit.DailyLimit != 7 {
		t.Errorf("Cockpit.DailyLimit: got %d, want 7", cfg.Cockpit.DailyLimit)
	}
	if cfg.Cockpit.ReviewDateProperty != "last_reviewed" {
		t.Errorf("Cockpit.ReviewDateProperty: got %q, want %q", cfg.Cockpit.ReviewDateProperty, "last_reviewed")
	}
	if !slices.Equal(cfg.Cockpit.ReviewStatuses, []string{"Evergreen", "Reference"}) {
		t.Errorf("Cockpit.ReviewStatuses: got %v", cfg.Cockpit.ReviewStatuses)
	}
	if !slices.Equal(cfg.Cockpit.WIPStatuses, []string{"WIP"}) {
		t.Errorf("Cockpit.WIPStatuses: got %v", cfg.Cockpit.WIPStatuses)
	}
	if cfg.Cockpit.MaxWIP != 2 {
		t.Errorf("Cockpit.MaxWIP: got %d, want 2", cfg.Cockpit.MaxWIP)
	}
	if cfg.Cockpit.StaleStatusDays != 45 {
		t.Errorf("Cockpit.StaleStatusDays: got %d, want 45", cfg.Cockpit.StaleStatusDays)
	}
	if !slices.Equal(cfg.Cockpit.HubTypeValues, []string{"moc", "hub"}) {
		t.Errorf("Cockpit.HubTypeValues: got %v", cfg.Cockpit.HubTypeValues)
	}
	if !slices.Equal(cfg.Cockpit.HubEmptyHeadings, []string{"## Synthesis"}) {
		t.Errorf("Cockpit.HubEmptyHeadings: got %v", cfg.Cockpit.HubEmptyHeadings)
	}
	if !cfg.Cockpit.SensitiveScan {
		t.Errorf("Cockpit.SensitiveScan: got false, want true")
	}
	if !slices.Equal(cfg.Cockpit.SensitiveIgnorePaths, []string{"Templates"}) {
		t.Errorf("Cockpit.SensitiveIgnorePaths: got %v", cfg.Cockpit.SensitiveIgnorePaths)
	}
	if !slices.Equal(cfg.Cockpit.SensitiveIgnorePatterns, []string{"EXAMPLE_[A-Z]+"}) {
		t.Errorf("Cockpit.SensitiveIgnorePatterns: got %v", cfg.Cockpit.SensitiveIgnorePatterns)
	}
	if cfg.Cockpit.AgentCommand != "my-agent --daily" {
		t.Errorf("Cockpit.AgentCommand: got %q", cfg.Cockpit.AgentCommand)
	}
	if cfg.Cockpit.AgentTimeoutSeconds != 120 {
		t.Errorf("Cockpit.AgentTimeoutSeconds: got %d, want 120", cfg.Cockpit.AgentTimeoutSeconds)
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
		{
			name:    "whitespace long_tail_ignore_properties entry",
			body:    "[cockpit]\nlong_tail_ignore_properties = [\"created\", \"  \"]\n",
			wantSub: "long_tail_ignore_properties[1]",
		},
		{
			name:    "negative daily_limit",
			body:    "[cockpit]\ndaily_limit = -1\n",
			wantSub: "daily_limit",
		},
		{
			name:    "negative max_wip",
			body:    "[cockpit]\nmax_wip = -1\n",
			wantSub: "max_wip",
		},
		{
			name:    "negative max_captures",
			body:    "[cockpit]\nmax_captures = -1\n",
			wantSub: "max_captures",
		},
		{
			name:    "negative stale_status_days",
			body:    "[cockpit]\nstale_status_days = -1\n",
			wantSub: "stale_status_days",
		},
		{
			name:    "negative agent_timeout_seconds",
			body:    "[cockpit]\nagent_timeout_seconds = -1\n",
			wantSub: "agent_timeout_seconds",
		},
		{
			name:    "empty review_statuses entry",
			body:    "[cockpit]\nreview_statuses = [\"Evergreen\", \"  \"]\n",
			wantSub: "review_statuses[1]",
		},
		{
			name:    "empty wip_statuses entry",
			body:    "[cockpit]\nwip_statuses = [\"\"]\n",
			wantSub: "wip_statuses[0]",
		},
		{
			name:    "empty hub_type_values entry",
			body:    "[cockpit]\nhub_type_values = [\"  \"]\n",
			wantSub: "hub_type_values[0]",
		},
		{
			name:    "empty hub_empty_headings entry",
			body:    "[cockpit]\nhub_empty_headings = [\"## Synthesis\", \"\"]\n",
			wantSub: "hub_empty_headings[1]",
		},
		{
			name:    "empty sensitive_ignore_paths entry",
			body:    "[cockpit]\nsensitive_ignore_paths = [\"  \"]\n",
			wantSub: "sensitive_ignore_paths[0]",
		},
		{
			name:    "empty sensitive_ignore_patterns entry",
			body:    "[cockpit]\nsensitive_ignore_patterns = [\"\"]\n",
			wantSub: "sensitive_ignore_patterns[0]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadTOML(t, tc.body)
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
	for _, want := range []string{
		"disabled_rules", "priority", "long_tail_limit", "long_tail_max_count", "long_tail_head_factor",
		"daily_limit", "review_date_property", "review_statuses", "wip_statuses", "max_wip", "max_captures",
		"stale_status_days", "hub_type_values", "hub_empty_headings", "sensitive_scan",
		"sensitive_ignore_paths", "sensitive_ignore_patterns", "agent_command", "agent_timeout_seconds",
	} {
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
	if cfg.Cockpit.DailyLimit != 0 || cfg.Cockpit.MaxWIP != 0 || cfg.Cockpit.MaxCaptures != 0 ||
		cfg.Cockpit.StaleStatusDays != 0 ||
		cfg.Cockpit.AgentTimeoutSeconds != 0 || cfg.Cockpit.SensitiveScan ||
		cfg.Cockpit.ReviewDateProperty != "" || cfg.Cockpit.AgentCommand != "" ||
		len(cfg.Cockpit.ReviewStatuses) != 0 || len(cfg.Cockpit.WIPStatuses) != 0 ||
		len(cfg.Cockpit.HubTypeValues) != 0 || len(cfg.Cockpit.HubEmptyHeadings) != 0 ||
		len(cfg.Cockpit.SensitiveIgnorePaths) != 0 || len(cfg.Cockpit.SensitiveIgnorePatterns) != 0 {
		t.Errorf("template [cockpit] daily keys should parse to zero CockpitConfig, got %+v", cfg.Cockpit)
	}
}
