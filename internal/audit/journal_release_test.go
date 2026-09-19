package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// journalHandoffMarker exempts a BeginOperation whose journal is handed to
// somebody else to release — a constructor that returns the run it opened, say.
// The text after it names who releases it, which is the whole point: the
// obligation moved, it did not vanish.
const journalHandoffMarker = "//bn:journal-handoff"

// beginOperationSite is one call to noteops.BeginOperation and the expression
// its result was assigned to.
type beginOperationSite struct {
	file   string
	line   int
	target string
}

// TestEveryBeginOperationDefersRelease closes the gap the barrier opened.
//
// BeginOperation joins the checkpoint barrier and hands the caller a hold that
// lasts until Release. Finalize deliberately does not release it: an operation
// is not over when its undo manifest is — the content-history snapshot comes
// after, and that snapshot is inside the window a checkpoint must not cut. So
// the hold belongs to the caller, and a caller that forgets it does not fail, it
// leaks: that vault's barrier can never be taken exclusively again, and the next
// backup or deployment snapshot drains until it times out with no indication of
// which command is responsible.
//
// A missing Release is therefore invisible in every ordinary test and fatal to
// the one thing the barrier exists for. This makes it a build failure at the
// call site instead.
//
// The check is syntactic and one-sided: it requires a `defer <x>.Release()` in
// the same function, matching the expression the journal was assigned to. It
// does not verify the defer is reachable or that the variable is not reassigned.
// It catches the mistake that actually happens — a new mutating command that
// copied everything except the one line with no visible effect.
//
// A constructor that opens a journal and returns it cannot defer anything, so it
// says so with a `//bn:journal-handoff <who releases it>` marker, in the same
// spirit and with the same limits as `//bn:direct-write`: it asks for a name,
// not for a proof.
func TestEveryBeginOperationDefersRelease(t *testing.T) {
	t.Parallel()

	missing, err := collectUnreleasedOperations(repositoryRoot(t))
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].file != missing[j].file {
			return missing[i].file < missing[j].file
		}
		return missing[i].line < missing[j].line
	})
	for _, site := range missing {
		t.Errorf("%s:%d takes a journal from BeginOperation without `defer %s.Release()`; the checkpoint barrier stays joined for the life of the process",
			site.file, site.line, site.target)
	}
}

// collectUnreleasedOperations walks every production Go file under root and
// gathers the BeginOperation calls whose journal is never released.
func collectUnreleasedOperations(root string) ([]beginOperationSite, error) {
	var missing []beginOperationSite
	err := walkProductionFiles(root, func(path, rel string) error {
		found, err := unreleasedOperations(path, rel)
		if err != nil {
			return err
		}
		missing = append(missing, found...)
		return nil
	})
	return missing, err
}

// unreleasedOperations reports the BeginOperation calls in one file whose
// enclosing function has no matching deferred Release.
func unreleasedOperations(path, rel string) ([]beginOperationSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// The declaration itself is not a call site, and neither is any other
	// BeginOperation in the repository: internal/vaultsync's Builder has a method
	// by the same name that returns a builder and holds nothing.
	inNoteops := file.Name.Name == "noteops"
	handedOff := markedLines(fset, file, journalHandoffMarker)

	var missing []beginOperationSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		released := releasedTargets(fn.Body)
		for _, site := range beginOperationSites(fset, rel, fn.Body, inNoteops) {
			if released[site.target] || handedOff[site.line] {
				continue
			}
			missing = append(missing, site)
		}
	}
	return missing, nil
}

// markedLines reports every line a marker covers: the lines of its own comment
// group, the line after it, and the whole body of a function whose doc comment
// carries it.
//
// gofmt treats `//bn:` as a directive and pins it to the last line of its group,
// so the prose explaining a handoff and the marker asserting it always end up in
// one block. Matching the block is what keeps the two together.
func markedLines(fset *token.FileSet, file *ast.File, marker string) map[int]bool {
	marked := map[int]bool{}
	for _, group := range file.Comments {
		if !carriesMarker(group, marker) {
			continue
		}
		for line := fset.Position(group.Pos()).Line; line <= fset.Position(group.End()).Line+1; line++ {
			marked[line] = true
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !carriesMarker(fn.Doc, marker) {
			continue
		}
		for line := fset.Position(fn.Pos()).Line; line <= fset.Position(fn.End()).Line; line++ {
			marked[line] = true
		}
	}
	return marked
}

// carriesMarker reports whether any line of the comment group is the marker.
func carriesMarker(group *ast.CommentGroup, marker string) bool {
	if group == nil {
		return false
	}
	for _, comment := range group.List {
		if strings.HasPrefix(strings.TrimSpace(comment.Text), marker) {
			return true
		}
	}
	return false
}

// beginOperationSites finds the assignments that take a journal.
//
// A call whose result is not assigned to a plain name or a field is reported
// with an empty target, which no defer can match — that is deliberate. A journal
// nobody named is a journal nobody can release.
func beginOperationSites(fset *token.FileSet, rel string, body *ast.BlockStmt, inNoteops bool) []beginOperationSite {
	var sites []beginOperationSite
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(assign.Rhs) != 1 || !isBeginOperationCall(assign.Rhs[0], inNoteops) {
			return true
		}
		target := ""
		if len(assign.Lhs) > 0 {
			target = callName(assign.Lhs[0])
		}
		sites = append(sites, beginOperationSite{
			file:   rel,
			line:   fset.Position(assign.Pos()).Line,
			target: target,
		})
		return true
	})
	return sites
}

// isBeginOperationCall matches `noteops.BeginOperation(...)`, and the bare
// `BeginOperation(...)` only inside internal/noteops itself.
func isBeginOperationCall(expr ast.Expr, inNoteops bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch callName(call.Fun) {
	case "noteops.BeginOperation":
		return true
	case "BeginOperation":
		return inNoteops
	default:
		return false
	}
}

// releasedTargets collects the expressions a function defers a Release on.
func releasedTargets(body *ast.BlockStmt) map[string]bool {
	released := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		deferred, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		selector, ok := deferred.Call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Release" {
			return true
		}
		if target := callName(selector.X); target != "" {
			released[target] = true
		}
		return true
	})
	return released
}
