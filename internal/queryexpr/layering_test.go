package queryexpr

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPackageLayering pins the one-way dependency this package exists to
// preserve: queryexpr may reach down into query and the value/vault layers,
// but nothing above it. If it ever imported web, cli, noteops, cockpit,
// properties, or recall, the reason for splitting it out of internal/query
// would be gone — see doc.go and docs/internals/architecture.md.
func TestPackageLayering(t *testing.T) {
	const prefix = "github.com/sglavoie/sglavoie_brainnotes/internal/"

	allowed := map[string]bool{
		"query":       true,
		"config":      true,
		"schema":      true,
		"frontmatter": true,
		"vault":       true,
		"wikilink":    true,
		"textnorm":    true,
	}

	files := parseImports(t, func(string) bool { return true })

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
				t.Errorf("%s imports internal/%s, which is outside the queryexpr layer", name, dep)
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
