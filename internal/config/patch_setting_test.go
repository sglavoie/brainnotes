package config

import (
	"strings"
	"testing"
)

func mustPatchSetting(t *testing.T, source []byte, key string, value SettingValue) PatchResult {
	t.Helper()
	result, err := PatchSetting(source, key, value)
	if err != nil {
		t.Fatalf("PatchSetting(%s): %v", key, err)
	}
	return result
}

func TestPatchSetting_RewritesOneAssignmentAndKeepsEverythingElse(t *testing.T) {
	source := []byte(strings.Join([]string{
		"# vault configuration",
		"ignore = [\"Archive\"]",
		"",
		"[cockpit]",
		"# how many tasks a day",
		"daily_limit = 5 # tuned in March",
		"max_wip = 3",
		"",
	}, "\n"))

	result := mustPatchSetting(t, source, "cockpit.daily_limit", IntSetting(9))
	if !result.Changed {
		t.Fatal("patch reported no change")
	}
	got := string(result.Patched)
	if !strings.Contains(got, "daily_limit = 9 # tuned in March") {
		t.Errorf("assignment or inline comment lost:\n%s", got)
	}
	for _, keep := range []string{"# vault configuration", "# how many tasks a day", "max_wip = 3"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
}

func TestPatchSetting_RendersEachKind(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value SettingValue
		want  string
	}{
		{"cockpit.review_date_property", StringSetting("reviewed"), `review_date_property = "reviewed"`},
		{"cockpit.sensitive_scan", BoolSetting(true), "sensitive_scan = true"},
		{"cockpit.max_wip", IntSetting(4), "max_wip = 4"},
		{"cockpit.wip_statuses", ListSetting([]string{"doing", "wip"}), `wip_statuses = ["doing", "wip"]`},
		{"cockpit.wip_statuses", ListSetting(nil), "wip_statuses = []"},
	} {
		result := mustPatchSetting(t, nil, tc.key, tc.value)
		if !strings.Contains(string(result.Patched), tc.want) {
			t.Errorf("%s: got\n%s\nwant a line %q", tc.key, result.Patched, tc.want)
		}
	}
}

func TestPatchSetting_ReassertingTheStoredValueIsANoOp(t *testing.T) {
	source := []byte("[cockpit]\nmax_wip = 3\n")
	result := mustPatchSetting(t, source, "cockpit.max_wip", IntSetting(3))
	if result.Changed || len(result.Diff) != 0 {
		t.Fatalf("reassertion reported a change: %+v", result)
	}
	if string(result.Patched) != string(source) {
		t.Fatalf("bytes moved:\n%s", result.Patched)
	}
}

// A value equal to the key's default over a file that assigns nothing is a real
// change: writing the default down is a decision the file did not record before.
func TestPatchSetting_WritingTheDefaultOverAnUnassignedKeyChangesTheFile(t *testing.T) {
	result := mustPatchSetting(t, []byte("[cockpit]\n"), "cockpit.max_wip", IntSetting(DefaultCockpitMaxWIP))
	if !result.Changed {
		t.Fatal("assigning the default over an absent key reported no change")
	}
}

func TestPatchSetting_CreatesTheTableWhenItIsAbsent(t *testing.T) {
	result := mustPatchSetting(t, []byte("ignore = [\"Archive\"]\n"), "capture.heading", StringSetting("## Inbox"))
	got := string(result.Patched)
	if !strings.Contains(got, "[capture]") || !strings.Contains(got, `heading = "## Inbox"`) {
		t.Fatalf("table not created:\n%s", got)
	}
	if !strings.HasPrefix(got, "ignore = ") {
		t.Fatalf("existing content moved:\n%s", got)
	}
}

func TestPatchSetting_PlacesARootKeyAboveTheFirstTable(t *testing.T) {
	source := []byte("# header comment\n\n[cockpit]\nmax_wip = 3\n")
	result := mustPatchSetting(t, source, "ignore", ListSetting([]string{"Archive", "Trash"}))
	got := string(result.Patched)
	ignoreAt := strings.Index(got, "ignore = ")
	tableAt := strings.Index(got, "[cockpit]")
	if ignoreAt < 0 || tableAt < 0 || ignoreAt > tableAt {
		t.Fatalf("root key did not land above the first table:\n%s", got)
	}
	if !strings.HasPrefix(got, "# header comment") {
		t.Fatalf("preamble comment moved:\n%s", got)
	}
}

