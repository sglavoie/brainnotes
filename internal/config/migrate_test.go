package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// planVault plans a migration for a vault holding the given two bodies. A local
// body of "" means the overlay is absent.
func planVault(t *testing.T, portable, local string) (string, Sources, MigrationPlan) {
	t.Helper()
	root := newVault(t, portable)
	src := Sources{Portable: []byte(portable)}
	if local != "" {
		writeFile(t, filepath.Join(root, LocalFileName), local)
		src.Local = []byte(local)
	}
	plan, err := PlanMigration(root, VaultOriginFlag, src)
	if err != nil {
		t.Fatalf("PlanMigration: %v", err)
	}
	return root, src, plan
}

// applied loads the vault as it would be after the plan is written.
func applied(t *testing.T, root string, plan MigrationPlan) *Config {
	t.Helper()
	cfg, err := DecodeSources(root, VaultOriginFlag, Sources{
		Portable: plan.Portable.Patched,
		Local:    plan.Local.Patched,
	})
	if err != nil {
		t.Fatalf("migrated config does not load: %v", err)
	}
	return cfg
}

const legacyConfig = `# The vault's own settings.
ignore = ["archive"]

[open]
# Which editor to launch. Machine-specific.
editor = "nvim"

[serve]
addr = "127.0.0.1:8099" # loopback only
open_browser = true

[capture]
inbox_note = "Inbox.md"

[autosave]
min_interval = "10m"
`

// TestPlanMigration_LegacyVaultRoutesLocalKeys is the case every existing vault
// is in: one portable file holding both kinds of key.
func TestPlanMigration_LegacyVaultRoutesLocalKeys(t *testing.T) {
	root, _, plan := planVault(t, legacyConfig, "")

	if !plan.Changed() {
		t.Fatal("expected the legacy config to need migrating")
	}
	want := []string{"open.editor", "serve.addr", "serve.open_browser"}
	if got := plan.MovedKeys(); !equalSlices(got, want) {
		t.Fatalf("moved %v, want %v", got, want)
	}
	for _, move := range plan.Moves {
		if move.From != LayerPortable || move.To != LayerLocal {
			t.Fatalf("%s moved %s -> %s", move.Key, move.From, move.To)
		}
	}

	portable, local := string(plan.Portable.Patched), string(plan.Local.Patched)
	for _, gone := range []string{"editor =", "addr =", "open_browser ="} {
		if strings.Contains(portable, gone) {
			t.Errorf("portable file still assigns %q:\n%s", gone, portable)
		}
	}
	for _, kept := range []string{`ignore = ["archive"]`, `inbox_note = "Inbox.md"`, `min_interval = "10m"`} {
		if !strings.Contains(portable, kept) {
			t.Errorf("portable file lost %q:\n%s", kept, portable)
		}
	}
	for _, moved := range []string{`editor = "nvim"`, `addr = "127.0.0.1:8099" # loopback only`, "open_browser = true"} {
		if !strings.Contains(local, moved) {
			t.Errorf("overlay is missing %q:\n%s", moved, local)
		}
	}

	cfg := applied(t, root, plan)
	if cfg.Open.Editor != "nvim" || cfg.Serve.Addr != "127.0.0.1:8099" || !cfg.Serve.BrowserEnabled() {
		t.Fatalf("migration changed the effective config: %+v", cfg.Serve)
	}
	if layer, ok := cfg.SourceOf("open.editor"); !ok || layer != LayerLocal {
		t.Fatalf("open.editor now comes from %q (assigned=%v), want the overlay", layer, ok)
	}
}

