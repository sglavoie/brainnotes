package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEndScenario_RelinkWorkflow(t *testing.T) {
	root := buildRelinkFixture(t)
	ref := filepath.Join(root, "Notes", "Ref.md")
	before := readNote(t, ref)

	beforeOut, err := runCLI(t, "", "find", "--broken-links", "--show-broken")
	if err != nil {
		t.Fatalf("find before relink: %v\n%s", err, beforeOut)
	}
	if !strings.Contains(beforeOut, "Notes/Ref.md:6: [[Projects\\Roadmap]]") {
		t.Fatalf("expected deterministic broken body link before relink, got:\n%s", beforeOut)
	}

	dryOut, err := runCLI(t, "", "relink", "--dry-run")
	if err != nil {
		t.Fatalf("relink --dry-run: %v\n%s", err, dryOut)
	}
	if !strings.Contains(dryOut, "--- Notes/Ref.md") {
		t.Fatalf("expected dry-run diff for Notes/Ref.md, got:\n%s", dryOut)
	}
	if afterDry := readNote(t, ref); afterDry != before {
		t.Fatalf("dry-run modified file:\n--- before ---\n%s--- after ---\n%s", before, afterDry)
	}

	applyOut, err := runCLI(t, "", "relink")
	if err != nil {
		t.Fatalf("relink apply: %v\n%s", err, applyOut)
	}
	if !strings.Contains(applyOut, "relinked 1 file(s)") {
		t.Fatalf("expected relink summary, got:\n%s", applyOut)
	}

	afterOut, err := runCLI(t, "", "find", "--broken-links", "--show-broken")
	if err != nil {
		t.Fatalf("find after relink: %v\n%s", err, afterOut)
	}
	if strings.Contains(afterOut, "[[Projects\\Roadmap]]") {
		t.Fatalf("deterministic broken link should be repaired, got:\n%s", afterOut)
	}
	if !strings.Contains(afterOut, "[[Missing\\Atlas]]") {
		t.Fatalf("ambiguous residue should remain for manual follow-up, got:\n%s", afterOut)
	}
}
