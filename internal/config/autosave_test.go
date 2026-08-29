package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAutosaveMinInterval_AcceptsAndCanonicalizes(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		canonical string
		interval  time.Duration
		disabled  bool
	}{
		{"absent means the default", "", DefaultAutosaveMinInterval, 5 * time.Minute, false},
		{"whitespace means the default", "   ", DefaultAutosaveMinInterval, 5 * time.Minute, false},
		{"explicit value", "10m", "10m", 10 * time.Minute, false},
		{"surrounding whitespace is trimmed", "  10m\t", "10m", 10 * time.Minute, false},
		{"seconds normalize to minutes", "300s", "5m", 5 * time.Minute, false},
		{"zero-valued units are dropped", "5m0s", "5m", 5 * time.Minute, false},
		{"compound durations keep both units", "1h30m", "1h30m", 90 * time.Minute, false},
		{"lower bound is inclusive", "30s", "30s", 30 * time.Second, false},
		{"upper bound is inclusive", "24h", "24h", 24 * time.Hour, false},
		{"minutes normalize to hours", "1440m", "24h", 24 * time.Hour, false},
		{"off disables autosave", AutosaveOff, AutosaveOff, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := ParseAutosaveMinInterval(tc.raw)
			if err != nil {
				t.Fatalf("ParseAutosaveMinInterval(%q): %v", tc.raw, err)
			}
			if policy.Disabled != tc.disabled {
				t.Errorf("Disabled = %v, want %v", policy.Disabled, tc.disabled)
			}
			if policy.Interval != tc.interval {
				t.Errorf("Interval = %v, want %v", policy.Interval, tc.interval)
			}
			if policy.Canonical != tc.canonical {
				t.Errorf("Canonical = %q, want %q", policy.Canonical, tc.canonical)
			}
			if policy.String() != tc.canonical {
				t.Errorf("String() = %q, want %q", policy.String(), tc.canonical)
			}
		})
	}
}

func TestParseAutosaveMinInterval_CanonicalFormIsStable(t *testing.T) {
	// Re-parsing a canonical value must produce the same text, or a value would
	// drift every time it round-trips through the config file.
	for _, raw := range []string{"30s", "300s", "5m0s", "90s", "1h", "1440m", "24h"} {
		first, err := ParseAutosaveMinInterval(raw)
		if err != nil {
			t.Fatalf("ParseAutosaveMinInterval(%q): %v", raw, err)
		}
		again, err := ParseAutosaveMinInterval(first.Canonical)
		if err != nil {
			t.Fatalf("ParseAutosaveMinInterval(%q): %v", first.Canonical, err)
		}
		if again.Canonical != first.Canonical || again.Interval != first.Interval {
			t.Fatalf("%q canonicalized to %q, which re-canonicalized to %q (%v)",
				raw, first.Canonical, again.Canonical, again.Interval)
		}
	}
}

func TestParseAutosaveMinInterval_Rejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"uppercase off", "OFF", "lower case"},
		{"mixed-case off", "Off", "lower case"},
		{"off with other text", "off now", "Go duration"},
		{"uppercase duration unit", "5M", "Go duration"},
		{"unitless number", "5", "Go duration"},
		{"prose", "five minutes", "Go duration"},
		{"space inside the duration", "5 m", "Go duration"},
		{"below the lower bound", "29s", "between 30s and 24h inclusive"},
		{"zero", "0s", "between 30s and 24h inclusive"},
		{"negative", "-5m", "between 30s and 24h inclusive"},
		{"above the upper bound", "24h1s", "between 30s and 24h inclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := ParseAutosaveMinInterval(tc.raw)
			if err == nil {
				t.Fatalf("ParseAutosaveMinInterval(%q) = %+v, want an error", tc.raw, policy)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "autosave.min_interval") {
				t.Fatalf("error %q should name the key it rejects", err)
			}
		})
	}
}

func TestLoad_AutosaveDefaultsWhenAbsent(t *testing.T) {
	cfg, err := loadTOML(t, "[templates]\ndir = \"Templates\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Autosave.MinInterval != DefaultAutosaveMinInterval {
		t.Fatalf("MinInterval = %q, want %q", cfg.Autosave.MinInterval, DefaultAutosaveMinInterval)
	}
	policy := cfg.AutosavePolicy()
	if policy.Disabled || policy.Interval != 5*time.Minute || policy.Canonical != "5m" {
		t.Fatalf("AutosavePolicy() = %+v, want an enabled 5m floor", policy)
	}
}

func TestLoad_AutosaveExplicitValueIsStoredCanonically(t *testing.T) {
	cfg, err := loadTOML(t, "[autosave]\nmin_interval = \"300s\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Autosave.MinInterval != "5m" {
		t.Fatalf("MinInterval = %q, want the canonical %q", cfg.Autosave.MinInterval, "5m")
	}
	if got := cfg.AutosavePolicy().Interval; got != 5*time.Minute {
		t.Fatalf("Interval = %v, want 5m", got)
	}
}

