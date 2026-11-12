package transformer

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
)

// Unused local bindings
//
// Go rejects a local variable nothing reads ("declared and not used"), so a
// local `val` or `var` GALA code never reads used to pass GALA's checks and
// fail in `go build`, reported against the generated Go. It is reported here
// instead, at the name in the GALA source, as an unused pattern binding of a
// match arm is (see lowerCaseClause).
//
// The check runs on the generated Go, after the whole file is lowered, so it
// judges exactly what Go will: a binding counts as read when its Go name
// appears anywhere after its declaration in the block that declares it. That
// over-counts reads (a later binding of the same name in a nested block, or an
// assignment, also counts), never under-counts them, so the check never
// rejects a program Go would accept.

// localBinding is a local val or var a declaration emits: its GALA keyword,
// its name and where the name is written, and whether it names a component
// of a tuple destructuring.
type localBinding struct {
	keyword, name string
	line, col     int
	component     bool
}

// declaredName is the Go name the declaration d, of keyword `val` or `var`,
// emits for its name id. A local one is recorded for checkUnusedBindings.
func (t *galaASTTransformer) declaredName(d bindingDecl, keyword string, id declName) *ast.Ident {
	ident := ast.NewIdent(id.text)
	if d.topLevel || id.text == "_" || !id.at.ok() {
		return ident
	}
	if t.localBindings == nil {
		t.localBindings = make(map[*ast.Ident]localBinding)
	}
	line, col := id.at.start()
	t.localBindings[ident] = localBinding{keyword: keyword, name: id.text, line: line, col: col, component: d.tuple.ok()}
	return ident
}

// checkUnusedBindings reports the first local val or var of file, in source
// order, that nothing reads.
func (t *galaASTTransformer) checkUnusedBindings(file *ast.File) error {
	if len(t.localBindings) == 0 {
		return nil
	}
	var unused []localBinding
	ast.Inspect(file, func(n ast.Node) bool {
		var list []ast.Stmt
		switch s := n.(type) {
		case *ast.BlockStmt:
			list = s.List
		case *ast.CaseClause:
			list = s.Body
		case *ast.CommClause:
			list = s.Body
		default:
			return true
		}
		for i, stmt := range list {
			unused = append(unused, t.unreadIn(stmt, list[i+1:])...)
		}
		return true
	})
	if len(unused) == 0 {
		return nil
	}
	b := slices.MinFunc(unused, func(a, b localBinding) int {
		return cmp.Or(cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col))
	})
	err := galaerr.NewSemanticErrorInFile(t.filePath, b.line, b.col,
		fmt.Sprintf("unused %s '%s': it is declared but never used", b.keyword, b.name))
	err.Hint = "use it, or remove the declaration; to run its initializer only for its effect, write the expression as a statement on its own"
	if b.component {
		err.Hint = "use it, or write `_` for a component you do not need"
	}
	return err.WithSpan(b.col + len([]rune(b.name)))
}

// unreadIn returns the tracked bindings stmt declares that neither a later
// spec of its declaration nor rest, the statements after it, mentions.
func (t *galaASTTransformer) unreadIn(stmt ast.Stmt, rest []ast.Stmt) []localBinding {
	decl, ok := stmt.(*ast.DeclStmt)
	if !ok {
		return nil
	}
	gen, ok := decl.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	var unread []localBinding
	for i, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, ident := range vs.Names {
			b, tracked := t.localBindings[ident]
			if !tracked || mentions(ident.Name, gen.Specs[i+1:]) || mentions(ident.Name, rest) {
				continue
			}
			unread = append(unread, b)
		}
	}
	return unread
}

// mentions reports whether an identifier spelled name appears in any of nodes.
func mentions[N ast.Node](name string, nodes []N) bool {
	found := false
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}