// TestPlanMigration_PreservesComments proves the move is a move of bytes: the
// comment documenting a key stays where the user put it, and the comment riding
// on the moved line travels with it.
func TestPlanMigration_PreservesComments(t *testing.T) {
	_, _, plan := planVault(t, legacyConfig, "")

	portable := string(plan.Portable.Patched)
	if !strings.Contains(portable, "# Which editor to launch. Machine-specific.") {
		t.Errorf("a free-standing comment was moved out of the portable file:\n%s", portable)
	}
	if !strings.Contains(portable, "# The vault's own settings.") {
		t.Errorf("the file header was disturbed:\n%s", portable)
	}
	if !strings.Contains(string(plan.Local.Patched), `# loopback only`) {
		t.Errorf("a trailing comment did not travel with its assignment:\n%s", plan.Local.Patched)
	}
}

// TestPlanMigration_Idempotent proves a second run is a no-op, which is what
// makes an interrupted migration safe to re-run.
func TestPlanMigration_Idempotent(t *testing.T) {
	root, _, first := planVault(t, legacyConfig, "")

	second, err := PlanMigration(root, VaultOriginFlag, Sources{
		Portable: first.Portable.Patched,
		Local:    first.Local.Patched,
	})
	if err != nil {
		t.Fatalf("PlanMigration (second run): %v", err)
	}
	if second.Changed() {
		t.Fatalf("second run still wants to write:\nportable=%v\nlocal=%v",
			second.Portable.Diff, second.Local.Diff)
	}
	if len(second.Moves) != 0 {
		t.Fatalf("second run still reports moves: %v", second.MovedKeys())
	}
	if string(second.Portable.Patched) != string(first.Portable.Patched) ||
		string(second.Local.Patched) != string(first.Local.Patched) {
		t.Fatal("a no-op plan did not reproduce its input bytes")
	}
}

// TestPlanMigration_NothingMisplaced is the other no-op: a vault already split
// correctly is left completely alone, overlay presence included.
func TestPlanMigration_NothingMisplaced(t *testing.T) {
	_, src, plan := planVault(t,
		"ignore = [\"archive\"]\n",
		"[open]\neditor = \"nvim\"\n")

	if plan.Changed() {
		t.Fatalf("a correctly split vault was rewritten: %v %v", plan.Portable.Diff, plan.Local.Diff)
	}
	if string(plan.Portable.Patched) != string(src.Portable) ||
		string(plan.Local.Patched) != string(src.Local) {
		t.Fatal("a no-op plan did not return the original bytes")
	}
}

// TestPlanMigration_NoOverlayLeftAbsent proves a vault needing no move never
// gains an overlay. The file's absence is a state of its own, and a migration
// that creates an empty one changes the composite fingerprint for nothing.
func TestPlanMigration_NoOverlayLeftAbsent(t *testing.T) {
	_, _, plan := planVault(t, "ignore = [\"archive\"]\n", "")

	if plan.Local.Changed || plan.Local.Patched != nil {
		t.Fatalf("an overlay was created with nothing to put in it: %q", plan.Local.Patched)
	}
}

// TestPlanMigration_AppendsToExistingTable proves a key lands inside the
// overlay's existing table rather than opening a duplicate one, which would be
// a file TOML refuses.
func TestPlanMigration_AppendsToExistingTable(t *testing.T) {
	root, _, plan := planVault(t,
		"[serve]\naddr = \"127.0.0.1:9000\"\n",
		"[serve]\nopen_browser = true\n")

	local := string(plan.Local.Patched)
	if strings.Count(local, "[serve]") != 1 {
		t.Fatalf("expected one [serve] table, got:\n%s", local)
	}
	cfg := applied(t, root, plan)
	if cfg.Serve.Addr != "127.0.0.1:9000" || !cfg.Serve.BrowserEnabled() {
		t.Fatalf("merged table lost a value: %+v", cfg.Serve)
	}
}

