package queryexpr

import (
	goparser "go/parser"
	gotoken "go/token"
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

	fset := gotoken.NewFileSet()
	pkgs, err := goparser.ParseDir(fset, ".", nil, goparser.ImportsOnly)
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
					t.Errorf("%s imports internal/%s, which is outside the queryexpr layer", name, dep)
				}
			}
		}
	}
	// Guard against the check silently passing because nothing was scanned.
	if internalImports == 0 {
		t.Error("no internal/ imports were inspected; the layering check is vacuous")
	}
}