func TestLoad_AutosaveOffDisablesTheFloor(t *testing.T) {
	cfg, err := loadTOML(t, "[autosave]\nmin_interval = \"off\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	policy := cfg.AutosavePolicy()
	if !policy.Disabled {
		t.Fatalf("AutosavePolicy() = %+v, want a disabled policy", policy)
	}
	if policy.Interval != 0 {
		t.Fatalf("a disabled policy should carry no interval, got %v", policy.Interval)
	}
	if policy.Canonical != AutosaveOff {
		t.Fatalf("Canonical = %q, want %q", policy.Canonical, AutosaveOff)
	}
}

func TestLoad_AutosaveBoundsAreInclusive(t *testing.T) {
	for _, value := range []string{"30s", "24h"} {
		cfg, err := loadTOML(t, "[autosave]\nmin_interval = \""+value+"\"\n")
		if err != nil {
			t.Fatalf("Load(%q): %v", value, err)
		}
		if cfg.Autosave.MinInterval != value {
			t.Fatalf("MinInterval = %q, want %q", cfg.Autosave.MinInterval, value)
		}
	}
}

func TestLoad_AutosaveRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"29s", "48h", "Off", "5M", "later"} {
		_, err := loadTOML(t, "[autosave]\nmin_interval = \""+value+"\"\n")
		if err == nil {
			t.Fatalf("Load(%q) should fail", value)
		}
		if !strings.Contains(err.Error(), "autosave.min_interval") {
			t.Fatalf("error %q should name the rejected key", err)
		}
	}
}

func TestLoad_AutosaveRejectsUnknownProperties(t *testing.T) {
	_, err := loadTOML(t, "[autosave]\nmin_interval = \"5m\"\nmax_interval = \"9m\"\n")
	if err == nil {
		t.Fatal("an unknown [autosave] key should fail the load")
	}
	if !strings.Contains(err.Error(), "unknown keys") || !strings.Contains(err.Error(), "autosave.max_interval") {
		t.Fatalf("error %q should report autosave.max_interval as an unknown key", err)
	}
}

func TestConfigAutosavePolicy_FallsBackForUnvalidatedConfigs(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.AutosavePolicy(); got.Canonical != DefaultAutosaveMinInterval {
		t.Fatalf("nil config policy = %+v, want the default", got)
	}
	hand := &Config{Autosave: AutosaveConfig{MinInterval: "nonsense"}}
	policy := hand.AutosavePolicy()
	if policy.Disabled || policy.Canonical != DefaultAutosaveMinInterval || policy.Interval != 5*time.Minute {
		t.Fatalf("policy = %+v, want the default 5m floor", policy)
	}
}

func TestClone_CopiesAutosave(t *testing.T) {
	cfg, err := loadTOML(t, "[autosave]\nmin_interval = \"off\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	clone := cfg.Clone()
	if clone.Autosave.MinInterval != AutosaveOff {
		t.Fatalf("clone MinInterval = %q, want %q", clone.Autosave.MinInterval, AutosaveOff)
	}
	if !clone.AutosavePolicy().Disabled {
		t.Fatal("clone should resolve to the same disabled policy")
	}
	clone.Autosave.MinInterval = "10m"
	if cfg.Autosave.MinInterval != AutosaveOff {
		t.Fatalf("mutating a clone changed its source: %q", cfg.Autosave.MinInterval)
	}
}

func TestDefaultTemplate_AutosaveSectionRoundTrips(t *testing.T) {
	tmpl := DefaultTemplate()
	for _, want := range []string{"[autosave]", "min_interval", `"` + DefaultAutosaveMinInterval + `"`, `"off"`} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q", want)
		}
	}

	cfg, err := Decode("/vault", VaultOriginFlag, []byte(tmpl))
	if err != nil {
		t.Fatalf("template should load: %v", err)
	}
	if cfg.Autosave.MinInterval != DefaultAutosaveMinInterval {
		t.Fatalf("template MinInterval = %q, want the commented-out default %q",
			cfg.Autosave.MinInterval, DefaultAutosaveMinInterval)
	}

	// The template ships the key commented out, so `bn config set` has to create
	// the assignment; that patched body must still load.
	patched, err := PatchAutosaveMinInterval([]byte(tmpl), "45m")
	if err != nil {
		t.Fatalf("patch template: %v", err)
	}
	round, err := Decode("/vault", VaultOriginFlag, patched.Patched)
	if err != nil {
		t.Fatalf("patched template should load: %v\n%s", err, patched.Patched)
	}
	if round.Autosave.MinInterval != "45m" {
		t.Fatalf("patched template MinInterval = %q, want %q", round.Autosave.MinInterval, "45m")
	}
}