// TestPlanMigration_MovesDottedKeyIntoTable proves a root-level dotted
// assignment is respelled for the table it lands in, value untouched.
func TestPlanMigration_MovesDottedKeyIntoTable(t *testing.T) {
	root, _, plan := planVault(t, "serve.addr = \"127.0.0.1:9100\"  # pinned\n", "")

	local := string(plan.Local.Patched)
	if !strings.Contains(local, "[serve]") || !strings.Contains(local, `addr = "127.0.0.1:9100"  # pinned`) {
		t.Fatalf("dotted key was not respelled into its table:\n%s", local)
	}
	if strings.Contains(local, "serve.addr") {
		t.Fatalf("dotted key was copied verbatim into a table that already names serve:\n%s", local)
	}
	if cfg := applied(t, root, plan); cfg.Serve.Addr != "127.0.0.1:9100" {
		t.Fatalf("serve.addr = %q", cfg.Serve.Addr)
	}
}

// TestPlanMigration_MovesMultiLineValue proves the whole assignment travels: a
// value spanning lines is the key's value, not text below it.
func TestPlanMigration_MovesMultiLineValue(t *testing.T) {
	root, _, plan := planVault(t, `ignore = ["archive"]

[fzf]
args = [
  "--height",
  "40%", # keep it small
]

[capture]
heading = "## Captures"
`, "")

	portable, local := string(plan.Portable.Patched), string(plan.Local.Patched)
	if strings.Contains(portable, "--height") || strings.Contains(portable, "40%") {
		t.Fatalf("part of a multi-line value was left behind:\n%s", portable)
	}
	if !strings.Contains(portable, `heading = "## Captures"`) {
		t.Fatalf("the key below the moved value was swept up:\n%s", portable)
	}
	if !strings.Contains(local, "--height") || !strings.Contains(local, "# keep it small") {
		t.Fatalf("the multi-line value did not arrive whole:\n%s", local)
	}
	if cfg := applied(t, root, plan); !equalSlices(cfg.Fzf.Args, []string{"--height", "40%"}) {
		t.Fatalf("fzf.args = %v", cfg.Fzf.Args)
	}
}

// TestPlanMigration_MovesPortableKeyOutOfOverlay covers the other direction: a
// portable key parked in the overlay never reaches the peer vault.
func TestPlanMigration_MovesPortableKeyOutOfOverlay(t *testing.T) {
	root, _, plan := planVault(t,
		"ignore = [\"archive\"]\n",
		"[capture]\nheading = \"## Captures\"\n\n[open]\neditor = \"nvim\"\n")

	want := []MigrationMove{{Key: "capture.heading", From: LayerLocal, To: LayerPortable}}
	if len(plan.Moves) != 1 || plan.Moves[0] != want[0] {
		t.Fatalf("moves = %+v, want %+v", plan.Moves, want)
	}
	if !strings.Contains(string(plan.Portable.Patched), `heading = "## Captures"`) {
		t.Fatalf("capture.heading did not arrive in the portable file:\n%s", plan.Portable.Patched)
	}
	if !strings.Contains(string(plan.Local.Patched), `editor = "nvim"`) {
		t.Fatalf("the overlay's own key was disturbed:\n%s", plan.Local.Patched)
	}
	if cfg := applied(t, root, plan); cfg.Capture.Heading != "## Captures" || cfg.Open.Editor != "nvim" {
		t.Fatalf("migration changed the effective config: %q %q", cfg.Capture.Heading, cfg.Open.Editor)
	}
}

// TestPlanMigration_RepairsEnrolledVault is the case the loader refuses: an
// enrolled vault whose keys are misplaced does not load at all, and the command
// that repairs it must still be able to run.
func TestPlanMigration_RepairsEnrolledVault(t *testing.T) {
	root := newVault(t, legacyConfig)
	enroll(t, root)

	src := Sources{Portable: []byte(legacyConfig)}
	if _, err := DecodeSources(root, VaultOriginFlag, src); err == nil {
		t.Fatal("expected an enrolled vault with misplaced keys to be refused")
	}

	plan, err := PlanMigration(root, VaultOriginFlag, src)
	if err != nil {
		t.Fatalf("PlanMigration on an enrolled vault: %v", err)
	}
	cfg := applied(t, root, plan)
	if cfg.Open.Editor != "nvim" || cfg.Serve.Addr != "127.0.0.1:8099" {
		t.Fatalf("migration changed the effective config: %q %q", cfg.Open.Editor, cfg.Serve.Addr)
	}
}

