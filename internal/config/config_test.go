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

func TestLoad_TildeExpansionInVaultFlag(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}

	cfg, err := Load("~/vault", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInVaultEnv(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "myvault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	t.Setenv(envVault, "~/myvault")

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault env should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInVaultFromConfigFile(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "vfile")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \"~/vfile\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault from config file should expand ~: got %q, want %q", cfg.Vault, vault)
	}
}

func TestLoad_TildeExpansionInConfigFlag(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := t.TempDir()

	cfgDir := filepath.Join(home, ".myconfig")
	cfgPath := filepath.Join(cfgDir, "bn.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n")

	cfg, err := Load("", "~/.myconfig/bn.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != vault {
		t.Fatalf("vault: got %q, want %q", cfg.Vault, vault)
	}
}

func TestCandidates_TildeExpansionInEnv(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envConfig, "~/somecfg.toml")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := filepath.Join(home, "somecfg.toml")
	if cands[1].Path != want {
		t.Fatalf("env path should expand ~: got %q, want %q", cands[1].Path, want)
	}
}

func TestCandidates_TildeExpansionInXDG(t *testing.T) {
	resetEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envXDG, "~/customxdg")

	cands, err := Candidates("")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := filepath.Join(home, "customxdg", "bnotes", "config.toml")
	if cands[2].Path != want {
		t.Fatalf("XDG path should expand ~: got %q, want %q", cands[2].Path, want)
	}
}

func TestLoad_ConfigPathDirectoryIsError(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()
	dir := t.TempDir() // exists but is a directory, not a TOML file

	_, err := Load(vault, dir)
	if err == nil {
		t.Fatalf("expected error when --config points at a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error should mention directory; got %v", err)
	}
}

func TestLoad_IgnoreNormalizesTrailingSlashAndDotSlash(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// User provides variants that should all collapse to "Archive" (default).
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"Archive/\", \"./Archive\", \"  Archive  \", \"Drafts/\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archiveCount := 0
	for _, p := range cfg.Ignore {
		if p == "Archive" {
			archiveCount++
		}
	}
	if archiveCount != 1 {
		t.Fatalf("expected Archive to appear exactly once after normalization, got %d (%v)", archiveCount, cfg.Ignore)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Fatalf("expected Drafts (trailing slash stripped) in ignore: %v", cfg.Ignore)
	}
	for _, p := range cfg.Ignore {
		if strings.HasSuffix(p, "/") {
			t.Errorf("ignore entry %q should have trailing slash trimmed", p)
		}
		if strings.HasPrefix(p, "./") {
			t.Errorf("ignore entry %q should have leading ./ stripped", p)
		}
		if strings.TrimSpace(p) != p {
			t.Errorf("ignore entry %q should have surrounding whitespace stripped", p)
		}
	}
}

func TestLoad_IgnoreDropsEmptyAndDotEntries(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"\", \"   \", \".\", \"./\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Defaults still present.
	for _, want := range DefaultIgnore {
		if !contains(cfg.Ignore, want) {
			t.Errorf("default %q missing: %v", want, cfg.Ignore)
		}
	}
	// No empty or "." entries.
	for _, p := range cfg.Ignore {
		if p == "" || p == "." {
			t.Errorf("ignore should not contain %q: %v", p, cfg.Ignore)
		}
	}
}

func TestLoad_FrontmatterEmptyListsOptOutOfDefaults(t *testing.T) {
	// Users who write `wikilink_properties = []` should get an empty list,
	// not the defaults — explicit empty differs from omitted.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[frontmatter]\nwikilink_properties = []\nlist_properties = []\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.WikilinkProperties) != 0 {
		t.Errorf("explicit empty wikilink_properties should stay empty, got %v", cfg.Frontmatter.WikilinkProperties)
	}
	if len(cfg.Frontmatter.ListProperties) != 0 {
		t.Errorf("explicit empty list_properties should stay empty, got %v", cfg.Frontmatter.ListProperties)
	}
}

func TestLoad_TemplatesDefaultExtensionWithoutDot(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"markdown\"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".markdown" {
		t.Errorf("default_extension should be normalized with leading dot: got %q", cfg.Templates.DefaultExtension)
	}
}

func TestLoad_TemplatesDefaultExtensionTrimsWhitespace(t *testing.T) {
	// A value of "  md  " in the TOML file used to flow through as
	// ".  md  ", producing bogus filenames such as `Note.  md  `.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"  md  \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("default_extension should be trimmed and dotted: got %q, want %q",
			cfg.Templates.DefaultExtension, ".md")
	}
}

func TestLoad_TemplatesDefaultExtensionWhitespaceOnlyFallsBackToDefault(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndefault_extension = \"   \"\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Templates.DefaultExtension != ".md" {
		t.Errorf("whitespace-only default_extension should fall back to .md, got %q",
			cfg.Templates.DefaultExtension)
	}
}

