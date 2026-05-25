package config

import (
	"path/filepath"
	"testing"
)

func TestCandidates_FlagWins(t *testing.T) {
	resetEnv(t)
	flag := filepath.Join(t.TempDir(), "flag.toml")

	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 4 {
		t.Fatalf("want 4 candidates, got %d", len(cands))
	}
	if !cands[0].Active {
		t.Fatalf("entry 0 should be active: %+v", cands)
	}
	if cands[0].Path != flag {
		t.Errorf("entry 0 path: got %q, want %q", cands[0].Path, flag)
	}
	for i := 1; i < 4; i++ {
		if cands[i].Active {
			t.Errorf("entry %d should not be active: %+v", i, cands[i])
		}
	}
}

func TestCandidates_EnvWins(t *testing.T) {
	resetEnv(t)
	envPath := filepath.Join(t.TempDir(), "env.toml")
	t.Setenv(envConfig, envPath)

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cands[0].Path != "" || cands[0].Active {
		t.Errorf("flag entry should be empty/inactive: %+v", cands[0])
	}
	if !cands[1].Active || cands[1].Path != envPath {
		t.Errorf("env entry should be active with %q: %+v", envPath, cands[1])
	}
}

func TestCandidates_XDGFallback(t *testing.T) {
	resetEnv(t)
	xdg := t.TempDir()
	t.Setenv(envXDG, xdg)

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if !cands[2].Active {
		t.Fatalf("XDG entry should be active: %+v", cands)
	}
	wantPath := filepath.Join(xdg, "bnotes", "config.toml")
	if cands[2].Path != wantPath {
		t.Errorf("XDG path: got %q, want %q", cands[2].Path, wantPath)
	}
}

func TestCandidates_WhitespaceXDGTreatedAsUnset(t *testing.T) {
	// Whitespace-only XDG_CONFIG_HOME used to produce a bogus path like
	// " /bnotes/config.toml"; instead it should be ignored.
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envXDG, "   ")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cands[2].Path != "" {
		t.Errorf("whitespace XDG should be empty, got %q", cands[2].Path)
	}
	if !cands[3].Active {
		t.Errorf("default path should be active when XDG is whitespace: %+v", cands)
	}
}

func TestCandidates_HomeFallback(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if !cands[3].Active {
		t.Fatalf("default entry should be active: %+v", cands)
	}
	wantPath := filepath.Join(home, ".config", "bnotes", "config.toml")
	if cands[3].Path != wantPath {
		t.Errorf("default path: got %q, want %q", cands[3].Path, wantPath)
	}
}

func TestCandidates_ExistsReflectsDisk(t *testing.T) {
	resetEnv(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.toml")
	writeFile(t, existing, "")
	t.Setenv(envConfig, existing)

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if !cands[1].Exists {
		t.Errorf("env candidate should report Exists=true: %+v", cands[1])
	}

	resetEnv(t)
	missing := filepath.Join(t.TempDir(), "missing.toml")
	t.Setenv(envConfig, missing)
	cands, err = Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cands[1].Exists {
		t.Errorf("missing path should report Exists=false: %+v", cands[1])
	}
}

func TestCandidates_NonActiveEntriesStillEnumerated(t *testing.T) {
	resetEnv(t)
	flag := filepath.Join(t.TempDir(), "flag.toml")
	t.Setenv(envConfig, filepath.Join(t.TempDir(), "env.toml"))
	t.Setenv(envXDG, t.TempDir())

	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 4 {
		t.Fatalf("want 4 entries, got %d", len(cands))
	}
	wantSources := []ConfigSource{SourceFlag, SourceEnv, SourceXDG, SourceDefault}
	for i, c := range cands {
		if c.Source != wantSources[i] {
			t.Errorf("entry %d source: got %v, want %v", i, c.Source, wantSources[i])
		}
	}
	activeCount := 0
	for _, c := range cands {
		if c.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("exactly one entry should be active, got %d", activeCount)
	}
}

func TestResolvePath_MatchesActiveCandidate(t *testing.T) {
	resetEnv(t)
	envPath := filepath.Join(t.TempDir(), "env.toml")
	t.Setenv(envConfig, envPath)

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	got, ok, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !ok {
		t.Fatalf("ResolvePath should report ok=true")
	}
	var wantPath string
	for _, c := range cands {
		if c.Active {
			wantPath = c.Path
		}
	}
	if got != wantPath {
		t.Errorf("ResolvePath: got %q, want %q", got, wantPath)
	}
}
