package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configMutationFiles are the sources behind `bn config set` and
// `bn config unset`, the only CLI surface that rewrites the vault's own
// configuration file.
var configMutationFiles = []string{
	"internal/cli/config.go",
	"internal/cli/config_scriptable.go",
}

// TestConfigMutationHasNoDirectWriteEscape closes the one door the
// repository-wide audit deliberately leaves open.
//
// TestNoUnannotatedDirectWrites accepts a `//bn:direct-write` marker anywhere
// outside the persistence packages: it asks for a reason, not for a particular
// answer. That is the right rule in general and the wrong one here.
// brainnotes.toml is the file the vault root is defined by and the file a
// running watcher re-reads, so a "just write it atomically" shortcut in these
// commands would bypass the expected-hash precondition that turns a concurrent
// edit into a refusal, the undo entry, and the immediate history commit — all
// while passing the audit with one line of prose.
//
// So this asserts the stronger property: these files call nothing that puts
// anything on disk. Every byte they change goes through the vault transaction.
func TestConfigMutationHasNoDirectWriteEscape(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, rel := range configMutationFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			// A rename would otherwise leave this test passing over nothing.
			t.Errorf("%s does not exist; update configMutationFiles: %v", rel, err)
			continue
		}
		found, err := directWrites(path, rel, false)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			continue
		}
		for _, finding := range found {
			t.Errorf("%s:%d calls %s; the config mutation path must write only through vault.FileTxn",
				finding.file, finding.line, finding.call)
		}
	}
}

// sourceSaveFiles are the sources behind a raw-Markdown save: the read half and
// its validation, the save itself, and the HTTP routes that reach it.
//
// internal/noteops/source_ledger.go is deliberately absent, and
// TestSourceSaveLedgerWritesOnlyRemovals is why: it holds the one direct write
// on this surface, and stating that exclusion as its own assertion is what keeps
// the absence from becoming a place to put a second one.
var sourceSaveFiles = []string{
	"internal/noteops/source.go",
	"internal/noteops/source_save.go",
	"internal/web/source_api.go",
}

// TestSourceSaveHasNoDirectWriteEscape closes the same door for whole-note
// Markdown writes that TestConfigMutationHasNoDirectWriteEscape closes for the
// configuration file.
//
// A save replaces a note's entire source, so it is the one mutation whose
// shortcut costs the most: the bytes a person is looking at are the only copy of
// their edit, and a direct write would bypass the expected-hash precondition
// that turns a concurrent editor into a refusal, the undo entry that makes the
// previous version recoverable, and the history commit that records it. Each of
// those is what lets this surface answer "your edit was not lost" truthfully,
// and all three are skipped at once by a single os.WriteFile.
//
// So these files are held to the stronger property: they call nothing that puts
// anything on disk. Every byte reaches the vault through vault.FileTxn.
func TestSourceSaveHasNoDirectWriteEscape(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, rel := range sourceSaveFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			// A rename would otherwise leave this test passing over nothing.
			t.Errorf("%s does not exist; update sourceSaveFiles: %v", rel, err)
			continue
		}
		found, err := directWrites(path, rel, false)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			continue
		}
		for _, finding := range found {
			t.Errorf("%s:%d calls %s; a source save must write only through vault.FileTxn",
				finding.file, finding.line, finding.call)
		}
	}
}

// TestSourceSaveLedgerWritesOnlyRemovals bounds the one exclusion the list above
// takes.
//
// The idempotency ledger prunes its own expired records, and that removal cannot
// go through the transaction: a path inside it would join the lock set and the
// undo entry, and two unrelated saves choosing the same expired victim would
// then conflict over a file neither of them owns. So one os.Remove is annotated
// and allowed.
//
// What must not happen is that exclusion quietly growing. A record is written by
// the same transaction that writes the note — that is what makes its existence
// proof the bytes landed — and a direct write here would break that link while
// still looking like the pruning this test exists to permit. Removal is the only
// verb this file gets.
func TestSourceSaveLedgerWritesOnlyRemovals(t *testing.T) {
	t.Parallel()

	const rel = "internal/noteops/source_ledger.go"
	root := repositoryRoot(t)
	path := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s does not exist; update TestSourceSaveLedgerWritesOnlyRemovals: %v", rel, err)
	}
	found, err := directWrites(path, rel, false)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	for _, finding := range found {
		if finding.call == "os.Remove" {
			continue
		}
		t.Errorf("%s:%d calls %s; only os.Remove may bypass the transaction here, and only to prune an expired record",
			finding.file, finding.line, finding.call)
	}
}

// TestApprovedPackagesExist keeps the allowlist honest. A package that was
// renamed or removed would otherwise leave a permanent hole in the audit that
// nothing points at.
func TestApprovedPackagesExist(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for pkg, reason := range approvedPackages {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("approved package %q has no reason", pkg)
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg)))
		if err != nil {
			t.Errorf("approved package %q does not exist: %v", pkg, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("approved package %q is not a directory", pkg)
		}
	}
}