// TestPlanMigration_RefusesUnloadableConfig proves migration is not a repair
// tool for arbitrary breakage: a body that does not parse is refused rather
// than rewritten on a guess.
func TestPlanMigration_RefusesUnloadableConfig(t *testing.T) {
	root := newVault(t, "ignore = [\n")

	_, err := PlanMigration(root, VaultOriginFlag, Sources{Portable: []byte("ignore = [\n")})
	if err == nil {
		t.Fatal("expected a parse failure to refuse the migration")
	}
	if !strings.Contains(err.Error(), "does not load") {
		t.Fatalf("error does not explain itself: %v", err)
	}
}

// TestPlanMigration_RefusesUnmovableSpelling proves a key this cannot move as a
// whole assignment is named rather than silently left behind while the plan
// reports success.
func TestPlanMigration_RefusesUnmovableSpelling(t *testing.T) {
	body := "serve = { addr = \"127.0.0.1:9000\" }\n"
	root := newVault(t, body)

	_, err := PlanMigration(root, VaultOriginFlag, Sources{Portable: []byte(body)})
	if err == nil {
		t.Fatal("expected an inline-table assignment to be refused")
	}
	if !strings.Contains(err.Error(), "serve.addr") || !strings.Contains(err.Error(), LocalFileName) {
		t.Fatalf("error names neither the key nor its destination: %v", err)
	}
}

// TestPlanMigration_PreservesCRLF proves a moved line does not become the one
// LF line in a CRLF file.
func TestPlanMigration_PreservesCRLF(t *testing.T) {
	portable := "ignore = [\"archive\"]\r\n\r\n[open]\r\neditor = \"nvim\"\r\n"
	local := "[serve]\r\naddr = \"127.0.0.1:9000\"\r\n"
	_, _, plan := planVault(t, portable, local)

	for _, line := range strings.Split(strings.TrimSuffix(string(plan.Local.Patched), "\n"), "\n") {
		if !strings.HasSuffix(line, "\r") {
			t.Fatalf("line %q lost its CR in:\n%q", line, plan.Local.Patched)
		}
	}
}

// TestPlanMigration_CreatedOverlayExplainsItself proves a file this command
// creates says why it exists.
func TestPlanMigration_CreatedOverlayExplainsItself(t *testing.T) {
	_, _, plan := planVault(t, "[open]\neditor = \"nvim\"\n", "")

	local := string(plan.Local.Patched)
	if !strings.HasPrefix(local, "# Machine-local BrainNotes configuration.") {
		t.Fatalf("a created overlay has no header:\n%s", local)
	}
	if !strings.Contains(local, FileName) {
		t.Fatalf("the header does not say what the other file is for:\n%s", local)
	}
	if !strings.HasSuffix(local, "\n") {
		t.Fatalf("a created overlay does not end in a newline: %q", local)
	}
}

// TestPlanMigration_DiffsBothFiles proves a preview has something to show for
// every file it would write.
func TestPlanMigration_DiffsBothFiles(t *testing.T) {
	_, _, plan := planVault(t, legacyConfig, "")

	if len(plan.Portable.Diff) == 0 {
		t.Error("no diff for the file keys were removed from")
	}
	if len(plan.Local.Diff) == 0 {
		t.Error("no diff for the file keys were added to")
	}
}

// TestSameEffectiveConfig_NoticesEveryOwnedKey guards the check the planner
// relies on: a difference in any key the ownership table names must be caught,
// so the table staying the single source of the split also keeps this honest.
func TestSameEffectiveConfig_NoticesEveryOwnedKey(t *testing.T) {
	for _, spec := range fieldSpecs {
		a, b := &Config{}, &Config{}
		mutateField(spec.key, b)
		if sameEffectiveConfig(a, b) {
			t.Errorf("a change to %s was not noticed", spec.key)
		}
	}
}

