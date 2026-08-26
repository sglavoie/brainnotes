package index

import (
	goparser "go/parser"
	gotoken "go/token"
	"io/fs"
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
	const prefix = "github.com/sglavoie/sglavoie_brainnotes/internal/"

	allowed := map[string]bool{
		"vault":       true,
		"config":      true,
		"frontmatter": true,
	}

	fset := gotoken.NewFileSet()
	pkgs, err := goparser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, goparser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse package directory: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no Go packages found in the package directory")
	}

	internalImports := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
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
	}
	// Guard against the check silently passing because nothing was scanned.
	if internalImports == 0 {
		t.Error("no internal/ imports were inspected; the layering check is vacuous")
	}
}
