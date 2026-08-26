package config

import (
	"strings"
	"testing"
)

const viewsSample = `[[views]]
name = "Active work"
query = "status:wip and not folder:Archive"
sort = "-modified"
limit = 20
description = "What I am actually working on"

[[views]]
name = "Hub neighbourhood"
query = "near:[[Deep Work]]<=2"
sort = "relevance"
relative_to = "Deep Work"
`

func TestLoad_DecodesSavedViews(t *testing.T) {
	cfg, err := loadTOML(t, viewsSample)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Views) != 2 {
		t.Fatalf("got %d views, want 2", len(cfg.Views))
	}
	want := SavedView{
		Name:        "Active work",
		Query:       "status:wip and not folder:Archive",
		Sort:        "-modified",
		Limit:       20,
		Description: "What I am actually working on",
	}
	if cfg.Views[0] != want {
		t.Fatalf("first view: got %+v, want %+v", cfg.Views[0], want)
	}
	// File order is the presentation order, so it must survive decoding.
	if cfg.Views[1].Name != "Hub neighbourhood" {
		t.Fatalf("second view name: got %q", cfg.Views[1].Name)
	}
	if cfg.Views[1].RelativeTo != "Deep Work" {
		t.Fatalf("relative_to: got %q, want %q", cfg.Views[1].RelativeTo, "Deep Work")
	}
}

func TestLoad_RejectsUnknownViewKey(t *testing.T) {
	_, err := loadTOML(t, `[[views]]
name = "Active"
query = "status:wip"
sortt = "-modified"
`)
	if err == nil {
		t.Fatal("expected error for unknown key inside [[views]]")
	}
	if !strings.Contains(err.Error(), "unknown keys") || !strings.Contains(err.Error(), "sortt") {
		t.Fatalf("error should name the unknown views key, got: %v", err)
	}
}

// TestLoad_AcceptsUnparseableViewQuery pins the contract that makes config's
// inability to validate an expression survivable: a query this package cannot
// check still loads, so one rotted view never locks a user out of the vault.
func TestLoad_AcceptsUnparseableViewQuery(t *testing.T) {
	cfg, err := loadTOML(t, `[[views]]
name = "Rotted"
query = "tag:(((("
`)
	if err != nil {
		t.Fatalf("a query config cannot validate must still load: %v", err)
	}
	if len(cfg.Views) != 1 || cfg.Views[0].Query != "tag:((((" {
		t.Fatalf("view not preserved verbatim: %+v", cfg.Views)
	}
}

func TestValidateViews(t *testing.T) {
	cases := []struct {
		name    string
		views   []SavedView
		wantErr string
	}{
		{
			name:  "valid",
			views: []SavedView{{Name: "Active work", Query: "status:wip"}},
		},
		{
			name:  "unicode name",
			views: []SavedView{{Name: "Idées", Query: "tag:x"}},
		},
		{
			name:    "missing name",
			views:   []SavedView{{Query: "status:wip"}},
			wantErr: "name is required",
		},
		{
			name:    "surrounding whitespace",
			views:   []SavedView{{Name: " Active ", Query: "status:wip"}},
			wantErr: "surrounding whitespace",
		},
		{
			name:    "punctuation in name",
			views:   []SavedView{{Name: "active/work", Query: "status:wip"}},
			wantErr: "must be letters, digits",
		},
		{
			name:    "name ends with a separator",
			views:   []SavedView{{Name: "active-", Query: "status:wip"}},
			wantErr: "must be letters, digits",
		},
		{
			name:    "empty query",
			views:   []SavedView{{Name: "Active", Query: "   "}},
			wantErr: "query must not be empty",
		},
		{
			name:    "control character in query",
			views:   []SavedView{{Name: "Active", Query: "tag:a\x00b"}},
			wantErr: "query contains a control character",
		},
		{
			name:    "negative limit",
			views:   []SavedView{{Name: "Active", Query: "status:wip", Limit: -1}},
			wantErr: "limit -1 must not be negative",
		},
		{
			name: "case-insensitive collision",
			views: []SavedView{
				{Name: "Active", Query: "status:wip"},
				{Name: "active", Query: "status:done"},
			},
			wantErr: "collide case-insensitively",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateViews(&Config{Views: tc.views})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateViews_IgnoresQuerySemantics documents the layering constraint
// directly: a query naming a field that does not exist is not this package's
// business, because checking it would require importing internal/queryexpr.
func TestValidateViews_IgnoresQuerySemantics(t *testing.T) {
	err := validateViews(&Config{Views: []SavedView{
		{Name: "Broken", Query: "nosuchfield>3", Sort: "not-a-sort-key"},
	}})
	if err != nil {
		t.Fatalf("shape validation must not reject query semantics: %v", err)
	}
}

func TestConfigViewLookup(t *testing.T) {
	cfg := &Config{Views: []SavedView{
		{Name: "Active work", Query: "status:wip"},
		{Name: "Reading", Query: "type:book"},
	}}

	if _, ok := cfg.View("active WORK"); !ok {
		t.Fatal("View should match case-insensitively")
	}
	if _, ok := cfg.View("  Reading  "); !ok {
		t.Fatal("View should trim its argument")
	}
	if _, ok := cfg.View("missing"); ok {
		t.Fatal("View should miss on an undeclared name")
	}
	if _, ok := cfg.View(""); ok {
		t.Fatal("View should miss on an empty name")
	}
	if names := cfg.ViewNames(); !equalSlices(names, []string{"Active work", "Reading"}) {
		t.Fatalf("ViewNames: got %v", names)
	}

	var nilCfg *Config
	if _, ok := nilCfg.View("Active work"); ok {
		t.Fatal("View on a nil config should miss rather than panic")
	}
	if names := nilCfg.ViewNames(); names != nil {
		t.Fatalf("ViewNames on a nil config: got %v", names)
	}
}
