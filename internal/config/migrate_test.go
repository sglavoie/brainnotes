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
	_, _, plan := planVault(t, legacyConfig, "")

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
}

// TestPlanMigration_LegacyVaultRewritesBothFiles checks the bytes: every moved
// assignment left one file and arrived in the other, and nothing else went with
// it.
func TestPlanMigration_LegacyVaultRewritesBothFiles(t *testing.T) {
	_, _, plan := planVault(t, legacyConfig, "")
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
}

// TestPlanMigration_LegacyVaultKeepsEffectiveValues is the property the whole
// operation is judged on: the vault does exactly what it did before, and only
// the file answering for each key has changed.
func TestPlanMigration_LegacyVaultKeepsEffectiveValues(t *testing.T) {
	root, _, plan := planVault(t, legacyConfig, "")
	cfg := applied(t, root, plan)

	if cfg.Open.Editor != "nvim" || cfg.Serve.Addr != "127.0.0.1:8099" || !cfg.Serve.BrowserEnabled() {
		t.Fatalf("migration changed the effective config: %+v", cfg.Serve)
	}
	if layer, ok := cfg.SourceOf("open.editor"); !ok || layer != LayerLocal {
		t.Fatalf("open.editor now comes from %q (assigned=%v), want the overlay", layer, ok)
	}
	if layer, ok := cfg.SourceOf("autosave.min_interval"); !ok || layer != LayerPortable {
		t.Fatalf("autosave.min_interval now comes from %q (assigned=%v), want the portable file", layer, ok)
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

// TestPlanMigration_ShadowedLocalKeyIsDropped covers a key both files assign
// whose owner is the overlay. The overlay already holds the value in force, so
// the portable copy is dead text and is removed rather than copied in beside the
// live one — which would bind the key twice and stop the file loading.
func TestPlanMigration_ShadowedLocalKeyIsDropped(t *testing.T) {
	root, _, plan := planVault(t,
		"[open]\neditor = \"nvim\"\n",
		"[open]\neditor = \"hx\"\n")

	want := MigrationMove{Key: "open.editor", From: LayerPortable, To: LayerLocal, Shadowed: true}
	if len(plan.Moves) != 1 || plan.Moves[0] != want {
		t.Fatalf("moves = %+v, want %+v", plan.Moves, want)
	}
	if got := string(plan.Portable.Patched); got != "[open]\n" {
		t.Fatalf("portable file = %q, want the dead assignment removed and the table kept", got)
	}
	if got := string(plan.Local.Patched); got != "[open]\neditor = \"hx\"\n" {
		t.Fatalf("overlay = %q, want it untouched", got)
	}
	if cfg := applied(t, root, plan); cfg.Open.Editor != "hx" {
		t.Fatalf("open.editor = %q, want the value that was in force", cfg.Open.Editor)
	}
}

// TestPlanMigration_ShadowedPortableKeyReplacesTheDeadOne is the same situation
// with the ownership the other way round. Here the assignment that has to travel
// is the live one, so it replaces the dead assignment already sitting in the
// destination instead of joining it.
func TestPlanMigration_ShadowedPortableKeyReplacesTheDeadOne(t *testing.T) {
	root, _, plan := planVault(t,
		"[capture]\ninbox_note = \"Inbox.md\" # the vault's own\n",
		"[capture]\ninbox_note = \"Later.md\"\n")

	want := MigrationMove{Key: "capture.inbox_note", From: LayerLocal, To: LayerPortable, Shadowed: true}
	if len(plan.Moves) != 1 || plan.Moves[0] != want {
		t.Fatalf("moves = %+v, want %+v", plan.Moves, want)
	}
	// The line that arrives is the overlay's, byte for byte; the portable file's
	// own line — comment included — is the one that was never in force.
	if got := string(plan.Portable.Patched); got != "[capture]\ninbox_note = \"Later.md\"\n" {
		t.Fatalf("portable file = %q, want the live assignment to have replaced the dead one", got)
	}
	if got := string(plan.Local.Patched); got != "[capture]\n" {
		t.Fatalf("overlay = %q, want the moved assignment gone", got)
	}
	if cfg := applied(t, root, plan); cfg.Capture.InboxNote != "Later.md" {
		t.Fatalf("capture.inbox_note = %q, want the value that was in force", cfg.Capture.InboxNote)
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
		mutate, ok := fieldMutators[spec.key]
		if !ok {
			t.Errorf("%s has no mutator, so nothing here proves a change to it is noticed", spec.key)
			continue
		}
		before, after := &Config{}, &Config{}
		mutate(after)
		if sameEffectiveConfig(before, after) {
			t.Errorf("a change to %s was not noticed", spec.key)
		}
	}
}

// fieldMutators sets one distinguishable value per configuration key.
//
// It is the hand-written half of the guard above, and a table rather than a
// switch for the same reason fieldSpecs is: the test can then check that every
// owned key has an entry, so a key added to the ownership table without one
// fails loudly instead of passing vacuously.
var fieldMutators = map[string]func(*Config){
	"ignore":                      func(c *Config) { c.Ignore = []string{"x"} },
	"templates.dir":               func(c *Config) { c.Templates.Dir = "x" },
	"templates.default_extension": func(c *Config) { c.Templates.DefaultExtension = "x" },
	"frontmatter.properties": func(c *Config) {
		c.Frontmatter.Properties = map[string]PropertyDefinition{"x": {Type: "string"}}
	},
	"fzf.args":                    func(c *Config) { c.Fzf.Args = []string{"x"} },
	"open.editor":                 func(c *Config) { c.Open.Editor = "x" },
	"serve.addr":                  func(c *Config) { c.Serve.Addr = "x" },
	"serve.open_browser":          func(c *Config) { c.Serve.OpenBrowser = new(bool) },
	"new.routes":                  func(c *Config) { c.New.Routes = map[string]string{"x": "y"} },
	"related.metadata_properties": func(c *Config) { c.Related.MetadataProperties = []string{"x"} },
	"brief.exclude_folders":       func(c *Config) { c.Brief.ExcludeFolders = []string{"x"} },
	"brief.exclude_types":         func(c *Config) { c.Brief.ExcludeTypes = []string{"x"} },
	"capture.inbox_note":          func(c *Config) { c.Capture.InboxNote = "x" },
	"capture.heading":             func(c *Config) { c.Capture.Heading = "x" },
	"agent.command":               func(c *Config) { c.Agent.Command = []string{"x"} },
	"agent.timeout_seconds":       func(c *Config) { c.Agent.TimeoutSeconds = 1 },
	"autosave.min_interval":       func(c *Config) { c.Autosave.MinInterval = "x" },
	"views":                       func(c *Config) { c.Views = []SavedView{{Name: "x"}} },

	"cockpit.disabled_rules":              func(c *Config) { c.Cockpit.DisabledRules = []string{"x"} },
	"cockpit.priority":                    func(c *Config) { c.Cockpit.Priority = []string{"x"} },
	"cockpit.long_tail_limit":             func(c *Config) { c.Cockpit.LongTailLimit = 1 },
	"cockpit.long_tail_max_count":         func(c *Config) { c.Cockpit.LongTailMaxCount = 1 },
	"cockpit.long_tail_head_factor":       func(c *Config) { c.Cockpit.LongTailHeadFactor = 1 },
	"cockpit.long_tail_ignore_properties": func(c *Config) { c.Cockpit.LongTailIgnoreProperties = []string{"x"} },
	"cockpit.daily_limit":                 func(c *Config) { c.Cockpit.DailyLimit = 1 },
	"cockpit.review_date_property":        func(c *Config) { c.Cockpit.ReviewDateProperty = "x" },
	"cockpit.review_statuses":             func(c *Config) { c.Cockpit.ReviewStatuses = []string{"x"} },
	"cockpit.wip_statuses":                func(c *Config) { c.Cockpit.WIPStatuses = []string{"x"} },
	"cockpit.max_wip":                     func(c *Config) { c.Cockpit.MaxWIP = 1 },
	"cockpit.stale_status_days":           func(c *Config) { c.Cockpit.StaleStatusDays = 1 },
	"cockpit.max_captures":                func(c *Config) { c.Cockpit.MaxCaptures = 1 },
	"cockpit.hub_type_values":             func(c *Config) { c.Cockpit.HubTypeValues = []string{"x"} },
	"cockpit.hub_empty_headings":          func(c *Config) { c.Cockpit.HubEmptyHeadings = []string{"x"} },
	"cockpit.sensitive_scan":              func(c *Config) { c.Cockpit.SensitiveScan = true },
	"cockpit.sensitive_ignore_paths":      func(c *Config) { c.Cockpit.SensitiveIgnorePaths = []string{"x"} },
	"cockpit.sensitive_ignore_patterns":   func(c *Config) { c.Cockpit.SensitiveIgnorePatterns = []string{"x"} },
	"cockpit.agent_command":               func(c *Config) { c.Cockpit.AgentCommand = "x" },
	"cockpit.agent_timeout_seconds":       func(c *Config) { c.Cockpit.AgentTimeoutSeconds = 1 },
}
