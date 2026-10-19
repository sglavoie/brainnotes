package noteops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestSetFrontmatterScalarPreservesBodyAndJournals(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody stays.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := SetFrontmatter(
		root,
		"Source.md",
		mutations,
		&cfg.Frontmatter,
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-set", nil }),
		),
	)
	if err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	if result.Journal == nil || result.Journal.ID != "entry-frontmatter-set" {
		t.Fatalf("journal = %#v, want deterministic entry", result.Journal)
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	if got != "---\nstatus: done\ntitle: Source\n---\nBody stays.\n" {
		t.Fatalf("note bytes = %q", got)
	}
	rec := frontmatterRecordForPath(t, *result.Journal, "Source.md")
	if rec.Before.Kind != journal.KindFile || rec.After.Kind != journal.KindFile {
		t.Fatalf("journal record = %#v, want file -> file", rec)
	}
}

func TestSetFrontmatterListAddRemoveAndWikilinkWrapping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\ncategories:\n  - \"[[Books]]\"\ntags:\n  - foo\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"categories": {Type: config.KindList, Items: config.KindWikilink},
			"tags":       {Type: config.KindList, Items: config.KindString},
		},
	}}
	mutations, err := ParseFrontmatterMutations([]string{"tags+=bar", "tags-=foo", "categories+=People"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter)
	if err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	for _, want := range []string{"[[Books]]", "[[People]]", "- bar"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in note:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- foo") {
		t.Fatalf("removed list value still present:\n%s", got)
	}
	if !strings.HasSuffix(got, "---\nBody.\n") {
		t.Fatalf("body was not preserved:\n%s", got)
	}
}

func TestUnsetFrontmatterRemovesKeyAndSkipsNoOpJournal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: done\ntitle: Source\n---\nBody.\n")
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	result, err := UnsetFrontmatter(
		root,
		"Source.md",
		[]string{"status"},
		&config.FrontmatterConfig{},
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return fixed }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-unset", nil }),
		),
	)
	if err != nil {
		t.Fatalf("UnsetFrontmatter: %v", err)
	}
	if !result.Changed || result.Journal == nil {
		t.Fatalf("result = %#v, want changed with journal", result)
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	if strings.Contains(got, "status:") || !strings.Contains(got, "title: Source") || !strings.HasSuffix(got, "---\nBody.\n") {
		t.Fatalf("unexpected unset output:\n%s", got)
	}

	noOp, err := UnsetFrontmatter(
		root,
		"Source.md",
		[]string{"missing"},
		&config.FrontmatterConfig{},
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return fixed }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-noop", nil }),
		),
	)
	if err != nil {
		t.Fatalf("UnsetFrontmatter no-op: %v", err)
	}
	if noOp.Changed || noOp.Journal != nil {
		t.Fatalf("no-op result = %#v, want unchanged without journal", noOp)
	}
	if _, err := os.Stat(filepath.Join(JournalRoot(root), "entry-frontmatter-noop", "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("no-op journal manifest stat err = %v, want missing", err)
	}
}

func TestSetFrontmatterListMutationRejectsExistingScalar(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\ntags: lonely\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"tags": {Type: config.KindList, Items: config.KindString},
		},
	}}
	mutations, err := ParseFrontmatterMutations([]string{"tags+=foo"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	_, err = SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); !strings.Contains(got, "tags: lonely") {
		t.Fatalf("note changed after conflict:\n%s", got)
	}
}

func writeFrontmatterTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readFrontmatterTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}

func frontmatterRecordForPath(t *testing.T, entry journal.Entry, rel string) journal.FileRecord {
	t.Helper()
	for _, rec := range entry.Files {
		if rec.Path == rel {
			return rec
		}
	}
	t.Fatalf("entry missing record for %q: %#v", rel, entry.Files)
	return journal.FileRecord{}
}

