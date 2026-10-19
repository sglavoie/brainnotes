package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsyncd"
)

func useBnBuild(t *testing.T, id string) {
	t.Helper()
	previous := bnBuildID
	bnBuildID = func() string { return id }
	t.Cleanup(func() { bnBuildID = previous })
}

func writeCompanionRecords(t *testing.T, root, build string) {
	t.Helper()
	guard := filepath.Join(root, ".git", "brainnotes-safety", "guard.json")
	sync := vaultsyncd.RecordPath(root)
	for path, body := range map[string]string{
		guard: `{"version":1,"build":"` + build + `","pid":4242,"started_at":"2026-10-19T08:00:00Z"}`,
		sync:  `{"version":1,"build":"` + build + `","workers":{}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runDoctorSplit(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append([]string{"doctor"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	return out.String(), errOut.String()
}

func TestDoctorWarnsWhenACompanionRunsAnotherBuild(t *testing.T) {
	root, _ := buildDoctorFixture(t)
	writeCompanionRecords(t, root, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	useBnBuild(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	stdout, stderr := runDoctorSplit(t, "--json")
	for _, want := range []string{"bn-vault-guard (pid 4242", "bn-vault-sync last ran as build aaaaaaaaaaaa", "this bn is build bbbbbbbbbbbb"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	var queue map[string]any
	if err := json.Unmarshal([]byte(stdout), &queue); err != nil {
		t.Fatalf("the warning leaked into the JSON report: %v\n%s", err, stdout)
	}
}

func TestDoctorIsQuietWhenTheBuildsAgreeOrAreUnknown(t *testing.T) {
	root, _ := buildDoctorFixture(t)
	writeCompanionRecords(t, root, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, self := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "unknown"} {
		useBnBuild(t, self)
		if _, stderr := runDoctorSplit(t); stderr != "" {
			t.Fatalf("bn build %q warned: %s", self, stderr)
		}
	}
}
