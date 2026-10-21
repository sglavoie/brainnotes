package index

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPackageLayering pins the one-way dependency that makes this package a
// leaf: it consumes a vault and produces []*vault.Note, and knows nothing about
// what the notes are for. Importing query, queryexpr, properties, schema,
// noteops, cockpit or web would make the cache a layer with opinions — the same
// mistake internal/properties avoids by deliberately not depending on
// internal/web. See doc.go and docs/internals/architecture.md.
//
// It scans non-test files only. parity_test.go imports queryexpr on purpose, to
// evaluate real queries against both load paths; that is a test-only edge and
// creates no import cycle, since nothing in those packages imports index.
func TestPackageLayering(t *testing.T) {
	const prefix = "github.com/sglavoie/brainnotes/internal/"

	allowed := map[string]bool{
		"vault":       true,
		"config":      true,
		"frontmatter": true,
	}

	files := parseImports(t, func(name string) bool {
		return !strings.HasSuffix(name, "_test.go")
	})

	internalImports := 0
	for name, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			internalImports++
			dep := strings.SplitN(strings.TrimPrefix(path, prefix), "/", 2)[0]
			if !allowed[dep] {
				t.Errorf("%s imports internal/%s, which is above the index layer", name, dep)
			}
		}
	}
	// Guard against the check silently passing because nothing was scanned.
	if internalImports == 0 {
		t.Error("no internal/ imports were inspected; the layering check is vacuous")
	}
}

// parseImports parses the import block of every Go file in the package
// directory, one file at a time. go/parser.ParseDir is deprecated, and this
// check never needed its grouping by package name anyway.
func parseImports(t *testing.T, keep func(name string) bool) map[string]*goast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package directory: %v", err)
	}
	fset := gotoken.NewFileSet()
	files := make(map[string]*goast.File)
	for _, name := range names {
		if !keep(name) {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, goparser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}
	if len(files) == 0 {
		t.Fatal("no Go files found in the package directory")
	}
	return files
}