// TestSetFrontmatterConflictsWhenTheNoteChangesUnderneath proves property
// writes go through the vault transaction: the note is loaded, then replaced by
// another writer, so the pinned content hash turns the write into a conflict
// instead of a silent overwrite of the other writer's bytes.
func TestSetFrontmatterConflictsWhenTheNoteChangesUnderneath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: draft\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	notePath := filepath.Join(root, "Source.md")
	concurrent := "---\nstatus: someone-else\n---\nBody.\n"
	_, err = ApplyFrontmatterMutations(root, notePath, mutations, &cfg.Frontmatter, testConcurrentWriter{
		path: notePath,
		body: concurrent,
	})
	if err == nil {
		t.Fatal("write should conflict with the concurrent modification")
	}
	if CodeOf(err) != CodeConflict {
		t.Fatalf("error code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); got != concurrent {
		t.Fatalf("concurrent writer's bytes must survive, got %q", got)
	}
}

// testConcurrentWriter replaces the note while the mutation is being journaled,
// which is the last moment before the transaction re-checks its precondition.
type testConcurrentWriter struct {
	path string
	body string
}

func (w testConcurrentWriter) Capture(path string) error {
	return os.WriteFile(w.path, []byte(w.body), 0o644)
}

func TestUnsetFrontmatterConflictsWhenTheNoteChangesUnderneath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}

	notePath := filepath.Join(root, "Source.md")
	concurrent := "---\nstatus: someone-else\ntitle: Source\n---\nBody.\n"
	_, err := ApplyFrontmatterUnset(root, notePath, []string{"status"}, &cfg.Frontmatter, testConcurrentWriter{
		path: notePath,
		body: concurrent,
	})
	if err == nil {
		t.Fatal("unset should conflict with the concurrent modification")
	}
	if CodeOf(err) != CodeConflict {
		t.Fatalf("error code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); got != concurrent {
		t.Fatalf("concurrent writer's bytes must survive, got %q", got)
	}
}

// TestSetFrontmatterConflictsWhenRequiredConfigChangedBeforeTheWrite proves the
// pin reaches the transaction: property semantics come from brainnotes.toml, so
// a write validated against one revision must be refused once another revision
// has replaced it.
func TestSetFrontmatterConflictsWhenRequiredConfigChangedBeforeTheWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	note := "---\nstatus: draft\n---\nBody.\n"
	writeFrontmatterTestFile(t, root, "Source.md", note)
	writeFrontmatterTestFile(t, root, "brainnotes.toml", "[frontmatter]\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	writeFrontmatterTestFile(t, root, "brainnotes.toml", "[frontmatter]\n[frontmatter.properties.status]\ntype = \"list\"\n")

	_, err = SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter,
		WithFrontmatterRequire(filepath.Join(root, "brainnotes.toml"), vault.ExpectContent([]byte("[frontmatter]\n"))))
	if err == nil {
		t.Fatal("write should conflict with the replaced configuration")
	}
	if CodeOf(err) != CodeConflict {
		t.Fatalf("error code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); got != note {
		t.Fatalf("note must be untouched, got %q", got)
	}
}

// The pin also holds at the commit boundary: a configuration replaced after the
// note was staged still stops the write.
func TestSetFrontmatterConflictsWhenRequiredConfigChangesDuringCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	note := "---\nstatus: draft\n---\nBody.\n"
	writeFrontmatterTestFile(t, root, "Source.md", note)
	writeFrontmatterTestFile(t, root, "brainnotes.toml", "[frontmatter]\n")
	configPath := filepath.Join(root, "brainnotes.toml")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	_, err = applyFrontmatterMutations(root, filepath.Join(root, "Source.md"), mutations, &cfg.Frontmatter,
		testConcurrentWriter{path: configPath, body: "[frontmatter]\nchanged = true\n"},
		[]fileRequirement{{path: configPath, expect: vault.ExpectContent([]byte("[frontmatter]\n"))}})
	if err == nil {
		t.Fatal("write should conflict with the configuration replaced during commit")
	}
	if CodeOf(err) != CodeConflict {
		t.Fatalf("error code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); got != note {
		t.Fatalf("note must be untouched, got %q", got)
	}
}

func TestSetFrontmatterPreservesFileMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: draft\n---\nBody.\n")
	notePath := filepath.Join(root, "Source.md")
	if err := os.Chmod(notePath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}
	if _, err := SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter); err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}
	info, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want 0600", got)
	}
	if !strings.Contains(readFrontmatterTestFile(t, root, "Source.md"), "status: done") {
		t.Fatal("mutation should still be applied")
	}
}

