package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// enroll marks a vault as enrolled in synchronization. Session-5 enrollment
// writes the real record; loading only ever asks whether the path exists.
func enroll(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, filepath.FromSlash(EnrollmentMarker)), "{}\n")
}

func TestVaultEnrollment_ReportsTheMarker(t *testing.T) {
	root := newVault(t, "")
	state, err := VaultEnrollment(root)
	if err != nil {
		t.Fatalf("VaultEnrollment: %v", err)
	}
	if state != EnrollmentNone {
		t.Fatalf("state = %q, want %q before enrollment", state, EnrollmentNone)
	}

	enroll(t, root)
	state, err = VaultEnrollment(root)
	if err != nil {
		t.Fatalf("VaultEnrollment: %v", err)
	}
	if state != EnrollmentEnrolled {
		t.Fatalf("state = %q, want %q once the marker exists", state, EnrollmentEnrolled)
	}
}

func TestLoad_UnenrolledVaultAcceptsEveryPlacement(t *testing.T) {
	// A vault that synchronizes with nothing cannot suffer a synchronization
	// fault, so both the legacy placement (machine settings in the portable
	// file) and a deliberate overlay override stay legal.
	root := newLayeredVault(t, `
[serve]
addr = "127.0.0.1:9999"

[capture]
heading = "## Portable"
`, `
[capture]
heading = "## Local"
`)

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Serve.Addr != "127.0.0.1:9999" {
		t.Errorf("serve.addr = %q, want the portable file's value", cfg.Serve.Addr)
	}
	if cfg.Capture.Heading != "## Local" {
		t.Errorf("capture.heading = %q, want the overlay to win an unenrolled conflict", cfg.Capture.Heading)
	}
}

func TestLoad_EnrolledVaultRejectsLocalKeysInThePortableFile(t *testing.T) {
	root := newVault(t, `
[serve]
addr = "127.0.0.1:9999"

[open]
editor = "vim"
`)
	enroll(t, root)

	_, err := loadVault(t, root)
	if err == nil {
		t.Fatal("expected an enrolled vault to refuse machine settings in the portable file")
	}
	for _, want := range []string{filepath.Join(root, FileName), LocalFileName, "open.editor", "serve.addr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got %v", want, err)
		}
	}
	// Every misplaced key is named at once, sorted, so one round of edits fixes
	// the file rather than one key per load.
	if i, j := strings.Index(err.Error(), "open.editor"), strings.Index(err.Error(), "serve.addr"); i > j {
		t.Errorf("misplaced keys should be listed sorted; got %v", err)
	}
}

func TestLoad_EnrolledVaultRejectsPortableKeysInTheOverlay(t *testing.T) {
	root := newLayeredVault(t, "", `
[capture]
heading = "## Local"
`)
	enroll(t, root)

	_, err := loadVault(t, root)
	if err == nil {
		t.Fatal("expected an enrolled vault to refuse a vault setting in the overlay")
	}
	if !strings.Contains(err.Error(), filepath.Join(root, LocalFileName)) {
		t.Errorf("error should name the overlay; got %v", err)
	}
	if !strings.Contains(err.Error(), "capture.heading") || !strings.Contains(err.Error(), FileName) {
		t.Errorf("error should name the key and the file that owns it; got %v", err)
	}
}

func TestLoad_EnrolledVaultAcceptsCorrectlyPlacedKeys(t *testing.T) {
	root := newLayeredVault(t, `
[capture]
heading = "## Captures"

[[views]]
name = "Active"
query = "status:wip"
`, `
[serve]
addr = "127.0.0.1:7000"

[agent]
command = ["my-agent"]
`)
	enroll(t, root)

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Enrollment != EnrollmentEnrolled {
		t.Errorf("Enrollment = %q, want %q", cfg.Enrollment, EnrollmentEnrolled)
	}
	if cfg.Serve.Addr != "127.0.0.1:7000" || len(cfg.Agent.Command) != 1 {
		t.Error("the overlay's machine settings did not reach the merged configuration")
	}
	if len(cfg.Views) != 1 || cfg.Capture.Heading != "## Captures" {
		t.Error("the portable file's vault settings did not reach the merged configuration")
	}
}

