package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// functionBody finds one top-level function or method by name.
func functionBody(path, recv, name string) (*ast.BlockStmt, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		if receiverName(fn) != recv {
			continue
		}
		return fn.Body, nil
	}
	return nil, errNotFound{recv: recv, name: name}
}

type errNotFound struct{ recv, name string }

func (e errNotFound) Error() string {
	if e.recv == "" {
		return "no function named " + e.name + " in this file"
	}
	return "no method " + e.recv + "." + e.name + " in this file"
}

// receiverName reports a method's receiver type without its pointer star, and
// the empty string for a plain function. Generic receivers are unwrapped to
// their base type, which is the name a row would spell.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if index, ok := expr.(*ast.IndexExpr); ok {
		expr = index.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// bodyCallsAny reports whether the body contains a call to any of the given
// names, spelled either bare (`joinBarrier`) or qualified (`checkpoint.Join`).
func bodyCallsAny(body *ast.BlockStmt, names []string) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if wanted[callName(call.Fun)] {
			found = true
			return false
		}
		return true
	})
	return found
}

// callName renders a call target as a reader would spell it, and the empty
// string for anything more involved than an identifier or one selector.
func callName(expr ast.Expr) string {
	switch fun := expr.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok {
			return pkg.Name + "." + fun.Sel.Name
		}
	}
	return ""
}

// barrierCallSites reports the enclosing functions of every checkpoint join or
// hold call in one file, in barrierDoor.String() form.
func barrierCallSites(path, rel string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	wanted := append(append([]string(nil), joinsBarrier...), holdsBarrier...)

	var sites []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !bodyCallsAny(fn.Body, wanted) {
			continue
		}
		door := barrierDoor{file: rel, recv: receiverName(fn), fn: fn.Name.Name}
		sites = append(sites, door.String())
	}
	return sites, nil
}