// A commit that could not put a displaced note back names the destination and
// the file holding its content. Replacing that with a generic message would
// leave the caller with no way to find their note.
func TestClassifyTxnErrorPlacementFailureSurfacesRecovery(t *testing.T) {
	t.Parallel()

	recovery := &vault.TxnRecoveryError{
		Path:         "/vault/Source.md",
		RecoveryPath: "/vault/.txn-custody-123.tmp",
		Displaced:    true,
		Err:          &vault.TxnConflictError{Path: "/vault/Source.md", Expected: "missing", Actual: "file"},
	}

	for name, err := range map[string]error{
		"placement failure": recovery,
		"placement failure during rollback": &vault.TxnRollbackError{
			Path:         "/vault/Source.md",
			Err:          recovery,
			Failed:       []string{"/vault/Other.md"},
			RollbackErrs: []error{errors.New("boom")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			classified := ClassifyTxnError("frontmatter: save note", err)
			if got := CodeOf(classified); got != CodeInternal {
				t.Fatalf("code = %q, want %q", got, CodeInternal)
			}
			message := MessageOf(classified)
			if !strings.Contains(message, recovery.Path) {
				t.Fatalf("message must name the destination, got %q", message)
			}
			if !strings.Contains(message, recovery.RecoveryPath) {
				t.Fatalf("message must name the recovery path, got %q", message)
			}
			if message == "frontmatter: save note" {
				t.Fatalf("the generic message must not replace the recovery guidance, got %q", message)
			}
		})
	}
}

func TestClassifyTxnErrorTreatsFailedRollbackOverConflictAsInternal(t *testing.T) {
	t.Parallel()

	err := &vault.TxnRollbackError{
		Path: "/vault/Source.md",
		Err: &vault.TxnConflictError{
			Path:     "/vault/Source.md",
			Expected: "old hash",
			Actual:   "new hash",
		},
		Failed:       []string{"/vault/Other.md"},
		RollbackErrs: []error{errors.New("permission denied")},
	}

	classified := ClassifyTxnError("frontmatter: save note", err)
	if got := CodeOf(classified); got != CodeInternal {
		t.Fatalf("code = %q, want %q", got, CodeInternal)
	}
	if got := HTTPStatusForError(classified); got != 500 {
		t.Fatalf("status = %d, want 500", got)
	}
	if message := MessageOf(classified); !strings.Contains(message, "inspect 'git diff' to recover") {
		t.Fatalf("message must preserve rollback recovery guidance, got %q", message)
	}
}

// A volume whose replacement semantics could not be proved, and a Windows
// replacement that stopped half-way, both carry the only sentence that tells a
// person what happened and what to do about it. Neither is a conflict — retrying
// an unprovable volume just fails again — so both must arrive as an internal
// error that kept its own message.
func TestClassifyTxnErrorSurfacesReplacementRefusals(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		err  error
		must []string
	}{
		"a volume that cannot be proved safe": {
			err: &vault.UnsafeReplacementError{
				Path:   "/vault/Source.md",
				Reason: "renameatx_np failed on this volume",
				Err:    errors.New("operation not supported"),
			},
			must: []string{"/vault/Source.md", "operation not supported"},
		},
		"a replacement that stopped half-way": {
			err: &vault.PartialReplacementError{
				Path:       "/vault/Source.md",
				Stage:      "ReplaceFileW",
				BackupPath: "/vault/.txn-backup-123.tmp",
				Err:        errors.New("sharing violation"),
			},
			must: []string{"/vault/Source.md", "/vault/.txn-backup-123.tmp"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			classified := ClassifyTxnError("frontmatter: save note", testCase.err)
			if got := CodeOf(classified); got != CodeInternal {
				t.Fatalf("code = %q, want %q", got, CodeInternal)
			}
			message := MessageOf(classified)
			for _, want := range testCase.must {
				if !strings.Contains(message, want) {
					t.Fatalf("message must name %q, got %q", want, message)
				}
			}
			if !errors.Is(classified, testCase.err) {
				t.Fatalf("the classified error must still unwrap to the cause, got %v", classified)
			}
		})
	}
}
