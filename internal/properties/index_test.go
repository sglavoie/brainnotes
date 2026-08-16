package properties

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestBuild_IndexesStoredAndConfiguredProperties(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nstatus: active\ntags:\n  - go\n  - web\n---\nbody\n",
		"Notes/Beta.md":  "---\nstatus: draft\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(map[string]config.PropertyDefinition{
		"status": {Type: config.KindString},
		"area":   {Type: config.KindWikilink},
	}))

	if got, want := index.TotalNotes, 2; got != want {
		t.Fatalf("TotalNotes = %d, want %d", got, want)
	}
	if got, want := index.Names(), []string{"area", "status", "tags"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}

	area := propertyByName(t, index, "area")
	if !area.Configured || area.NoteCount != 0 {
		t.Fatalf("area = {configured:%v notes:%d}, want a configured property with no notes", area.Configured, area.NoteCount)
	}
	tags := propertyByName(t, index, "tags")
	if tags.Configured {
		t.Fatalf("tags should not be reported as configured")
	}
	if got, want := tags.NoteCount, 1; got != want {
		t.Fatalf("tags NoteCount = %d, want %d", got, want)
	}

	status := propertyByName(t, index, "status")
	if got, want := status.Coverage(), 1.0; got != want {
		t.Fatalf("status Coverage() = %v, want %v", got, want)
	}
	if got, want := index.Totals.Configured, 2; got != want {
		t.Fatalf("Totals.Configured = %d, want %d", got, want)
	}
	if got, want := index.Totals.Unconfigured, 1; got != want {
		t.Fatalf("Totals.Unconfigured = %d, want %d", got, want)
	}
}

func TestBuild_CountsEachLogicalValueOncePerNote(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - go\n  - go\n  - web\n---\nbody\n",
		"Notes/Beta.md":  "---\ntags:\n  - go\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	tags := propertyByName(t, index, "tags")

	if got, want := valueTexts(tags.Values), []string{"go", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("value texts = %v, want %v", got, want)
	}
	if got, want := tags.Values[0].Notes, 2; got != want {
		t.Fatalf("go note count = %d, want %d (a repeated list item must not inflate it)", got, want)
	}
	if got, want := tags.Values[1].Notes, 1; got != want {
		t.Fatalf("web note count = %d, want %d", got, want)
	}
	if got, want := tags.DistinctValues(), 2; got != want {
		t.Fatalf("DistinctValues() = %d, want %d", got, want)
	}
}

func TestBuild_SeparatesValuesByKind(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nrank: 1\n---\nbody\n",
		"Notes/Beta.md":  "---\nrank: \"1\"\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	rank := propertyByName(t, index, "rank")

	if got, want := rank.DistinctValues(), 2; got != want {
		t.Fatalf("DistinctValues() = %d, want %d (int 1 and string \"1\" are distinct)", got, want)
	}
	if got, want := kindSet(rank.ObservedKinds), map[Kind]bool{config.KindInt: true, config.KindString: true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ObservedKinds = %v, want int and string", rank.ObservedKinds)
	}
}

func TestBuild_RecordsViolationsAndMissingRequiredRows(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nstatus: active\n---\nbody\n",
		"Notes/Beta.md":  "---\ntitle: Beta\n---\nbody\n",
		"Notes/Gamma.md": "---\nstatus: retired\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Required: true, Allowed: []string{"active", "draft"}},
	}))
	status := propertyByName(t, index, "status")

	if got, want := status.MissingRequired, 1; got != want {
		t.Fatalf("MissingRequired = %d, want %d", got, want)
	}
	if got, want := status.ViolationCounts.Missing, 1; got != want {
		t.Fatalf("ViolationCounts.Missing = %d, want %d", got, want)
	}
	if got, want := status.ViolationCounts.Value, 1; got != want {
		t.Fatalf("ViolationCounts.Value = %d, want %d", got, want)
	}
	if got, want := status.ViolationNotes, 2; got != want {
		t.Fatalf("ViolationNotes = %d, want %d", got, want)
	}
	if got, want := rowPaths(status.Rows), []string{"Notes/Alpha.md", "Notes/Beta.md", "Notes/Gamma.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want a row per note including the one missing a required value", got)
	}
	for _, row := range status.Rows {
		if row.RelPath == "Notes/Beta.md" && row.Present {
			t.Fatalf("Notes/Beta.md must be reported as not present")
		}
	}
	if got, want := index.Totals.Violations, 2; got != want {
		t.Fatalf("Totals.Violations = %d, want %d", got, want)
	}
	if got, want := index.Totals.ViolationNotes, 2; got != want {
		t.Fatalf("Totals.ViolationNotes = %d, want %d", got, want)
	}
}

