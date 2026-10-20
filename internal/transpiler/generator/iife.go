package generator

import (
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

// inlineReturnedIIFEs replaces `return func() R { body }()` with body itself,
// when R is the result type of the function the return is in.
//
// The transformer lowers an if-expression or a match to an immediately
// invoked function literal, and a function whose body is one of them returns
// that call. Go compiles every function literal as a function of its own,
// even when it inlines the call, so each such return costs a second function
// in the object file. In a method of a generic type the cost is paid once per
// instantiation of the type: OptionPtr's GetOrElse, OrElse, ToOption and
// String compiled two functions apiece for every element type a program puts
// in a collection.
//
// The body runs the same way inline. Its `return v` returned v from the
// literal, which the enclosing `return` returned in turn; it now returns v
// directly, converted to the same R. The literal has no parameters, so the
// body sees the same names, and its own declarations stay local to the block
// that replaces the return. A body is left alone when moving it would change
// what it means: a `defer` would run when the enclosing function returns
// instead of when the literal does, a label could clash with one of the
// enclosing function's, and a `recover()` could start stopping a panic.
//
// The two result types are compared as printed. The same text names the same
// type unless the function body declares a type of that name; then the
// inlined returns would fail to compile, not run differently.
//
// It mutates the AST in place, like parenthesizeControlClauseLits.
func inlineReturnedIIFEs(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch f := n.(type) {
		case *ast.FuncDecl:
			if f.Body != nil {
				inlineReturnsIn(f.Type, f.Body)
			}
		case *ast.FuncLit:
			inlineReturnsIn(f.Type, f.Body)
		}
		return true
	})
}

// inlineReturnsIn applies inlineReturnedIIFEs to the body of one function of
// type typ. It does not enter the function literals in body, which
// inlineReturnedIIFEs reaches on its own; a body it inlines is walked like the
// rest, so a return that body brings along is inlined too.
func inlineReturnsIn(typ *ast.FuncType, body *ast.BlockStmt) {
	resultType, ok := singleResult(typ)
	if !ok {
		return
	}
	// Printed on the first candidate: most functions have none.
	var result string
	printed := false
	inlined := func(stmt ast.Stmt) *ast.BlockStmt {
		lit, litResult := returnedIIFE(stmt)
		if lit == nil {
			return nil
		}
		if !printed {
			result, printed = typeText(resultType), true
		}
		if result == "" || typeText(litResult) != result || !inlinable(lit.Body) {
			return nil
		}
		return lit.Body
	}
	inlineAll := func(stmts []ast.Stmt) {
		for i, stmt := range stmts {
			if b := inlined(stmt); b != nil {
				stmts[i] = b
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BlockStmt:
			inlineAll(s.List)
		case *ast.CaseClause:
			inlineAll(s.Body)
		case *ast.CommClause:
			inlineAll(s.Body)
		case *ast.LabeledStmt:
			if b := inlined(s.Stmt); b != nil {
				s.Stmt = b
			}
		}
		return true
	})
}

// returnedIIFE returns the function literal stmt returns the call of, and the
// literal's result type R, when stmt is `return func() R { ... }()` with a
// single unnamed result R.
func returnedIIFE(stmt ast.Stmt) (*ast.FuncLit, ast.Expr) {
	ret, ok := stmt.(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil, nil
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return nil, nil
	}
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok || lit.Type.Params.NumFields() != 0 {
		return nil, nil
	}
	result, ok := singleResult(lit.Type)
	if !ok {
		return nil, nil
	}
	return lit, result
}

// singleResult returns the type of a function's one unnamed result.
func singleResult(typ *ast.FuncType) (ast.Expr, bool) {
	if typ.Results == nil || len(typ.Results.List) != 1 {
		return nil, false
	}
	field := typ.Results.List[0]
	if len(field.Names) != 0 {
		return nil, false
	}
	return field.Type, true
}

// inlinable reports whether body has no defer, label, goto or recover call of
// its own: what means something different once body is part of the enclosing
// function. Those of the function literals inside body belong to those
// literals and do not count.
func inlinable(body *ast.BlockStmt) bool {
	ok := true
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt, *ast.LabeledStmt:
			ok = false
		case *ast.BranchStmt:
			if s.Tok == token.GOTO || s.Label != nil {
				ok = false
			}
		case *ast.CallExpr:
			// recover stops a panic only when the deferred function calls it
			// directly, which the enclosing function may be.
			if id, isIdent := s.Fun.(*ast.Ident); isIdent && id.Name == "recover" {
				ok = false
			}
		}
		return ok
	})
	return ok
}

// typeText prints a type expression, for comparing two of them, or returns ""
// when it cannot. go/printer accepts the transformer's synthetic function
// types, whose parameter list may be nil, where types.ExprString does not.
func typeText(expr ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return b.String()
}