// A `["a"]` line inside another key's multi-line array parses as a table header
// on its own. Reading it as one would make the scan stop early, report the key
// unassigned, and insert a second binding that TOML refuses.
func TestPatchSetting_ReadsPastABracketLineInsideAnArray(t *testing.T) {
	source := []byte("other = [\n  [\"a\"]\n]\nignore = [\"Archive\"]\n\n[cockpit]\n")
	result := mustPatchSetting(t, source, "ignore", ListSetting([]string{"Trash"}))
	got := string(result.Patched)
	if strings.Count(got, "ignore = ") != 1 {
		t.Fatalf("key bound twice:\n%s", got)
	}
	if !strings.Contains(got, `ignore = ["Trash"]`) {
		t.Fatalf("assignment not rewritten:\n%s", got)
	}
}

func TestPatchSetting_CollapsesAMultiLineValue(t *testing.T) {
	source := []byte("[cockpit]\nwip_statuses = [\n  \"doing\",\n  \"wip\",\n]\nmax_wip = 3\n")
	result := mustPatchSetting(t, source, "cockpit.wip_statuses", ListSetting([]string{"active"}))
	got := string(result.Patched)
	if strings.Contains(got, `"doing"`) || strings.Contains(got, `"wip"`) {
		t.Fatalf("continuation lines survived:\n%s", got)
	}
	if !strings.Contains(got, "max_wip = 3") {
		t.Fatalf("the following key was eaten:\n%s", got)
	}
}

func TestPatchSetting_RespellsADottedRootAssignment(t *testing.T) {
	source := []byte("cockpit.max_wip = 3\n")
	result := mustPatchSetting(t, source, "cockpit.max_wip", IntSetting(7))
	if got := string(result.Patched); !strings.Contains(got, "cockpit.max_wip = 7") {
		t.Fatalf("dotted key not rewritten in place:\n%s", got)
	}
}

func TestPatchSetting_RefusesAValueOfTheWrongKind(t *testing.T) {
	_, err := PatchSetting(nil, "cockpit.max_wip", StringSetting("lots"))
	if err == nil || !strings.Contains(err.Error(), "holds a int value") {
		t.Fatalf("got %v, want a kind mismatch", err)
	}
}

func TestPatchSetting_RefusesAnUnknownKey(t *testing.T) {
	if _, err := PatchSetting(nil, "cockpit.nope", IntSetting(1)); err == nil {
		t.Fatal("unknown key accepted")
	}
}

// Malformed configuration recovery: a body that does not decode is refused
// before anything is rewritten, and the message says the file needs repair
// rather than reporting a successful edit of a file that cannot load.
func TestPatchSetting_RefusesAMalformedBody(t *testing.T) {
	source := []byte("[cockpit\nmax_wip = 3\n")
	_, err := PatchSetting(source, "cockpit.max_wip", IntSetting(4))
	if err == nil || !strings.Contains(err.Error(), "not valid TOML") {
		t.Fatalf("got %v, want a malformed-config refusal", err)
	}
	if _, err := UnsetSetting(source, "cockpit.max_wip"); err == nil {
		t.Fatal("unset accepted a malformed body")
	}
}

func TestPatchSetting_KeepsACRLFFileConsistent(t *testing.T) {
	source := []byte("[cockpit]\r\nmax_wip = 3\r\n")
	result := mustPatchSetting(t, source, "cockpit.daily_limit", IntSetting(8))
	got := string(result.Patched)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("a lone LF line was introduced: %q", got)
	}
}

// A control character in a value cannot become a second line: quoteTOMLString
// escapes it, so one assignment stays one assignment.
func TestPatchSetting_EscapesAValueThatWouldSplitTheLine(t *testing.T) {
	result := mustPatchSetting(t, nil, "capture.heading", StringSetting("## In\nbox"))
	got := string(result.Patched)
	if strings.Contains(got, "\nbox") {
		t.Fatalf("value broke the assignment across lines:\n%q", got)
	}
	value, assigned, err := readSetting(result.Patched, SettingSpec{Key: "capture.heading", Kind: SettingString})
	if err != nil || !assigned || value.Text != "## In\nbox" {
		t.Fatalf("round trip lost the value: %+v assigned=%v err=%v", value, assigned, err)
	}
}

