package config

import (
	"strings"
	"testing"
)

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
	"attachments.dir":             func(c *Config) { c.Attachments.Dir = "x" },
	"attachments.max_file_bytes":  func(c *Config) { c.Attachments.MaxFileBytes = 1 },
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
