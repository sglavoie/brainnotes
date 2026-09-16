package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readCapabilitiesCSS reads the hand-maintained stylesheet. It is deliberately
// outside cssbuild's manifest, so nothing regenerates it and nothing else looks
// at it — which is exactly why the group a selector lands in needs a test.
func readCapabilitiesCSS(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("static", "capabilities.css"))
	if err != nil {
		t.Fatalf("read capabilities CSS: %v", err)
	}
	return string(body)
}

func capabilityRuleSelectors(t *testing.T, css, attribute string) string {
	t.Helper()
	marker := "html[" + attribute + "] :is("
	start := strings.Index(css, marker)
	if start == -1 {
		t.Fatalf("capabilities CSS has no %s rule", attribute)
	}
	rest := css[start+len(marker):]
	end := strings.Index(rest, ")")
	if end == -1 {
		t.Fatalf("%s rule is never closed", attribute)
	}
	return rest[:end]
}

// TestCapabilityStylesheetHidesEachControlUnderItsOwnDimension pins which of the
// two groups a control belongs to. The groups key on independent attributes
// because the capabilities are independent: read-only sets both, hosted sets only
// data-no-process-launch. Put a selector in the wrong group and a hosted vault
// either hides a control that works or offers one whose answer is a 403 — and
// nothing else in the tree would notice, since this file has no generator and no
// other reader.
func TestCapabilityStylesheetHidesEachControlUnderItsOwnDimension(t *testing.T) {
	css := readCapabilitiesCSS(t)
	writes := capabilityRuleSelectors(t, css, "data-read-only")
	launch := capabilityRuleSelectors(t, css, "data-no-process-launch")

	for _, tc := range []struct {
		selector string
		group    string
		why      string
	}{
		// The synthesis workbench creates a note. Everything on it but the
		// optional generation step works without an agent, so it is a write
		// control and a hosted vault keeps it.
		{"[data-synthesize-launch]", "writes", "the workbench writes a note and hosted may write"},
		{"[data-capture-root]", "writes", "capture is a write"},
		{"[data-related-action]", "writes", "related feedback writes the recall sidecar"},
		{"[data-fixes-fix-action]", "writes", "applying a fix writes a note"},
		// The note page's own Open would navigate a reader to the note they are
		// already on, so it goes with the process-launch group.
		{"[data-open-editor]", "launch", "opening the current note in an editor is a launch"},
		{"[data-open-button]", "launch", "opening the current note in an editor is a launch"},
		{"[data-note-insight]", "launch", "AI actions run the configured agent command"},
		{"[data-brief-action-insight]", "launch", "AI actions run the configured agent command"},
		{"[data-properties-config-open]", "launch", "opening the config file in an editor is a launch"},
	} {
		inWrites := strings.Contains(writes, tc.selector)
		inLaunch := strings.Contains(launch, tc.selector)
		wantWrites := tc.group == "writes"
		if inWrites != wantWrites {
			t.Errorf("%s in data-read-only group = %v, want %v (%s)", tc.selector, inWrites, wantWrites, tc.why)
		}
		if inLaunch == wantWrites {
			t.Errorf("%s in data-no-process-launch group = %v, want %v (%s)", tc.selector, inLaunch, !wantWrites, tc.why)
		}
	}

	// An Open that navigates somewhere else must never be hidden for want of a
	// process, which is what the process-launch group would be saying. Whether
	// it survives read-only is a separate question answered by its route: the
	// Brief's Open posts to /api/brief/item, which is on the replica allowlist,
	// so it appears in neither group; the fix queue's Open posts to /api/open,
	// which is not, so it is hidden in read-only along with the rest of the
	// queue's controls, because it carries data-fixes-button too — and kept in
	// hosted. This file can only read the stylesheet, so that second attribute
	// is pinned where it is rendered, by daily-fixes-dom.test.ts.
	for _, selector := range []string{"[data-brief-action-open]", "[data-fixes-open-key]"} {
		if strings.Contains(launch, selector) {
			t.Errorf("%s is hidden for want of a process launch, but it navigates", selector)
		}
	}
	if strings.Contains(writes, "[data-brief-action-open]") {
		t.Error("the Brief's Open is hidden in read-only, but /api/brief/item is on the replica allowlist")
	}
	if !strings.Contains(writes, "[data-fixes-button]") {
		t.Error("the fix queue's Open is offered in read-only, but /api/open answers it with a 403 there")
	}
}

// capabilityPageSources concatenates everything that can put a control on a
// page: the templates and the TypeScript that renders the rest of them.
//
// Three things are deliberately left out, because each one lets a dead selector
// look alive. The stylesheet parts under static_src/css mention every class they
// style, and a class that is only styled is not rendered. A *.test.ts file
// queries for controls it builds itself. And an import specifier is a file name:
// "./properties-schema-editor" is not the class .properties-schema-editor, and
// reading it as one is how a dead selector survived the first version of this
// test.
func capabilityPageSources(t *testing.T) string {
	t.Helper()
	var sources strings.Builder
	for _, dir := range []string{"templates", "static_src"} {
		walk := func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if filepath.Ext(path) != ".html" && !strings.HasSuffix(path, ".ts") {
				return nil
			}
			if strings.HasSuffix(path, ".test.ts") || strings.HasSuffix(path, ".test-helpers.ts") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sources.WriteString(withoutModuleSpecifiers(string(body)))
			sources.WriteString("\n")
			return nil
		}
		if err := filepath.WalkDir(dir, walk); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return sources.String()
}

// moduleSpecifierPattern matches the lines that name another module: the
// one-line `import { a } from "./b";`, the closing `} from "./b";` of a
// multi-line one, a bare `import "./b";`, and the re-exporting `export * from
// "./b";`. Anchoring to the whole line is safe because a line that ends in a
// module specifier contains nothing else.
var moduleSpecifierPattern = regexp.MustCompile(`(?m)^.*\bfrom\s+"[^"]*";?\s*$|^\s*import\s+"[^"]*";?\s*$`)

func withoutModuleSpecifiers(body string) string {
	return moduleSpecifierPattern.ReplaceAllString(body, "")
}

var capabilitySelectorPattern = regexp.MustCompile(`\[(data-[a-z0-9-]+)\]|(?:^|[\s,(])\.([a-z][a-z0-9-]*)`)

// TestCapabilityStylesheetHasNoSelectorThatMatchesNothing is the assertion this
// file most needs. A selector here is a plain string with no compiler, no
// bundler and no other reader: rename a control's attribute and the rule that
// hides it silently stops hiding anything, which looks exactly like a rule that
// works. A dead selector is a control offered in a mode that will refuse it.
func TestCapabilityStylesheetHasNoSelectorThatMatchesNothing(t *testing.T) {
	sources := capabilityPageSources(t)
	for _, match := range capabilitySelectorPattern.FindAllStringSubmatch(readCapabilitiesCSS(t), -1) {
		name := match[1] + match[2]
		// The two mode attributes are set as dataset properties in themeBoot
		// (readOnly, noProcessLaunch) and so never appear in kebab-case.
		if name == "data-read-only" || name == "data-no-process-launch" {
			continue
		}
		used := regexp.MustCompile(`(?:^|[^a-z0-9-])` + regexp.QuoteMeta(name) + `(?:[^a-z0-9-]|$)`)
		if !used.MatchString(sources) {
			t.Errorf("capabilities.css hides %q, which nothing renders", name)
		}
	}
}
