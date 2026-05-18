package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// resetEnv clears env vars that affect config loading and restores them at test end.
func resetEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envVault, "")
	t.Setenv(envConfig, "")
	t.Setenv(envXDG, "")
	// Force HOME into the temp dir so a stray ~/.config/bnotes/config.toml on the
	// developer's machine cannot leak into the tests.
	t.Setenv("HOME", t.TempDir())
}

func TestLoad_FlagBeatsEnvAndFile(t *testing.T) {
	resetEnv(t)
	vaultFlag := t.TempDir()
	vaultEnv := t.TempDir()
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envVault, vaultEnv)

	cfg, err := Load(vaultFlag, cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFlag {
		t.Fatalf("vault precedence: got %q, want %q", cfg.Vault, vaultFlag)
	}
}

func TestLoad_EnvBeatsFile(t *testing.T) {
	resetEnv(t)
	vaultEnv := t.TempDir()
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envVault, vaultEnv)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultEnv {
		t.Fatalf("env precedence: got %q, want %q", cfg.Vault, vaultEnv)
	}
}

func TestLoad_FileUsedWhenNoFlagOrEnv(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("file precedence: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_MissingConfigOKWhenVaultFlagGiven(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	// No --config flag, no $BNOTES_CONFIG, default search paths empty (HOME and
	// XDG point at empty temp dirs).
	cfg, err := Load(vault, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_ExplicitConfigPathMissingIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	missing := filepath.Join(t.TempDir(), "nope.toml")
	_, err := Load(vault, missing)
	if err == nil {
		t.Fatalf("expected error for missing --config path")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should mention missing path; got %v", err)
	}
}

func TestLoad_InvalidTOMLSurfacesError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "this is = = not toml\n")

	_, err := Load(vault, cfgPath)
	if err == nil {
		t.Fatalf("expected error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error should mention parse failure; got %v", err)
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
	wantWiki := []string{"categories", "org", "parent", "area"}
	if !equalSlices(cfg.Frontmatter.WikilinkProperties, wantWiki) {
		t.Errorf("WikilinkProperties: got %v, want %v", cfg.Frontmatter.WikilinkProperties, wantWiki)
	}
	wantList := []string{"categories", "tags", "topics", "org", "type"}
	if !equalSlices(cfg.Frontmatter.ListProperties, wantList) {
		t.Errorf("ListProperties: got %v, want %v", cfg.Frontmatter.ListProperties, wantList)
	}
	// Defaults merged into Ignore.
	for _, want := range DefaultIgnore {
		if !contains(cfg.Ignore, want) {
			t.Errorf("Ignore missing default %q (got %v)", want, cfg.Ignore)
		}
	}
}

func TestLoad_IgnoreMergesUserAndDefaults(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"Drafts\", \".git\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Errorf("user ignore missing: %v", cfg.Ignore)
	}
	count := 0
	for _, p := range cfg.Ignore {
		if p == ".git" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected .git to appear once after merge, got %d (%v)", count, cfg.Ignore)
	}
}

func TestLoad_BNOTES_CONFIGHonored(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "from-env.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envConfig, cfgPath)
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("vault from $BNOTES_CONFIG: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_BNOTES_CONFIGMissingIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.toml")
	t.Setenv(envConfig, missing)

	if _, err := Load(vault, ""); err == nil {
		t.Fatalf("expected error for missing $BNOTES_CONFIG path")
	}
}

func TestLoad_XDGConfigPath(t *testing.T) {
	resetEnv(t)
	vaultFile := t.TempDir()

	xdg := t.TempDir()
	cfgPath := filepath.Join(xdg, "bnotes", "config.toml")
	writeFile(t, cfgPath, "vault = \""+vaultFile+"\"\n")

	t.Setenv(envXDG, xdg)
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vaultFile {
		t.Fatalf("vault from XDG path: got %q, want %q", cfg.Vault, vaultFile)
	}
}

func TestLoad_VaultMustExist(t *testing.T) {
	resetEnv(t)
	missing := filepath.Join(t.TempDir(), "nonexistent")
	if _, err := Load(missing, ""); err == nil {
		t.Fatalf("expected error for missing vault")
	}
}

func TestLoad_VaultMustBeDirectory(t *testing.T) {
	resetEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	writeFile(t, file, "")

	_, err := Load(file, "")
	if err == nil {
		t.Fatalf("expected error for vault path pointing at file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should mention not a directory; got %v", err)
	}
}

func TestLoad_NoVaultAnywhere(t *testing.T) {
	resetEnv(t)
	if _, err := Load("", ""); err == nil {
		t.Fatalf("expected error when no vault path is set")
	}
}

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
}

func TestDefaultTemplate_Mentions_AllKnownKeys(t *testing.T) {
	tmpl := DefaultTemplate()
	for _, want := range []string{
		"vault", "ignore", "[templates]", "[frontmatter]", "[fzf]", "[new.routes]",
		"wikilink_properties", "list_properties",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q", want)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
