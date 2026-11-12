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
// judges what Go will: a binding counts as read when its Go name appears after
// its declaration in the block that declares it, anywhere but as the whole
// target of a plain assignment (`x = v`). That over-counts reads (a later
// binding of the same name in a nested block also counts, as does `x += 1`),
// never under-counts them, so the check never rejects a program Go accepts.

// localBinding is a local val or var a declaration emits: its GALA keyword,
// its name and where the name is written, and whether the declaration binds
// other names too (a tuple destructuring, or `val v, ok = ...`).
type localBinding struct {
	keyword, name string
	line, col     int
	oneOfSeveral  bool
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
	t.localBindings[ident] = localBinding{keyword: keyword, name: id.text, line: line, col: col, oneOfSeveral: len(d.names) > 1}
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
		switch s := n.(type) {
		case *ast.BlockStmt:
			unused = t.appendUnread(unused, s.List)
		case *ast.CaseClause:
			unused = t.appendUnread(unused, s.Body)
		case *ast.CommClause:
			unused = t.appendUnread(unused, s.Body)
		}
		return true
	})
	if len(unused) == 0 {
		return nil
	}
	b := slices.MinFunc(unused, func(a, b localBinding) int {
		return cmp.Or(cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col))
	})
	hint := "use it, or remove the declaration; to run its initializer only for its effect, write the expression as a statement on its own"
	if b.oneOfSeveral {
		hint = "use it, or write `_` in its place"
	}
	err := galaerr.NewSemanticErrorInFile(t.filePath, b.line, b.col,
		fmt.Sprintf("unused %s '%s': it is declared but never used", b.keyword, b.name))
	err.Code = galaerr.CodeUnusedBinding
	err.Hint = hint
	return err.WithSpan(b.col + len([]rune(b.name)))
}

// appendUnread appends to unused the tracked bindings the statements of list
// declare that nothing after their declaration reads. The list is walked
// once, from its end, gathering the names read after each statement; a list
// that declares none is not walked.
func (t *galaASTTransformer) appendUnread(unused []localBinding, list []ast.Stmt) []localBinding {
	first := slices.IndexFunc(list, func(s ast.Stmt) bool { return len(t.trackedSpecs(s)) > 0 })
	if first < 0 {
		return unused
	}
	read := make(map[string]bool)
	for i := len(list) - 1; i >= first; i-- {
		specs := t.trackedSpecs(list[i])
		if specs == nil {
			gatherReads(read, list[i])
			continue
		}
		// A spec's names are in scope from the next spec on.
		for j := len(specs) - 1; j >= 0; j-- {
			for _, ident := range specs[j].Names {
				if b, tracked := t.localBindings[ident]; tracked && !read[ident.Name] {
					unused = append(unused, b)
				}
			}
			gatherReads(read, specs[j])
		}
	}
	return unused
}

// trackedSpecs is the specs of stmt when it is a var declaration that
// declares a tracked binding, nil otherwise.
func (t *galaASTTransformer) trackedSpecs(stmt ast.Stmt) []*ast.ValueSpec {
	decl, ok := stmt.(*ast.DeclStmt)
	if !ok {
		return nil
	}
	gen, ok := decl.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	var specs []*ast.ValueSpec
	tracked := false
	for _, spec := range gen.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok {
			specs = append(specs, vs)
			tracked = tracked || slices.ContainsFunc(vs.Names, func(id *ast.Ident) bool { _, ok := t.localBindings[id]; return ok })
		}
	}
	if !tracked {
		return nil
	}
	return specs
}

// gatherReads adds to read the name of every identifier n mentions, except
// the whole target of a plain assignment, which Go does not count as a use.
func gatherReads(read map[string]bool, n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok != token.ASSIGN {
				return true
			}
			for _, lhs := range n.Lhs {
				if _, bare := lhs.(*ast.Ident); !bare {
					gatherReads(read, lhs)
				}
			}
			for _, rhs := range n.Rhs {
				gatherReads(read, rhs)
			}
			return false
		case *ast.Ident:
			read[n.Name] = true
		}
		return true
	})
}
