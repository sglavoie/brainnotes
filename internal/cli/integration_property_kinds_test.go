package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
	"github.com/sglavoie/brainnotes/internal/properties"
	"github.com/sglavoie/brainnotes/internal/schema"
)

// propertyKindsConfig declares one property per supported kind plus a
// wikilink list, so a single vault can exercise the whole unified model.
const propertyKindsConfig = `[frontmatter.properties.status]
type     = "string"
required = true
allowed  = ["WIP", "Done"]

[frontmatter.properties.created]
type = "date"

[frontmatter.properties.reviewed]
type = "datetime"

[frontmatter.properties.rating]
type = "int"

[frontmatter.properties.score]
type = "float"

[frontmatter.properties.archived]
type = "bool"

[frontmatter.properties.parent]
type = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.categories]
type  = "list"
items = "wikilink"
`

// TestEndToEndScenario_EveryPropertyKindLintsAndRepairs walks a vault holding
// one note per supported kind through lint and repair. It pins that a
// conforming note is silent, that every kind's lossless string form is
// coerced, and that an undeclared property is never touched.
func TestEndToEndScenario_EveryPropertyKindLintsAndRepairs(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"conforming.md": "---\n" +
			"archived: false\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"created: 2025-01-01\n" +
			"parent: \"[[Home]]\"\n" +
			"rating: 4\n" +
			"reviewed: 2025-01-01T09:30:00Z\n" +
			"score: 3.5\n" +
			"status: WIP\n" +
			"tags:\n  - go\n" +
			"---\nConforming.\n",
		"coercible.md": "---\n" +
			"archived: \"true\"\n" +
			"created: \"2025-02-03\"\n" +
			"rating: \"7\"\n" +
			"reviewed: \"2025-02-03T18:45:00Z\"\n" +
			"score: \"1.25\"\n" +
			"status: WIP\n" +
			"---\nCoercible.\n",
		"undeclared.md": "---\n" +
			"anything: 2025-01-01\n" +
			"status: Done\n" +
			"---\nUndeclared property.\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	useVaultRoot(t, root, propertyKindsConfig)

	lintOut, lintErr := runCLI(t, "", "lint")
	if lintErr == nil {
		t.Fatalf("expected lint to report the string-typed values, got:\n%s", lintOut)
	}
	if strings.Contains(lintOut, "conforming.md") {
		t.Errorf("conforming.md should satisfy every definition, got:\n%s", lintOut)
	}
	if strings.Contains(lintOut, "undeclared.md") {
		t.Errorf("an undeclared property must not be linted, got:\n%s", lintOut)
	}

	undeclaredBefore := readNote(t, filepath.Join(root, "undeclared.md"))

	if out, err := runCLI(t, "", "repair"); err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "coercible.md"))
	for _, want := range []string{
		"archived: true",
		"created: 2025-02-03",
		"rating: 7",
		"reviewed: 2025-02-03T18:45:00Z",
		"score: 1.25",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q after repair, got:\n%s", want, got)
		}
	}

	if after := readNote(t, filepath.Join(root, "undeclared.md")); after != undeclaredBefore {
		t.Errorf("undeclared property was rewritten:\n--- before ---\n%s--- after ---\n%s", undeclaredBefore, after)
	}

	if out, lintErr := runCLI(t, "", "lint"); lintErr != nil {
		t.Fatalf("expected a clean lint after repair, got: %v\n%s", lintErr, out)
	}
}

func TestCLIRepairPreservesMidnightDatetimeSpelling(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	const source = "---\n" +
		"published: '2026-03-01T00:00:00Z'\n" +
		"reviewed: '2026-03-05'\n" +
		"timeline:\n" +
		"  - '2026-03-02T00:00:00Z'\n" +
		"  - '2026-03-02T00:00:00Z'\n" +
		"---\nBody.\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	const cfgSource = `[frontmatter.properties.published]
type = "datetime"

[frontmatter.properties.timeline]
type = "list"
items = "datetime"

[frontmatter.properties.reviewed]
type = "date"
`
	definitions := map[string]config.PropertyDefinition{
		"published": {Type: config.KindDatetime},
		"timeline":  {Type: config.KindList, Items: config.KindDatetime},
		"reviewed":  {Type: config.KindDate},
	}
	useVaultRoot(t, root, cfgSource)
	if out, err := runCLI(t, "", "repair"); err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repaired note: %v", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("reparse repaired note: %v", err)
	}
	published, _ := properties.ClassifyProperty(&doc.Front, "published")
	if published.Kind != config.KindDatetime {
		t.Fatalf("published kind = %q, want datetime", published.Kind)
	}
	timeline, _ := properties.ClassifyProperty(&doc.Front, "timeline")
	if len(timeline.Items) != 1 || timeline.Items[0].Kind != config.KindDatetime {
		t.Fatalf("timeline = %#v, want one datetime", timeline)
	}
	reviewed, _ := properties.ClassifyProperty(&doc.Front, "reviewed")
	if reviewed.Kind != config.KindDate {
		t.Fatalf("reviewed kind = %q, want date", reviewed.Kind)
	}
	if violations := schema.ValidateNote(&doc.Front, path, definitions); len(violations) != 0 {
		t.Fatalf("violations after repair = %#v", violations)
	}
	got := string(data)
	for _, want := range []string{
		"published: 2026-03-01T00:00:00Z",
		"- 2026-03-02T00:00:00Z",
		"reviewed: 2026-03-05\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repaired note missing %q:\n%s", want, got)
		}
	}
}

// TestEndToEndScenario_ListAndWikilinkKindsDriveSetBehavior pins that comma
// splitting and `[[ ]]` wrapping follow the property definition alone: a
// declared wikilink list wraps and splits, while an undeclared key stores the
// raw scalar.
func TestEndToEndScenario_ListAndWikilinkKindsDriveSetBehavior(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("---\nstatus: WIP\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	useVaultRoot(t, root, propertyKindsConfig)

	if out, err := runCLI(t, "", "set", notePath, "categories=Books,People", "parent=Home", "tags=a,b", "note=x,y"); err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}

	got := readNote(t, notePath)
	for _, want := range []string{"[[Books]]", "[[People]]", "[[Home]]", "- a", "- b", "note: x,y"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q, got:\n%s", want, got)
		}
	}

	if _, err := runCLI(t, "", "set", notePath, "status+=Done"); err == nil {
		t.Fatal("expected += to be rejected for a non-list property")
	}

	if out, err := runCLI(t, "", "set", notePath, "categories+=Films"); err != nil {
		t.Fatalf("categories+=: %v\n%s", err, out)
	}
	if got := readNote(t, notePath); !strings.Contains(got, "[[Films]]") {
		t.Errorf("expected appended value wrapped as [[Films]], got:\n%s", got)
	}
}
