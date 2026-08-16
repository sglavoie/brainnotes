package config

import (
	"strings"
	"testing"
)

func TestLoad_BriefDefaults(t *testing.T) {
	cfg, err := loadVault(t, newVault(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Brief.ExcludeFolders == nil || len(cfg.Brief.ExcludeFolders) != 0 {
		t.Fatalf("Brief.ExcludeFolders: got %#v, want empty non-nil slice", cfg.Brief.ExcludeFolders)
	}
	if cfg.Brief.ExcludeTypes == nil || len(cfg.Brief.ExcludeTypes) != 0 {
		t.Fatalf("Brief.ExcludeTypes: got %#v, want empty non-nil slice", cfg.Brief.ExcludeTypes)
	}
}

func TestLoad_BriefExclusionsNormalizeAndDeduplicate(t *testing.T) {
	cfg, err := loadTOML(t, `[brief]
exclude_folders = [" ./Archive/ ", ".\\Drafts\\", "Archive", "Drafts"]
exclude_types = [" Journal ", "journal", "Scratch"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Brief.ExcludeFolders, []string{"Archive", "Drafts"}; !equalSlices(got, want) {
		t.Fatalf("Brief.ExcludeFolders = %v, want %v", got, want)
	}
	if got, want := cfg.Brief.ExcludeTypes, []string{"journal", "scratch"}; !equalSlices(got, want) {
		t.Fatalf("Brief.ExcludeTypes = %v, want %v", got, want)
	}
}

func TestLoad_BriefExcludeFoldersRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name: "empty folder",
			body: `[brief]
exclude_folders = ["   "]
`,
			wantSub: "brief.exclude_folders[0]",
		},
		{
			name: "absolute folder",
			body: `[brief]
exclude_folders = ["/tmp/Archive"]
`,
			wantSub: "absolute path",
		},
		{
			name: "windows absolute folder",
			body: `[brief]
exclude_folders = ['C:\Vault\Archive']
`,
			wantSub: "absolute path",
		},
		{
			name: "parent traversal",
			body: `[brief]
exclude_folders = ["../Archive"]
`,
			wantSub: "escapes the vault",
		},
	}

	for _, tc := range tests {
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

func TestLoad_BriefExcludeTypesRejectMalformedValues(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name: "empty type",
			body: `[brief]
exclude_types = ["  "]
`,
			wantSub: "brief.exclude_types[0]",
		},
		{
			name:    "control character",
			body:    "[brief]\nexclude_types = [\"journal\\tbeta\"]\n",
			wantSub: "control character",
		},
	}

	for _, tc := range tests {
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

func TestLoad_RejectsUnknownBriefNestedKey(t *testing.T) {
	_, err := loadTOML(t, `[brief]
exclude_folders = ["Archive"]
exclude_folder = ["Typo"]
`)
	if err == nil {
		t.Fatal("expected error for unknown brief key, got nil")
	}
	if !strings.Contains(err.Error(), "brief.exclude_folder") {
		t.Fatalf("error should name brief.exclude_folder, got: %v", err)
	}
}
