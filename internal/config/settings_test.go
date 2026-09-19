package config

import (
	"sort"
	"strings"
	"testing"
)

// TestEditableSettings_CoverEveryPortableKeyExactlyOnce is the field-ownership
// gate: every portable key is either editable as a typed value or written down
// as owned by another workflow, and no key is both. A key added to fieldSpecs
// fails this until someone decides which it is.
func TestEditableSettings_CoverEveryPortableKeyExactlyOnce(t *testing.T) {
	editable := editableKeySet(t)

	for _, key := range LayerKeys(LayerPortable) {
		_, elsewhere := settingsOwnedElsewhere[key]
		switch {
		case editable[key] && elsewhere:
			t.Errorf("%s is both editable and owned elsewhere", key)
		case !editable[key] && !elsewhere:
			t.Errorf("portable key %s is neither editable nor recorded in settingsOwnedElsewhere", key)
		}
	}
}

// editableKeySet indexes the editable table, failing on a key it lists twice —
// which would otherwise make the coverage check above pass on a table missing a
// key, since one duplicate hides one omission.
func editableKeySet(t *testing.T) map[string]bool {
	t.Helper()

	editable := make(map[string]bool, len(settingSpecs))
	for _, spec := range settingSpecs {
		if editable[spec.Key] {
			t.Fatalf("editable settings list %s twice", spec.Key)
		}
		editable[spec.Key] = true
	}
	return editable
}

// TestSettingsOwnedElsewhere_NamesOnlyPortableKeys is the other direction of the
// same table: an exemption for a key that is machine-local, or for one the
// ownership table does not know at all, exempts nothing and hides a typo.
func TestSettingsOwnedElsewhere_NamesOnlyPortableKeys(t *testing.T) {
	for key := range settingsOwnedElsewhere {
		if layer, ok := KeyLayer(key); !ok || layer != LayerPortable {
			t.Errorf("settingsOwnedElsewhere names %s, which is not a portable key", key)
		}
	}
}

// TestEditableSettings_ExcludeEveryMachineLocalKey is the privacy half of the
// same gate. The eight local keys name a binary to run or an address to bind;
// none of them may be reachable through a surface that edits what travels.
func TestEditableSettings_ExcludeEveryMachineLocalKey(t *testing.T) {
	local := LayerKeys(LayerLocal)
	if len(local) == 0 {
		t.Fatal("no machine-local keys: the ownership table lost its local half")
	}
	for _, key := range local {
		if _, ok := SettingSpecFor(key); ok {
			t.Errorf("machine-local key %s is exposed as an editable setting", key)
		}
		if _, err := PatchSetting(nil, key, StringSetting("x")); err == nil {
			t.Errorf("PatchSetting accepted machine-local key %s", key)
		}
		if _, err := UnsetSetting(nil, key); err == nil {
			t.Errorf("UnsetSetting accepted machine-local key %s", key)
		}
	}
}

func TestEditableSettings_AreSortedAndDescribed(t *testing.T) {
	specs := EditableSettings()
	keys := make([]string, len(specs))
	for i, spec := range specs {
		keys[i] = spec.Key
		if strings.TrimSpace(spec.Summary) == "" {
			t.Errorf("%s has no summary", spec.Key)
		}
		if spec.read == nil {
			t.Errorf("%s cannot read its current value", spec.Key)
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("EditableSettings is not sorted: %v", keys)
	}
}

func TestEditableSettings_ReturnsACopy(t *testing.T) {
	first := EditableSettings()
	if len(first) == 0 {
		t.Fatal("no editable settings")
	}
	first[0] = SettingSpec{Key: "zzz"}
	if EditableSettings()[0].Key == "zzz" {
		t.Fatal("EditableSettings handed out the shared table")
	}
}

func TestSettingSpec_TableAndName(t *testing.T) {
	for _, tc := range []struct{ key, table, name string }{
		{"ignore", "", "ignore"},
		{"templates.dir", "templates", "dir"},
		{"cockpit.long_tail_limit", "cockpit", "long_tail_limit"},
	} {
		spec := SettingSpec{Key: tc.key}
		if spec.Table() != tc.table || spec.Name() != tc.name {
			t.Errorf("%s: got table %q name %q, want %q/%q", tc.key, spec.Table(), spec.Name(), tc.table, tc.name)
		}
	}
}

// TestSettingValueOf_ReadsEveryEditableKey proves each spec's reader is wired to
// the field it names, by loading a config that assigns every key and checking
// the value comes back.
func TestSettingValueOf_ReadsEveryEditableKey(t *testing.T) {
	cfg := &Config{}
	cfg.Templates.Dir = "Blueprints"
	cfg.Cockpit.SensitiveScan = true
	cfg.Cockpit.MaxWIP = 11
	cfg.Ignore = []string{"Archive"}

	for _, tc := range []struct {
		key  string
		want SettingValue
	}{
		{"templates.dir", StringSetting("Blueprints")},
		{"cockpit.sensitive_scan", BoolSetting(true)},
		{"cockpit.max_wip", IntSetting(11)},
		{"ignore", ListSetting([]string{"Archive"})},
	} {
		got, err := SettingValueOf(cfg, tc.key)
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		if !settingValuesEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.key, got, tc.want)
		}
	}

	if _, err := SettingValueOf(cfg, "serve.addr"); err == nil {
		t.Error("SettingValueOf read a machine-local key")
	}
}

func TestSettingValueOf_NamesTheOwningWorkflow(t *testing.T) {
	_, err := SettingValueOf(&Config{}, "views")
	if err == nil || !strings.Contains(err.Error(), "saved-view workflow") {
		t.Fatalf("got %v, want an error naming the saved-view workflow", err)
	}
}