// TestLoad_EnrolledVaultToleratesBareHeadersInBothFiles is the ownership half of
// the header-versus-value rule. A section header with everything under it
// commented out is how a person keeps the starter template's documentation in
// front of them; it assigns nothing, so it cannot be misplaced, and refusing to
// open the vault over one would be an error about a comment.
func TestLoad_EnrolledVaultToleratesBareHeadersInBothFiles(t *testing.T) {
	root := newLayeredVault(t, `
[serve]
# addr = "127.0.0.1:7000"

[agent]

[capture]
heading = "## Captures"
`, `
[frontmatter.properties]

[capture]
# heading = "## Notes"

[open]
editor = "vim"
`)
	enroll(t, root)

	cfg, err := loadVault(t, root)
	if err != nil {
		t.Fatalf("a bare header is not a misplaced key: %v", err)
	}
	if cfg.Capture.Heading != "## Captures" || cfg.Open.Editor != "vim" {
		t.Errorf("the real assignments were lost: heading=%q editor=%q", cfg.Capture.Heading, cfg.Open.Editor)
	}
}

func TestLoad_EnrolledVaultAcceptsTheStarterConfiguration(t *testing.T) {
	// `bn init` writes DefaultTemplate. Every machine key in it is commented
	// out, so a vault created today is enrollable without being edited first.
	root := newVault(t, DefaultTemplate())
	enroll(t, root)

	if _, err := loadVault(t, root); err != nil {
		t.Fatalf("the starter configuration must load on an enrolled vault: %v", err)
	}
}

func TestVaultEnrollment_UnreadableMarkerIsAnError(t *testing.T) {
	// Reporting an unreadable marker as "not enrolled" would silently switch
	// off the ownership rules on a vault that is in fact synchronizing.
	root := newVault(t, "")
	parent := filepath.Join(root, filepath.FromSlash(".bnotes/sync"))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(parent, "enrollment.json"), "{}\n")
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Skipf("cannot drop directory permissions here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	if _, err := VaultEnrollment(root); err == nil {
		t.Fatal("expected an unreadable enrollment marker to be an error")
	}
}

func TestFieldSpecs_CoverEveryConfigKeyExactlyOnce(t *testing.T) {
	// The ownership table is the schema's second half: a field the decoder can
	// fill but nothing here names would silently belong to neither file, and
	// would neither be overlaid nor checked for misplacement.
	declared := map[string]bool{}
	for _, spec := range fieldSpecs {
		if declared[spec.key] {
			t.Errorf("field %q is declared twice", spec.key)
		}
		declared[spec.key] = true
		if spec.layer != LayerPortable && spec.layer != LayerLocal {
			t.Errorf("field %q has unknown layer %q", spec.key, spec.layer)
		}
	}

	for _, key := range tomlKeyPaths(t, reflect.TypeOf(Config{}), "") {
		if !declared[key] {
			t.Errorf("configuration key %q has no owner in fieldSpecs", key)
		}
		delete(declared, key)
	}
	for key := range declared {
		t.Errorf("fieldSpecs declares %q, which is not a configuration key", key)
	}
}

func TestFieldSpecs_NoKeyShadowsAnother(t *testing.T) {
	// specFor returns the first governing field, so two fields may never both
	// govern one decoded key.
	for i, outer := range fieldSpecs {
		for j, inner := range fieldSpecs {
			if i != j && outer.governs(inner.key) {
				t.Errorf("field %q governs field %q, so one of them can never be seen", outer.key, inner.key)
			}
		}
	}
}

