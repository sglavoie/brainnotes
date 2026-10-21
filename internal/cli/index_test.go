package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/index"
)

// TestIndexStatus_NotBuilt pins the report for a vault that has never been
// indexed, and asserts the report is genuinely read-only: asking about the
// cache must not be the thing that creates it.
func TestIndexStatus_NotBuilt(t *testing.T) {
	root := buildSetFixture(t)

	out, err := runCLI(t, "", "index")
	if err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	assertGolden(t, "index_status_not_built.golden", out)

	if _, err := os.Stat(index.Dir(root)); !os.IsNotExist(err) {
		t.Fatalf("status built the index: stat err = %v", err)
	}

	explicit, err := runCLI(t, "", "index", "--status")
	if err != nil {
		t.Fatalf("index --status: %v\n%s", err, explicit)
	}
	if explicit != out {
		t.Fatalf("--status differs from the default:\n--- default ---\n%s--- --status ---\n%s", out, explicit)
	}
}

// TestIndexRebuild pins the rebuild summary and then checks the status report
// against the state file the rebuild actually wrote. The byte size and the
// write time cannot be a golden — the state file records the vault's absolute
// root — so those two lines are asserted structurally.
func TestIndexRebuild(t *testing.T) {
	root := buildSetFixture(t)

	out, err := runCLI(t, "", "index", "--rebuild")
	if err != nil {
		t.Fatalf("index --rebuild: %v\n%s", err, out)
	}
	assertGolden(t, "index_rebuild.golden", out)

	statePath := filepath.Join(index.Dir(root), "index.gob")
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state file: %v", err)
	}

	status, err := runCLI(t, "", "index", "--status")
	if err != nil {
		t.Fatalf("index --status: %v\n%s", err, status)
	}
	wantLines := []string{
		"State file:  .bnotes/index/index.gob",
		"Status:      healthy",
		"Entries:     5",
	}
	for _, want := range wantLines {
		if !strings.Contains(status, want+"\n") {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}
	if !strings.Contains(status, "Size:        "+strconv.FormatInt(info.Size(), 10)+" B\n") {
		t.Fatalf("status size does not match the state file (%d B):\n%s", info.Size(), status)
	}
	if !strings.Contains(status, "Last write:  ") {
		t.Fatalf("status has no write time:\n%s", status)
	}
}

// TestIndexStatus_UnusableStateFile checks the report a self-healing condition
// produces: the failure class is named, and the report says the next load fixes
// it rather than telling the user to do something.
func TestIndexStatus_UnusableStateFile(t *testing.T) {
	root := buildSetFixture(t)

	if out, err := runCLI(t, "", "index", "--rebuild"); err != nil {
		t.Fatalf("index --rebuild: %v\n%s", err, out)
	}
	statePath := filepath.Join(index.Dir(root), "index.gob")
	if err := os.WriteFile(statePath, []byte("not a gob stream"), 0o644); err != nil {
		t.Fatalf("corrupt state file: %v", err)
	}

	out, err := runCLI(t, "", "index", "--status")
	if err != nil {
		t.Fatalf("index --status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Status:      unusable (state file could not be decoded); the next load rebuilds it\n") {
		t.Fatalf("corrupt state file not reported:\n%s", out)
	}
	if strings.Contains(out, "Entries:") {
		t.Fatalf("an undecodable state file has no entry count to report:\n%s", out)
	}

	// A rebuild is the documented repair and must work from the corrupt state.
	rebuilt, err := runCLI(t, "", "index", "--rebuild")
	if err != nil {
		t.Fatalf("index --rebuild over corrupt state: %v\n%s", err, rebuilt)
	}
	assertGolden(t, "index_rebuild.golden", rebuilt)
}

// TestIndexStatusAndRebuildAreMutuallyExclusive pins the refusal: one asks what
// the cache is, the other replaces it, and combining them would make the report
// describe a file the same command had just written.
func TestIndexStatusAndRebuildAreMutuallyExclusive(t *testing.T) {
	buildSetFixture(t)

	out, err := runCLI(t, "", "index", "--status", "--rebuild")
	if err == nil {
		t.Fatalf("expected an error, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "status") || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("error does not name both flags: %v", err)
	}
}
