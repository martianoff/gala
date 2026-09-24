package generator

import "go/ast"

// parenthesizeControlClauseLits parenthesizes the composite literals that land
// in the header of a generated `if`, `for` or `switch`. Go's parser treats '{'
// there as the start of the block body, so `if d == RightToLeft{}.Apply()` does
// not parse while `if d == (RightToLeft{}).Apply()` does.
//
// The pass lives here, at the single point where an AST becomes Go source,
// rather than at each lowering site that builds a control statement. It is a
// fact about go/printer and go/parser, not about GALA: the transformer has many
// unrelated lowerings that put a user-written expression into an `if` — the
// if-expression IIFE, the tail-call loop, match arm guards — and each one
// reaching this shape by its own route is what made it a recurring bug.
//
// It mutates the AST in place. Both callers of Generate discard the AST
// afterwards, and the parens it adds are semantically inert.
func parenthesizeControlClauseLits(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.IfStmt:
			parenthesizeSimpleStmtCompositeLits(s.Init)
			s.Cond = parenthesizeCompositeLits(s.Cond)
		case *ast.ForStmt:
			parenthesizeSimpleStmtCompositeLits(s.Init)
			s.Cond = parenthesizeCompositeLits(s.Cond)
			parenthesizeSimpleStmtCompositeLits(s.Post)
		case *ast.RangeStmt:
			s.X = parenthesizeCompositeLits(s.X)
		case *ast.SwitchStmt:
			parenthesizeSimpleStmtCompositeLits(s.Init)
			s.Tag = parenthesizeCompositeLits(s.Tag)
		case *ast.TypeSwitchStmt:
			parenthesizeSimpleStmtCompositeLits(s.Init)
			parenthesizeSimpleStmtCompositeLits(s.Assign)
		}
		return true
	})
}

// parenthesizeSimpleStmtCompositeLits applies parenthesizeCompositeLits to the
// expressions of a simple statement used as a control-clause init or post
// clause, where the same '{' ambiguity applies.
func parenthesizeSimpleStmtCompositeLits(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i, rhs := range s.Rhs {
			s.Rhs[i] = parenthesizeCompositeLits(rhs)
		}
	case *ast.ExprStmt:
		s.X = parenthesizeCompositeLits(s.X)
	case *ast.IncDecStmt:
		s.X = parenthesizeCompositeLits(s.X)
	}
}

// parenthesizeCompositeLits wraps any ambiguous ast.CompositeLit found in an
// expression tree with ast.ParenExpr. Only a literal whose type is a name
// (`T{}`, `pkg.T{}`, `T[int]{}`) is ambiguous in a control clause; one written
// with an explicit type literal (`[]int{}`, `map[string]int{}`) already tells
// the parser that '{' opens the literal, so it is left alone.
func parenthesizeCompositeLits(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.CompositeLit:
		if !compositeLitTypeIsAmbiguous(e.Type) {
			return e
		}
		return &ast.ParenExpr{X: e}
	case *ast.CallExpr:
		// Only the callee needs it; the arguments sit inside parens, where the
		// parser is not looking for a block.
		e.Fun = parenthesizeCompositeLits(e.Fun)
		return e
	case *ast.SelectorExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.BinaryExpr:
		e.X = parenthesizeCompositeLits(e.X)
		e.Y = parenthesizeCompositeLits(e.Y)
		return e
	case *ast.UnaryExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.ParenExpr:
		// Already parenthesized: the ambiguity is resolved, and recursing
		// would add a second layer of parens on every pass.
		if _, ok := e.X.(*ast.CompositeLit); ok {
			return e
		}
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.IndexExpr:
		// Only the operand needs it; an index sits inside brackets, where the
		// parser is not looking for a block.
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.SliceExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.StarExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.TypeAssertExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	default:
		return expr
	}
}

// compositeLitTypeIsAmbiguous reports whether a composite literal of this type
// would be misread in a control clause. A named type — possibly qualified or
// instantiated — is indistinguishable from an operand followed by a block; an
// explicit array, slice, map or struct type is not. An elided type (nil) only
// occurs for a nested element, which is already inside braces.
func compositeLitTypeIsAmbiguous(typ ast.Expr) bool {
	switch t := typ.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return true
	case *ast.IndexExpr:
		return compositeLitTypeIsAmbiguous(t.X)
	case *ast.IndexListExpr:
		return compositeLitTypeIsAmbiguous(t.X)
	default:
		return false
	}
}
