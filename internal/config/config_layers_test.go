package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLayeredVault creates a vault root holding both configuration layers.
func newLayeredVault(t *testing.T, portable, local string) string {
	t.Helper()
	root := newVault(t, portable)
	writeFile(t, filepath.Join(root, LocalFileName), local)
	return root
}

// sha256Hex is the expected digest spelled independently of the package's own
// helper, so a test that asserts a fingerprint is not asserting against the
// code that produced it.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

const legacyPortable = `
[serve]
addr = "127.0.0.1:9999"

[open]
editor = "vim"

[capture]
inbox_note = "Inbox.md"
`

func TestLoad_LegacySingleFileConfigurationIsUnchanged(t *testing.T) {
	// The legacy shape is a portable file carrying machine settings and no
	// overlay at all. It must keep loading exactly as it always has.
	cfg, err := loadVault(t, newVault(t, legacyPortable))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Serve.Addr != "127.0.0.1:9999" {
		t.Errorf("serve.addr = %q, want the value the portable file assigns", cfg.Serve.Addr)
	}
	if cfg.Open.Editor != "vim" {
		t.Errorf("open.editor = %q, want the value the portable file assigns", cfg.Open.Editor)
	}
	if got, ok := cfg.SourceOf("serve.addr"); !ok || got != LayerPortable {
		t.Errorf("SourceOf(serve.addr) = %q,%v; want %q,true", got, ok, LayerPortable)
	}
	if _, ok := cfg.SourceOf("templates.dir"); ok {
		t.Error("templates.dir has provenance although no file assigns it")
	}
}

// TestLoad_LegacyVaultDescribesItselfAsHavingNoOverlay is the other half: the
// runtime fields a caller reads to decide what the vault carries must all say
// "no overlay" rather than "an overlay I could not find".
func TestLoad_LegacyVaultDescribesItselfAsHavingNoOverlay(t *testing.T) {
	cfg, err := loadVault(t, newVault(t, legacyPortable))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LocalSourcePath != "" {
		t.Errorf("LocalSourcePath = %q, want empty when no overlay exists", cfg.LocalSourcePath)
	}
	if cfg.Fingerprint.LocalPresent {
		t.Error("Fingerprint.LocalPresent is true with no overlay on disk")
	}
	if cfg.Fingerprint.Local != "" {
		t.Errorf("Fingerprint.Local = %q, want empty when no overlay exists", cfg.Fingerprint.Local)
	}
	if cfg.Enrollment != EnrollmentNone {
		t.Errorf("Enrollment = %q, want %q", cfg.Enrollment, EnrollmentNone)
	}
}

func TestLoad_AbsentOverlayIsDistinctFromEmptyOverlay(t *testing.T) {
	absent, err := loadVault(t, newVault(t, legacyPortable))
	if err != nil {
		t.Fatalf("Load without overlay: %v", err)
	}
	empty, err := loadVault(t, newLayeredVault(t, legacyPortable, ""))
	if err != nil {
		t.Fatalf("Load with empty overlay: %v", err)
	}

	if absent.Fingerprint.Portable != empty.Fingerprint.Portable {
		t.Fatal("the two vaults were meant to carry identical portable bytes")
	}
	if absent.Fingerprint.Composite == empty.Fingerprint.Composite {
		t.Error("an absent overlay and an empty one produced the same composite fingerprint")
	}
	if empty.Fingerprint.Local != sha256Hex("") {
		t.Errorf("empty overlay hash = %q, want the SHA-256 of no bytes", empty.Fingerprint.Local)
	}
	if !empty.Fingerprint.LocalPresent {
		t.Error("an empty overlay on disk must report LocalPresent")
	}
	if empty.LocalSourcePath == "" {
		t.Error("an empty overlay on disk must still name its path")
	}
	// Presence is the only difference: an empty overlay assigns nothing, so
	// every value still comes from the portable file.
	if empty.Serve.Addr != absent.Serve.Addr || empty.Open.Editor != absent.Open.Editor {
		t.Error("an empty overlay changed a value the portable file assigned")
	}
}