// mutateField sets one distinguishable value for the named key. It is the
// hand-written half of the guard above: if a new key is added to fieldSpecs
// without a case here, the test fails rather than passing vacuously.
func mutateField(key string, cfg *Config) {
	switch key {
	case "ignore":
		cfg.Ignore = []string{"x"}
	case "templates.dir":
		cfg.Templates.Dir = "x"
	case "templates.default_extension":
		cfg.Templates.DefaultExtension = "x"
	case "frontmatter.properties":
		cfg.Frontmatter.Properties = map[string]PropertyDefinition{"x": {Type: "string"}}
	case "fzf.args":
		cfg.Fzf.Args = []string{"x"}
	case "open.editor":
		cfg.Open.Editor = "x"
	case "serve.addr":
		cfg.Serve.Addr = "x"
	case "serve.open_browser":
		cfg.Serve.OpenBrowser = new(bool)
	case "new.routes":
		cfg.New.Routes = map[string]string{"x": "y"}
	case "related.metadata_properties":
		cfg.Related.MetadataProperties = []string{"x"}
	case "brief.exclude_folders":
		cfg.Brief.ExcludeFolders = []string{"x"}
	case "brief.exclude_types":
		cfg.Brief.ExcludeTypes = []string{"x"}
	case "capture.inbox_note":
		cfg.Capture.InboxNote = "x"
	case "capture.heading":
		cfg.Capture.Heading = "x"
	case "agent.command":
		cfg.Agent.Command = []string{"x"}
	case "agent.timeout_seconds":
		cfg.Agent.TimeoutSeconds = 1
	case "autosave.min_interval":
		cfg.Autosave.MinInterval = "x"
	case "views":
		cfg.Views = []SavedView{{Name: "x"}}
	default:
		mutateCockpitField(key, cfg)
	}
}

func mutateCockpitField(key string, cfg *Config) {
	switch key {
	case "cockpit.disabled_rules":
		cfg.Cockpit.DisabledRules = []string{"x"}
	case "cockpit.priority":
		cfg.Cockpit.Priority = []string{"x"}
	case "cockpit.long_tail_limit":
		cfg.Cockpit.LongTailLimit = 1
	case "cockpit.long_tail_max_count":
		cfg.Cockpit.LongTailMaxCount = 1
	case "cockpit.long_tail_head_factor":
		cfg.Cockpit.LongTailHeadFactor = 1
	case "cockpit.long_tail_ignore_properties":
		cfg.Cockpit.LongTailIgnoreProperties = []string{"x"}
	case "cockpit.daily_limit":
		cfg.Cockpit.DailyLimit = 1
	case "cockpit.review_date_property":
		cfg.Cockpit.ReviewDateProperty = "x"
	case "cockpit.review_statuses":
		cfg.Cockpit.ReviewStatuses = []string{"x"}
	case "cockpit.wip_statuses":
		cfg.Cockpit.WIPStatuses = []string{"x"}
	case "cockpit.max_wip":
		cfg.Cockpit.MaxWIP = 1
	case "cockpit.stale_status_days":
		cfg.Cockpit.StaleStatusDays = 1
	case "cockpit.max_captures":
		cfg.Cockpit.MaxCaptures = 1
	case "cockpit.hub_type_values":
		cfg.Cockpit.HubTypeValues = []string{"x"}
	case "cockpit.hub_empty_headings":
		cfg.Cockpit.HubEmptyHeadings = []string{"x"}
	case "cockpit.sensitive_scan":
		cfg.Cockpit.SensitiveScan = true
	case "cockpit.sensitive_ignore_paths":
		cfg.Cockpit.SensitiveIgnorePaths = []string{"x"}
	case "cockpit.sensitive_ignore_patterns":
		cfg.Cockpit.SensitiveIgnorePatterns = []string{"x"}
	case "cockpit.agent_command":
		cfg.Cockpit.AgentCommand = "x"
	case "cockpit.agent_timeout_seconds":
		cfg.Cockpit.AgentTimeoutSeconds = 1
	}
}