func TestCandidates_BadXDGIgnoredWhenFlagSet(t *testing.T) {
	// Previously a bad ~-expansion in XDG_CONFIG_HOME (HOME unset)
	// would error out Candidates even when --config was provided. The
	// flag is highest priority, so it should still win cleanly.
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envXDG, "~/broken")

	flag := filepath.Join(t.TempDir(), "flag.toml")
	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates with flag set should not error: %v", err)
	}
	if !cands[0].Active || cands[0].Path != flag {
		t.Errorf("flag candidate should be active with %q: %+v", flag, cands[0])
	}
	if cands[2].Path != "" {
		t.Errorf("broken XDG path should be empty, got %q", cands[2].Path)
	}
}

func TestCandidates_BadEnvIgnoredWhenFlagSet(t *testing.T) {
	// Bad ~-expansion in $BNOTES_CONFIG (HOME unset) should not break
	// resolution when --config is provided.
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envConfig, "~/broken.toml")

	flag := filepath.Join(t.TempDir(), "flag.toml")
	cands, err := Candidates(flag)
	if err != nil {
		t.Fatalf("Candidates with flag set should not error: %v", err)
	}
	if !cands[0].Active || cands[0].Path != flag {
		t.Errorf("flag candidate should be active with %q: %+v", flag, cands[0])
	}
	if cands[1].Path != "" {
		t.Errorf("broken env path should be empty, got %q", cands[1].Path)
	}
}

func TestLoad_TemplatesDirNormalized(t *testing.T) {
	// A user writing `dir = "Templates/"` or stray whitespace must still land
	// on a normalized value so Walk's pruning matches the relative path.
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"Templates/", "  Templates  ", "Templates"} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		writeFile(t, cfgPath, "vault = \""+vault+"\"\n[templates]\ndir = \""+raw+"\"\n")
		cfg, err := Load("", cfgPath)
		if err != nil {
			t.Fatalf("Load (%q): %v", raw, err)
		}
		if cfg.Templates.Dir != "Templates" {
			t.Errorf("raw %q: got %q, want %q", raw, cfg.Templates.Dir, "Templates")
		}
	}
}

func TestCandidates_BadEnvStillErrorsWhenNoHigherSource(t *testing.T) {
	resetEnv(t)
	os.Unsetenv("HOME")
	t.Setenv(envConfig, "~/broken.toml")

	if _, err := Candidates(""); err == nil {
		t.Fatalf("expected error when env expansion fails and no flag is set")
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

func TestLoad_SchemaParsesFromTOML(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
required = true
allowed = ["WIP", "Done", "Archived"]

[frontmatter.schema.tags]
type = "list"
required = true

[frontmatter.schema.created]
type = "date"
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema) != 3 {
		t.Fatalf("expected 3 schema entries, got %d", len(cfg.Frontmatter.Schema))
	}
	s := cfg.Frontmatter.Schema["status"]
	if s.Type != "string" || !s.Required || len(s.Allowed) != 3 {
		t.Errorf("status schema: %+v", s)
	}
	tags := cfg.Frontmatter.Schema["tags"]
	if tags.Type != "list" || !tags.Required {
		t.Errorf("tags schema: %+v", tags)
	}
	created := cfg.Frontmatter.Schema["created"]
	if created.Type != "date" || created.Required {
		t.Errorf("created schema: %+v", created)
	}
}

func TestLoad_SchemaUnknownTypeRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.rating]
type = "number"
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown schema type")
	}
	if !strings.Contains(err.Error(), `unknown type "number"`) {
		t.Fatalf("error should mention unknown type: %v", err)
	}
}

func TestLoad_SchemaEmptyTypeRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
required = true
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for empty schema type")
	}
	if !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("error should mention type is required: %v", err)
	}
}

func TestLoad_SchemaAllowedOnDateRejected(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.created]
type = "date"
allowed = ["2024-01-01"]
`)

	_, err := Load("", cfgPath)
	if err == nil {
		t.Fatal("expected error for allowed on date type")
	}
	if !strings.Contains(err.Error(), "allowed is only valid for string or list") {
		t.Fatalf("error should mention allowed constraint: %v", err)
	}
}

func TestLoad_SchemaAllowedOnStringAccepted(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.status]
type = "string"
allowed = ["WIP", "Done"]
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema["status"].Allowed) != 2 {
		t.Errorf("expected 2 allowed values, got %d", len(cfg.Frontmatter.Schema["status"].Allowed))
	}
}

func TestLoad_SchemaEmptyIsValid(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, `vault = "`+vault+`"
`)

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Frontmatter.Schema) != 0 {
		t.Errorf("expected empty schema after defaults, got %d entries", len(cfg.Frontmatter.Schema))
	}
}

func TestLoad_SchemaAllowedOnAllInvalidTypes(t *testing.T) {
	for _, typ := range []string{"int", "bool", "float", "date"} {
		t.Run(typ, func(t *testing.T) {
			resetEnv(t)
			vault := t.TempDir()
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, `vault = "`+vault+`"
[frontmatter.schema.prop]
type = "`+typ+`"
allowed = ["x"]
`)
			_, err := Load("", cfgPath)
			if err == nil {
				t.Fatalf("expected error for allowed on %s type", typ)
			}
		})
	}
}
