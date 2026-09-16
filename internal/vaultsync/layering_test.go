package vaultsync

import (
	goparser "go/parser"
	gotoken "go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// TestPackageLayering pins the boundary that makes this package a leaf: it
// decides what a package *is*, who a vault accepts one from, and what a receipt
// means, and it does that out of the protocol alone.
//
// The three allowed dependencies are the ones the decisions are actually made
// of — config for the vault marker and the enrollment path, vault for the
// mutation guard and the fsync primitives, textnorm for the collision key.
//
// What must stay out is the point. Importing vaultgit, journal, noteops or index
// would make "may this travel?" a question about what *this machine* happens to
// track, and the two replicas would then answer it differently the moment one of
// them gained a package the other did not have. The portable scope is required
// to be narrower than the content history's, and a narrowing expressed by
// calling the thing it narrows is not a narrowing — it is the same answer with
// an extra hop. See doc.go and docs/internals/architecture.md.
//
// It scans non-test files only. portable_test.go imports vaultgit on purpose, to
// assert the narrowing against the real scope rather than against a copy of it;
// that is a test-only edge and creates no cycle, since nothing in vaultgit
// imports vaultsync.
func TestPackageLayering(t *testing.T) {
	allowed := map[string]bool{
		"config":   true,
		"vault":    true,
		"textnorm": true,
	}

	deps := internalDeps(t)
	// Guard against the check silently passing because nothing was scanned.
	if len(deps) == 0 {
		t.Fatal("no internal/ imports were inspected; the layering check is vacuous")
	}
	for _, dep := range deps {
		if !allowed[dep.pkg] {
			t.Errorf("%s imports internal/%s, which this package must not depend on", dep.file, dep.pkg)
		}
	}
}

// internalDep is one import of a sibling internal package, with the file that
// took it so a failure names something to open.
type internalDep struct {
	file string
	pkg  string
}

// internalDeps returns every internal/ import in this package's non-test files.
func internalDeps(t *testing.T) []internalDep {
	t.Helper()
	const prefix = "github.com/sglavoie/sglavoie_brainnotes/internal/"

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

	var deps []internalDep
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
				deps = append(deps, internalDep{
					file: name,
					pkg:  strings.SplitN(strings.TrimPrefix(path, prefix), "/", 2)[0],
				})
			}
		}
	}
	return deps
}