func TestBuild_PreservesUnsupportedValuesSafely(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nmeta:\n  nested: 1\n---\nbody\n",
		"Notes/Beta.md":  "---\nmeta:\n---\nbody\n",
		"Notes/Gamma.md": "---\nmeta: plain\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	meta := propertyByName(t, index, "meta")

	if got, want := meta.UnsupportedValues, 2; got != want {
		t.Fatalf("UnsupportedValues = %d, want %d", got, want)
	}
	if got, want := meta.UnsupportedNotes, 2; got != want {
		t.Fatalf("UnsupportedNotes = %d, want %d", got, want)
	}
	labels := map[string]bool{}
	for _, value := range meta.Values {
		if !value.Value.Supported {
			labels[value.Value.Text] = true
			if value.Value.Value != nil {
				t.Fatalf("unsupported value %q carried a JSON scalar %#v", value.Value.Text, value.Value.Value)
			}
		}
	}
	if !labels["<map>"] || !labels["<null>"] {
		t.Fatalf("unsupported labels = %v, want <map> and <null>", labels)
	}
	if got, want := index.Totals.Unsupported, 2; got != want {
		t.Fatalf("Totals.Unsupported = %d, want %d", got, want)
	}
}

func TestBuild_UnsupportedListElementMarksRow(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - go\n  -\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	tags := propertyByName(t, index, "tags")

	if len(tags.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(tags.Rows))
	}
	if !tags.Rows[0].HasUnsupported() {
		t.Fatalf("row with a null list element must report HasUnsupported")
	}
	if !tags.Rows[0].Value.Supported {
		t.Fatalf("the list itself is a supported shape even when an element is not")
	}
}

func TestBuild_SkipsNotesWithUnparseableFrontmatter(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md":  "---\nstatus: active\n---\nbody\n",
		"Notes/Broken.md": "---\nstatus: [unterminated\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))

	if got, want := index.TotalNotes, 1; got != want {
		t.Fatalf("TotalNotes = %d, want %d (a note that will not parse is skipped)", got, want)
	}
	status := propertyByName(t, index, "status")
	if got, want := rowPaths(status.Rows), []string{"Notes/Alpha.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want only the parseable note", got)
	}
}

func TestBuild_SortsValuesByNoteCountThenText(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\ntags:\n  - zeta\n  - Beta\n  - common\n---\nbody\n",
		"Notes/Beta.md":  "---\ntags:\n  - common\n---\nbody\n",
	})

	index := Build(root, notes, frontmatterConfig(nil))
	tags := propertyByName(t, index, "tags")

	if got, want := valueTexts(tags.Values), []string{"common", "Beta", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("value order = %v, want %v", got, want)
	}
}

func TestBuild_DoesNotMutateInputs(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nstatus: active\n---\nbody\n",
	})
	definitions := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Allowed: []string{"active"}},
	}
	fm := frontmatterConfig(definitions)

	index := Build(root, notes, fm)
	status := propertyByName(t, index, "status")
	if status.Definition == nil {
		t.Fatalf("configured property has no definition")
	}
	status.Definition.Type = config.KindInt

	if got := definitions["status"].Type; got != config.KindString {
		t.Fatalf("configured definition mutated to %q; Build must copy definitions", got)
	}
}

func TestBuild_NilFrontmatterConfigIndexesStoredPropertiesOnly(t *testing.T) {
	root, notes := testVault(t, map[string]string{
		"Notes/Alpha.md": "---\nstatus: active\n---\nbody\n",
	})

	index := Build(root, notes, nil)

	if got, want := index.Names(), []string{"status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if got, want := index.Totals.Configured, 0; got != want {
		t.Fatalf("Totals.Configured = %d, want %d", got, want)
	}
}

func kindSet(kinds []Kind) map[Kind]bool {
	out := make(map[Kind]bool, len(kinds))
	for _, kind := range kinds {
		out[kind] = true
	}
	return out
}