func TestFieldSpecs_CopyExactlyTheFieldTheyName(t *testing.T) {
	// A copy function that reaches for the wrong struct field would overlay the
	// wrong setting with no visible error anywhere.
	for _, spec := range fieldSpecs {
		t.Run(spec.key, func(t *testing.T) {
			src, dst := &Config{}, &Config{}
			field, ok := fieldByTOMLKey(reflect.ValueOf(src).Elem(), spec.key)
			if !ok {
				t.Fatalf("no struct field for key %q", spec.key)
			}
			setNonZero(t, field)
			spec.copy(dst, src)
			assertOnlyKeyWasCopied(t, dst, spec.key)
		})
	}
}

// assertOnlyKeyWasCopied checks every field in the ownership table against a
// destination that should carry exactly one of them.
func assertOnlyKeyWasCopied(t *testing.T, dst *Config, copied string) {
	t.Helper()
	for _, other := range fieldSpecs {
		got, ok := fieldByTOMLKey(reflect.ValueOf(dst).Elem(), other.key)
		if !ok {
			t.Fatalf("no struct field for key %q", other.key)
		}
		if other.key == copied && got.IsZero() {
			t.Fatalf("copying %q left it unset", copied)
		}
		if other.key != copied && !got.IsZero() {
			t.Fatalf("copying %q also wrote %q", copied, other.key)
		}
	}
}

func TestLayerKeys_PartitionTheSchema(t *testing.T) {
	portable, local := LayerKeys(LayerPortable), LayerKeys(LayerLocal)
	if len(portable)+len(local) != len(fieldSpecs) {
		t.Fatalf("LayerKeys covers %d of %d fields", len(portable)+len(local), len(fieldSpecs))
	}
	for _, key := range append(append([]string{}, portable...), local...) {
		if _, ok := KeyLayer(key); !ok {
			t.Errorf("KeyLayer(%q) does not resolve", key)
		}
	}
	if !sorted(portable) || !sorted(local) {
		t.Error("LayerKeys must return sorted keys")
	}
	if layer, ok := KeyLayer("serve"); ok {
		t.Errorf("KeyLayer(\"serve\") = %q; a bare table header owns nothing", layer)
	}
	if layer, ok := KeyLayer("frontmatter.properties.status.type"); !ok || layer != LayerPortable {
		t.Errorf("KeyLayer of a nested table key = %q,%v; want %q,true", layer, ok, LayerPortable)
	}
}

func sorted(keys []string) bool {
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			return false
		}
	}
	return true
}

// tomlKeyPaths enumerates every key path the TOML decoder can fill on typ.
// Recursion stops at anything that is not a struct, so a table of user-named
// entries (`frontmatter.properties`, `new.routes`) and an array of tables
// (`views`) are each one key.
func tomlKeyPaths(t *testing.T, typ reflect.Type, prefix string) []string {
	t.Helper()
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("toml"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			t.Fatalf("%s.%s has no toml tag, so it is neither loadable nor ownable", typ.Name(), field.Name)
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if field.Type.Kind() == reflect.Struct {
			out = append(out, tomlKeyPaths(t, field.Type, key)...)
			continue
		}
		out = append(out, key)
	}
	return out
}

// fieldByTOMLKey walks a Config value to the field a TOML key path names.
func fieldByTOMLKey(v reflect.Value, key string) (reflect.Value, bool) {
	for _, part := range strings.Split(key, ".") {
		typ := v.Type()
		next := -1
		for i := 0; i < typ.NumField(); i++ {
			if strings.Split(typ.Field(i).Tag.Get("toml"), ",")[0] == part {
				next = i
				break
			}
		}
		if next < 0 {
			return reflect.Value{}, false
		}
		v = v.Field(next)
	}
	return v, true
}

// setNonZero gives a field a value reflect.Value.IsZero reports as set.
func setNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("set")
	case reflect.Int, reflect.Int64:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	default:
		t.Fatalf("no non-zero value known for kind %s", v.Kind())
	}
}