func TestPatchSetting_DelegatesAutosaveToItsOwnPatcher(t *testing.T) {
	// "300s" and "5m" are the same policy, and the dedicated patcher treats a
	// restatement of the policy in force as a no-op whatever the spelling.
	source := []byte("[autosave]\nmin_interval = \"300s\"\n")
	result := mustPatchSetting(t, source, "autosave.min_interval", StringSetting("5m"))
	if result.Changed {
		t.Fatalf("autosave restatement rewrote the file:\n%s", result.Patched)
	}
	if _, err := PatchSetting(source, "autosave.min_interval", StringSetting("")); err == nil {
		t.Fatal("autosave accepted an empty value instead of directing at unset")
	}
}

func TestUnsetSetting_RemovesTheAssignmentAndKeepsTheTable(t *testing.T) {
	source := []byte("[cockpit]\n# keep me\nmax_wip = 3\ndaily_limit = 5\n")
	result, err := UnsetSetting(source, "cockpit.max_wip")
	if err != nil {
		t.Fatalf("UnsetSetting: %v", err)
	}
	got := string(result.Patched)
	if strings.Contains(got, "max_wip") {
		t.Fatalf("assignment survived:\n%s", got)
	}
	for _, keep := range []string{"[cockpit]", "# keep me", "daily_limit = 5"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
}

func TestUnsetSetting_IsANoOpWhenTheKeyIsAbsent(t *testing.T) {
	source := []byte("[cockpit]\ndaily_limit = 5\n")
	result, err := UnsetSetting(source, "cockpit.max_wip")
	if err != nil {
		t.Fatalf("UnsetSetting: %v", err)
	}
	if result.Changed || string(result.Patched) != string(source) {
		t.Fatalf("absent key reported a change: %+v", result)
	}
}

func TestUnsetSetting_RemovesARootKey(t *testing.T) {
	source := []byte("ignore = [\"Archive\"]\n\n[cockpit]\nmax_wip = 3\n")
	result, err := UnsetSetting(source, "ignore")
	if err != nil {
		t.Fatalf("UnsetSetting: %v", err)
	}
	got := string(result.Patched)
	if strings.Contains(got, "ignore = ") {
		t.Fatalf("root key survived:\n%s", got)
	}
	if !strings.Contains(got, "max_wip = 3") {
		t.Fatalf("the rest of the file moved:\n%s", got)
	}
}

// Every editable key must survive a set/unset round trip against the shipped
// starter template, which is the file a real vault begins from.
func TestPatchSetting_RoundTripsEveryEditableKeyAgainstTheStarterTemplate(t *testing.T) {
	source := []byte(DefaultTemplate())
	for _, spec := range EditableSettings() {
		value := sampleSettingValue(spec)
		patched, err := PatchSetting(source, spec.Key, value)
		if err != nil {
			t.Fatalf("%s: PatchSetting: %v", spec.Key, err)
		}
		got, assigned, err := readSetting(patched.Patched, spec)
		if err != nil || !assigned || !settingValuesEqual(got, value) {
			t.Fatalf("%s: round trip gave %+v assigned=%v err=%v", spec.Key, got, assigned, err)
		}
		cleared, err := UnsetSetting(patched.Patched, spec.Key)
		if err != nil {
			t.Fatalf("%s: UnsetSetting: %v", spec.Key, err)
		}
		if _, stillAssigned, err := readSetting(cleared.Patched, spec); err != nil || stillAssigned {
			t.Fatalf("%s: unset left the key assigned (err=%v)", spec.Key, err)
		}
	}
}

// sampleSettingValue is a value of the right kind that is not the key's default,
// so a patch that silently did nothing fails the round trip.
func sampleSettingValue(spec SettingSpec) SettingValue {
	switch spec.Kind {
	case SettingBool:
		return BoolSetting(true)
	case SettingInt:
		return IntSetting(17)
	case SettingStringList:
		return ListSetting([]string{"alpha", "beta"})
	}
	if spec.Key == autosaveSettingKey {
		return StringSetting("10m")
	}
	if spec.Key == "templates.default_extension" {
		return StringSetting(".markdown")
	}
	return StringSetting("sample")
}

func TestDiffText_ReportsLineLevelChanges(t *testing.T) {
	diff := DiffText([]byte("a\nb\n"), []byte("a\nc\n"))
	if len(diff) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(diff), diff)
	}
	if diff[0].Kind != PatchLineRemoved || diff[0].Text != "b" {
		t.Errorf("first line: %+v", diff[0])
	}
	if diff[1].Kind != PatchLineAdded || diff[1].Text != "c" {
		t.Errorf("second line: %+v", diff[1])
	}
	if len(DiffText([]byte("a\n"), []byte("a\n"))) != 0 {
		t.Error("identical bodies reported a difference")
	}
}