func TestLoad_OverlayOverridesOnlyTheKeysItAssigns(t *testing.T) {
	cfg, err := loadVault(t, newLayeredVault(t, legacyPortable, `
[serve]
addr = "127.0.0.1:7000"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Serve.Addr != "127.0.0.1:7000" {
		t.Errorf("serve.addr = %q, want the overlay's value", cfg.Serve.Addr)
	}
	if cfg.Open.Editor != "vim" {
		t.Errorf("open.editor = %q, want the portable value the overlay never touched", cfg.Open.Editor)
	}
	if cfg.Capture.InboxNote != "Inbox.md" {
		t.Errorf("capture.inbox_note = %q, want the portable value", cfg.Capture.InboxNote)
	}
	if got, _ := cfg.SourceOf("serve.addr"); got != LayerLocal {
		t.Errorf("SourceOf(serve.addr) = %q, want %q", got, LayerLocal)
	}
	if got, _ := cfg.SourceOf("open.editor"); got != LayerPortable {
		t.Errorf("SourceOf(open.editor) = %q, want %q", got, LayerPortable)
	}
	if cfg.LocalSourcePath != filepath.Join(cfg.Vault, LocalFileName) {
		t.Errorf("LocalSourcePath = %q, want the overlay beside the portable file", cfg.LocalSourcePath)
	}
}

func TestLoad_OverlayAssignmentsGoThroughDefaultsAndValidation(t *testing.T) {
	// An overlay value is not a special case: it is normalized by applyDefaults
	// and judged by validate exactly like a portable one. The portable file
	// assigns a non-default address, so falling back to the default can only
	// happen if the overlay's whitespace really did replace it.
	cfg, err := loadVault(t, newLayeredVault(t, legacyPortable, `
[serve]
addr = "   "
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Serve.Addr != DefaultServeAddr {
		t.Errorf("serve.addr = %q, want the default a whitespace assignment falls back to", cfg.Serve.Addr)
	}
	if got, _ := cfg.SourceOf("serve.addr"); got != LayerLocal {
		t.Errorf("SourceOf(serve.addr) = %q; an assignment that defaults still came from the overlay", got)
	}

	_, err = loadVault(t, newLayeredVault(t, "", `
[agent]
command = [""]
`))
	if err == nil {
		t.Fatal("expected the overlay's invalid agent command to be rejected")
	}
	if !strings.Contains(err.Error(), "agent.command[0]") {
		t.Fatalf("error should name the offending key; got %v", err)
	}
}

// TestLoad_OverlayAssignsItsZeroValues covers the assignment that looks like an
// absence. "Do not open a browser" and "score no metadata properties" are
// spelled `false` and `[]`, and an overlay that silently dropped them would be
// indistinguishable from one that never mentioned them.
func TestLoad_OverlayAssignsItsZeroValues(t *testing.T) {
	cfg, err := loadVault(t, newLayeredVault(t, `
[serve]
open_browser = true

[related]
metadata_properties = ["tags", "topics"]

[fzf]
args = ["--height", "40%"]
`, `
[serve]
open_browser = false

[related]
metadata_properties = []

[fzf]
args = []
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Serve.BrowserEnabled() {
		t.Error("serve.open_browser: the overlay's false was lost")
	}
	if len(cfg.Related.MetadataProperties) != 0 {
		t.Errorf("related.metadata_properties = %v, want the overlay's empty list", cfg.Related.MetadataProperties)
	}
	if len(cfg.Fzf.Args) != 0 {
		t.Errorf("fzf.args = %v, want the overlay's empty list", cfg.Fzf.Args)
	}
	for _, key := range []string{"serve.open_browser", "related.metadata_properties", "fzf.args"} {
		if got, ok := cfg.SourceOf(key); !ok || got != LayerLocal {
			t.Errorf("SourceOf(%s) = %q, %v; a zero value is still an assignment", key, got, ok)
		}
	}
}

// TestLoad_EmptyTablesInTheOverlayAssignNothing pins the difference between a
// table and a value. The overlay below is what a person gets by copying the
// starter template and uncommenting one line: real section headers with nothing
// live under them. Treating `[frontmatter.properties]` as an assignment would
// overlay an empty map onto the vault's declarations and silently change what
// `bn set` and `bn lint` do, with a configuration that still loads cleanly. The
// inline `new.routes = {}` is the same rule in its other spelling; `[[views]]`
// checks that a table the overlay never mentions is untouched either way.
func TestLoad_EmptyTablesInTheOverlayAssignNothing(t *testing.T) {
	cfg, err := loadVault(t, newLayeredVault(t, `
[frontmatter.properties.tags]
type  = "list"
items = "string"

[new.routes]
note = "Notes"

[[views]]
name  = "Recent"
query = "-modified"
`, `
new.routes = {}

[frontmatter.properties]
# type = "list"

[capture]

[serve]
addr = "127.0.0.1:7000"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Frontmatter.Properties["tags"]; !ok {
		t.Errorf("frontmatter.properties = %v; an empty table erased the vault's declarations", cfg.Frontmatter.Properties)
	}
	if cfg.New.Routes["note"] != "Notes" {
		t.Errorf("new.routes = %v; an empty inline table erased the portable routes", cfg.New.Routes)
	}
	if len(cfg.Views) != 1 {
		t.Errorf("views = %v; a table the overlay never assigned must survive", cfg.Views)
	}
	for _, key := range []string{"frontmatter.properties", "new.routes", "capture.inbox_note"} {
		if got, ok := cfg.SourceOf(key); ok && got == LayerLocal {
			t.Errorf("SourceOf(%s) = local; an empty table assigns nothing", key)
		}
	}
	// The one real assignment in that overlay still lands.
	if cfg.Serve.Addr != "127.0.0.1:7000" {
		t.Errorf("serve.addr = %q; the overlay's only value was dropped too", cfg.Serve.Addr)
	}
}

func TestLoad_OverlayFailuresNameTheOverlayFile(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown key", "nonsense = 1\n", "unknown keys: nonsense"},
		{"invalid toml", "this is = = not toml\n", "parse"},
		{"removed vault key", "vault = \"/elsewhere\"\n", "the `vault` key was removed"},
		{
			"removed property model",
			"[frontmatter]\nlist_properties = [\"tags\"]\n",
			"frontmatter.list_properties",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newLayeredVault(t, "", tc.body)
			_, err := loadVault(t, root)
			if err == nil {
				t.Fatal("expected the overlay to be rejected")
			}
			if !strings.Contains(err.Error(), filepath.Join(root, LocalFileName)) {
				t.Errorf("error must name %s, the file that holds the problem; got %v", LocalFileName, err)
			}
			if strings.Contains(err.Error(), filepath.Join(root, FileName)) {
				t.Errorf("error must not blame the portable file; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q; got %v", tc.want, err)
			}
		})
	}
}

func TestLoad_UnreadableOverlayIsAnErrorRatherThanAnAbsentOne(t *testing.T) {
	// A directory in the overlay's place is readable enough to prove the file
	// exists and unreadable as TOML. Treating that as "no overlay" would hide a
	// broken vault behind a configuration that looks complete.
	root := newVault(t, "")
	if err := os.MkdirAll(filepath.Join(root, LocalFileName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := loadVault(t, root); err == nil {
		t.Fatal("expected an unreadable overlay to fail the load")
	}
}

func TestLoad_CompositeFingerprintTracksBothLayers(t *testing.T) {
	base, err := loadVault(t, newLayeredVault(t, legacyPortable, "[serve]\naddr = \"127.0.0.1:7000\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	same, err := loadVault(t, newLayeredVault(t, legacyPortable, "[serve]\naddr = \"127.0.0.1:7000\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if base.Fingerprint.Composite != same.Fingerprint.Composite {
		t.Error("identical bytes produced different composite fingerprints")
	}
	if base.Fingerprint.Portable != sha256Hex(legacyPortable) {
		t.Errorf("Fingerprint.Portable = %q, want the SHA-256 of the portable bytes", base.Fingerprint.Portable)
	}

	portableChanged, err := loadVault(t, newLayeredVault(t, legacyPortable+"\n", "[serve]\naddr = \"127.0.0.1:7000\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if portableChanged.Fingerprint.Composite == base.Fingerprint.Composite {
		t.Error("a changed portable file left the composite fingerprint alone")
	}

	localChanged, err := loadVault(t, newLayeredVault(t, legacyPortable, "[serve]\naddr = \"127.0.0.1:7001\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if localChanged.Fingerprint.Composite == base.Fingerprint.Composite {
		t.Error("a changed overlay left the composite fingerprint alone")
	}
	if localChanged.Fingerprint.Portable != base.Fingerprint.Portable {
		t.Error("changing the overlay must not move the portable digest")
	}
}

func TestDecode_AppliesTheOverlayOnDisk(t *testing.T) {
	// Decode is handed candidate portable bytes and answers what the vault
	// would load from them, so the overlay it already carries is part of the
	// answer.
	root := newLayeredVault(t, legacyPortable, "[serve]\naddr = \"127.0.0.1:7000\"\n")
	candidate := "[open]\neditor = \"nano\"\n"

	cfg, err := Decode(root, VaultOriginFlag, []byte(candidate))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.Serve.Addr != "127.0.0.1:7000" {
		t.Errorf("serve.addr = %q, want the on-disk overlay's value", cfg.Serve.Addr)
	}
	if cfg.Open.Editor != "nano" {
		t.Errorf("open.editor = %q, want the candidate's value", cfg.Open.Editor)
	}
	if cfg.Fingerprint.Portable != sha256Hex(candidate) {
		t.Error("Fingerprint.Portable must digest the bytes Decode was handed")
	}
}

func TestDecodeSources_NilLocalIsAbsentAndEmptyLocalIsPresent(t *testing.T) {
	root := newVault(t, legacyPortable)

	absent, err := DecodeSources(root, VaultOriginFlag, Sources{Portable: []byte(legacyPortable)})
	if err != nil {
		t.Fatalf("DecodeSources: %v", err)
	}
	if absent.Fingerprint.LocalPresent || absent.LocalSourcePath != "" {
		t.Error("a nil local source must load as an absent overlay")
	}

	empty, err := DecodeSources(root, VaultOriginFlag, Sources{Portable: []byte(legacyPortable), Local: []byte{}})
	if err != nil {
		t.Fatalf("DecodeSources: %v", err)
	}
	if !empty.Fingerprint.LocalPresent || empty.LocalSourcePath == "" {
		t.Error("an empty local source must load as a present, empty overlay")
	}
	if empty.Fingerprint.Composite == absent.Fingerprint.Composite {
		t.Error("nil and empty local sources produced the same composite fingerprint")
	}
}

func TestClone_CopiesProvenanceWithoutSharingIt(t *testing.T) {
	cfg, err := loadVault(t, newLayeredVault(t, legacyPortable, "[serve]\naddr = \"127.0.0.1:7000\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	clone := cfg.Clone()

	if got, _ := clone.SourceOf("serve.addr"); got != LayerLocal {
		t.Errorf("clone SourceOf(serve.addr) = %q, want %q", got, LayerLocal)
	}
	if clone.Fingerprint != cfg.Fingerprint {
		t.Error("Clone dropped the fingerprint")
	}
	if clone.LocalSourcePath != cfg.LocalSourcePath || clone.Enrollment != cfg.Enrollment {
		t.Error("Clone dropped a source-identity field")
	}
	clone.Provenance["serve.addr"] = LayerPortable
	if got, _ := cfg.SourceOf("serve.addr"); got != LayerLocal {
		t.Error("a clone shares its provenance map with its source")
	}
}
