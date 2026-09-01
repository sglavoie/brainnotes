package index

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// siblingTempPattern matches every place internal/vault names a temp file in a
// note's own directory. Custody files are the load-bearing one, but the staging
// and atomic-write temps live in the same namespace and are excluded from the
// walk for exactly the same reasons.
var siblingTempPattern = regexp.MustCompile(`openExclusiveTemp\(\s*dir\s*,\s*"([^"]*)"\s*,\s*"([^"]*)"`)

type siblingTemp struct {
	prefix string
	suffix string
	source string
}

// siblingTempNamings reads the naming out of internal/vault's source rather
// than hard-coding it here. That is the whole point of the custody test: a
// future rename of ".txn-custody-" must be caught, and a test carrying its own
// copy of the literal would keep passing against a name the code no longer
// uses.
func siblingTempNamings(t *testing.T) []siblingTemp {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join("..", "vault"))
	if err != nil {
		t.Fatalf("read internal/vault: %v", err)
	}
	var found []siblingTemp
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "vault", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range siblingTempPattern.FindAllStringSubmatch(string(data), -1) {
			found = append(found, siblingTemp{prefix: m[1], suffix: m[2], source: name})
		}
	}
	if len(found) == 0 {
		t.Fatal("found no sibling temp-file namings in internal/vault: the call shape this test scans for has moved, so it is no longer guarding anything — re-point siblingTempPattern")
	}
	return found
}

// TestVaultSiblingTempFilesAreInvisibleToTheIndex pins the assumption the cache
// silently rests on. `.txn-custody-*.tmp` files are created *next to notes*, in
// the vault proper, holding a complete and perfectly parseable note. Nothing in
// this package excludes them; they are invisible only because vault.Walk gates
// on a leading dot and on the `.md` extension. A custody rename that dropped
// both gates would poison the cache with a file that exists for microseconds.
func TestVaultSiblingTempFilesAreInvisibleToTheIndex(t *testing.T) {
	namings := siblingTempNamings(t)

	files := map[string]string{"Alpha.md": "---\nstatus: wip\n---\nalpha\n"}
	custodySeen := false
	for i, n := range namings {
		if strings.Contains(n.prefix, "custody") {
			custodySeen = true
		}
		hidden := strings.HasPrefix(n.prefix, ".")
		markdown := strings.EqualFold(n.suffix, ".md")
		if !hidden && markdown {
			t.Errorf("%s names sibling temp files %q…%q, which vault.Walk would visit: neither the hidden-file gate nor the .md gate excludes it, so the index would cache a file that is about to be renamed away", n.source, n.prefix, n.suffix)
		}
		files[fmt.Sprintf("%s%08x%s", n.prefix, i, n.suffix)] = "---\nstatus: wip\n---\nimposter\n"
	}
	if !custodySeen {
		t.Fatal("no custody naming found in internal/vault: takeCustody's prefix has changed and this test is no longer covering it")
	}

	v := testVault(t, files)
	ix, logs := newTestIndex(t, v)
	result := mustLoad(t, ix)

	want := observation{"Alpha.md": diskObservation(t, v)["Alpha.md"]}
	compareObservations(t, "writing one of every vault sibling temp file", "index", want, resultObservation(t, v.Root, result))
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %v, want none: a temp file the walk skips is not a failure", result.Failed)
	}
	if len(*logs) != 0 {
		t.Errorf("logged %v, want nothing", *logs)
	}
	// The cache itself, not just the returned notes: a temp file that reached
	// entries would survive into the state file and be served to the next
	// process after the real file it shadowed was gone.
	for rel := range ix.entries {
		if rel != "Alpha.md" {
			t.Errorf("cached entry %q: a vault temp file reached the on-disk cache", rel)
		}
	}
}

// TestCustodyExclusionDependsOnBothWalkGates says out loud which rule does the
// work, because either gate alone is enough today. A rename that kept only one
// of them would still pass a test that merely asserted "custody files are not
// indexed", so each gate is removed in turn and the fourth case — both gates
// dropped — is asserted positively as the shape that would poison the cache.
func TestCustodyExclusionDependsOnBothWalkGates(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		indexed bool
		why     string
	}{
		{"as named today", ".txn-custody-cafebabe.tmp", false, "hidden and not .md"},
		{"leading dot dropped", "txn-custody-cafebabe.tmp", false, "the .md extension gate alone"},
		{"suffix changed to .md", ".txn-custody-cafebabe.md", false, "the hidden-file gate alone"},
		{"both gates dropped", "txn-custody-cafebabe.md", true, "nothing excludes it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := testVault(t, map[string]string{
				"Alpha.md": "---\nstatus: wip\n---\nalpha\n",
				tc.file:    "---\nstatus: wip\n---\nimposter\n",
			})
			ix, _ := newTestIndex(t, v)

			indexed := false
			for _, rel := range relPaths(t, v.Root, mustLoad(t, ix).Notes) {
				if rel == tc.file {
					indexed = true
				}
			}
			if indexed != tc.indexed {
				t.Errorf("%q indexed = %v, want %v (%s)", tc.file, indexed, tc.indexed, tc.why)
			}
		})
	}
}

// TestCommittedTransactionLeavesNothingIndexable runs a real vault.FileTxn over
// an indexed note. The commit takes the destination into custody and places a
// replacement, so this exercises the actual sequence the test above only
// simulates, and requires the index to land on the committed revision with no
// residue in the cache.
func TestCommittedTransactionLeavesNothingIndexable(t *testing.T) {
	const before = "---\nstatus: wip\n---\nalpha\n"
	v := testVault(t, map[string]string{
		"Notes/Alpha.md": before,
		"Notes/Beta.md":  "---\nstatus: done\n---\nbeta\n",
	})
	backdate(t, v.Root, time.Minute)

	warm, _ := newTestIndex(t, v)
	assertEveryLoaderAgrees(t, "the initial load", v, warm)

	txn, err := vault.NewFileTxn(v.Root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	const after = "---\nstatus: done\n---\nalpha, committed\n"
	abs := filepath.Join(v.Root, "Notes", "Alpha.md")
	if err := txn.Write(abs, []byte(after), 0o644, vault.ExpectContent([]byte(before))); err != nil {
		t.Fatalf("plan write: %v", err)
	}
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("commit: %v", err)
	}

	assertEveryLoaderAgrees(t, "a committed FileTxn", v, warm)

	result := mustLoad(t, warm)
	got := resultObservation(t, v.Root, result)
	if got["Notes/Alpha.md"] != vault.HashBytes([]byte(after)) {
		t.Errorf("Alpha hash = %q, want the committed revision", got["Notes/Alpha.md"])
	}
	for rel := range warm.entries {
		if rel != "Notes/Alpha.md" && rel != "Notes/Beta.md" {
			t.Errorf("cached entry %q: the transaction left indexable residue", rel)
		}
	}
}
