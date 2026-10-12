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
