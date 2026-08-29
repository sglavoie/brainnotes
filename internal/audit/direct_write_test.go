package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// directWriteMarker is the annotation that makes a direct filesystem write in
// production code legal. See the package doc for what belongs after it.
const directWriteMarker = "//bn:direct-write"

// approvedPackages are the repository-relative directories whose whole job is
// persistence, and which therefore hold the guarantees the annotation would
// otherwise have to point at. A package earns a place here by owning a safety
// property, not by writing to disk a lot.
var approvedPackages = map[string]string{
	"internal/vault":      "owns the storage-location policy, the locks, and the path-continuous replacement every other writer borrows",
	"internal/vaultgit":   "owns the content history; it stages exact paths and moves a ref, and never writes a note",
	"internal/vaultstate": "owns durable sidecar state and applies the same guard, replacement, and snapshot to it",
	"internal/journal":    "owns the undo manifests, which are what authorize committing a deletion at all",
	"internal/index":      "owns a regenerable cache under .bnotes/index that carries no user intent and is out of the history's scope",
	"internal/backup":     "owns archive creation and extraction; its restore is reached through noteops.RestoreBackup, which adds the preflight and the snapshot",
}

// directWriteFuncs are the package-level calls that put something on disk. The
// list is deliberately about *creating, replacing, and removing names*, not just
// about writing bytes: an empty folder a refused operation left behind is
// exactly the artifact the location policy exists to prevent.
var directWriteFuncs = map[string]map[string]bool{
	"os": {
		"WriteFile":  true,
		"Create":     true,
		"CreateTemp": true,
		"OpenFile":   true,
		"Rename":     true,
		"Remove":     true,
		"RemoveAll":  true,
		"Mkdir":      true,
		"MkdirAll":   true,
		"MkdirTemp":  true,
		"Chmod":      true,
		"Chown":      true,
		"Truncate":   true,
		"Symlink":    true,
		"Link":       true,
	},
	"ioutil": {
		"WriteFile": true,
		"TempFile":  true,
		"TempDir":   true,
	},
}

type directWrite struct {
	file string
	line int
	call string
}

// TestNoUnannotatedDirectWrites is the source-level half of the vault's safety
// contract.
//
// Every runtime guarantee here — the location refusal, the replacement that
// never vacates a name, the snapshot that must land before a change counts as
// done — is enforced by a layer a caller has to go through. None of that stops
// someone from calling os.WriteFile next to it, and a mutation that skips the
// layer skips all three at once, silently, forever. This test is what makes that
// a build failure instead of a discovery.
//
// It is deliberately syntactic. It cannot tell a safe write from an unsafe one,
// and it does not try: it requires that a human said, in one line, why the write
// is allowed. That is the whole mechanism, and its value is that it cannot be
// satisfied by accident.
func TestNoUnannotatedDirectWrites(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	var findings []directWrite

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if approvedDir(filepath.ToSlash(filepath.Dir(rel))) {
			return nil
		}
		found, err := unannotatedWrites(path, rel)
		if err != nil {
			return err
		}
		findings = append(findings, found...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	if len(findings) == 0 {
		return
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})
	var b strings.Builder
	b.WriteString("production code writes to the filesystem without going through a safety layer,\n")
	b.WriteString("and without saying why it is allowed to. Either route the write through\n")
	b.WriteString("noteops.BeginOperation, vault.WriteStateFile, or vaultstate.Save, or annotate it:\n\n")
	b.WriteString("    " + directWriteMarker + " <what already guarantees this write is safe>\n\n")
	b.WriteString("Unannotated direct writes:\n")
	for _, finding := range findings {
		b.WriteString("  " + finding.file + ":" + itoa(finding.line) + ": " + finding.call + "\n")
	}
	t.Fatal(b.String())
}

// unannotatedWrites reports the direct-write calls in one file that carry no
// marker, either on the line above or in the enclosing function's doc comment.
func unannotatedWrites(path, rel string) ([]directWrite, error) {
	return directWrites(path, rel, true)
}

// directWrites reports the direct-write calls in one file. honorMarkers drops
// the ones a human vouched for, which is what the repository-wide audit wants;
// passing false asks the harder question of whether a file writes to the
// filesystem at all, which is the only way to state that a particular surface
// has no escape hatch rather than an approved one.
func directWrites(path, rel string, honorMarkers bool) ([]directWrite, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// A marker marks its whole comment group, not just its own line. gofmt treats
	// //bn: as a directive and pins it to the last line of the group it is in, so
	// the prose explaining a write and the marker asserting it always end up in
	// one block; matching the block is what keeps the two together.
	markedLines := map[int]bool{}
	for _, group := range file.Comments {
		marked := false
		for _, comment := range group.List {
			if strings.HasPrefix(strings.TrimSpace(comment.Text), directWriteMarker) {
				marked = true
				break
			}
		}
		if !marked {
			continue
		}
		for line := fset.Position(group.Pos()).Line; line <= fset.Position(group.End()).Line; line++ {
			markedLines[line] = true
		}
	}
	markedFuncs := markedFunctionRanges(fset, file)

	var findings []directWrite
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := directWriteName(call)
		if !ok {
			return true
		}
		line := fset.Position(call.Pos()).Line
		if honorMarkers && (markedLines[line] || markedLines[line-1] || withinMarkedFunc(line, markedFuncs)) {
			return true
		}
		findings = append(findings, directWrite{file: rel, line: line, call: name})
		return true
	})
	return findings, nil
}

// directWriteName reports the qualified name of a direct-write call. It matches
// on the package identifier as written, which is what a reader sees; an aliased
// import would slip past, and that is an accepted limit of a syntactic check.
func directWriteName(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	funcs, ok := directWriteFuncs[pkg.Name]
	if !ok || !funcs[selector.Sel.Name] {
		return "", false
	}
	return pkg.Name + "." + selector.Sel.Name, true
}

type lineRange struct{ start, end int }

// markedFunctionRanges finds the functions whose doc comment carries the marker,
// which is how a function that exists to perform one guarded write annotates
// itself once instead of on every line.
func markedFunctionRanges(fset *token.FileSet, file *ast.File) []lineRange {
	var ranges []lineRange
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		marked := false
		for _, comment := range fn.Doc.List {
			if strings.HasPrefix(strings.TrimSpace(comment.Text), directWriteMarker) {
				marked = true
				break
			}
		}
		if !marked {
			continue
		}
		ranges = append(ranges, lineRange{
			start: fset.Position(fn.Pos()).Line,
			end:   fset.Position(fn.End()).Line,
		})
	}
	return ranges
}

func withinMarkedFunc(line int, ranges []lineRange) bool {
	for _, r := range ranges {
		if line >= r.start && line <= r.end {
			return true
		}
	}
	return false
}

func approvedDir(dir string) bool {
	for approved := range approvedPackages {
		if dir == approved || strings.HasPrefix(dir, approved+"/") {
			return true
		}
	}
	return false
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".codegraph", "node_modules", "testdata", "backups":
		return true
	}
	return false
}

// repositoryRoot walks up from this package until it finds the module file, so
// the audit covers the whole repository however the test was invoked.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("audit: go.mod not found above the test's working directory")
		}
		dir = parent
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 12)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

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
