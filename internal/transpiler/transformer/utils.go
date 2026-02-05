package transformer

import (
	"fmt"
	"go/ast"
	"martianoff/gala/internal/transpiler/registry"
	"strings"
)

func (t *galaASTTransformer) nextTempVar() string {
	t.tempVarCount++
	return fmt.Sprintf("_tmp_%d", t.tempVarCount)
}

func (t *galaASTTransformer) isNoneCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return registry.Global.IsPreludePackage(x.Name) && sel.Sel.Name == "None"
}

func (t *galaASTTransformer) stdIdent(name string) ast.Expr {
	// If we're in the std package, no prefix needed
	if t.packageName == registry.StdPackageName {
		return ast.NewIdent(name)
	}
	// If std is dot-imported, no prefix needed
	if t.importManager.IsDotImported(registry.StdPackageName) {
		return ast.NewIdent(name)
	}
	// Otherwise, need the std. prefix and import
	t.needsStdImport = true
	return &ast.SelectorExpr{
		X:   ast.NewIdent(registry.StdPackageName),
		Sel: ast.NewIdent(name),
	}
}

func (t *galaASTTransformer) ident(name string) ast.Expr {
	if idx := strings.Index(name, "."); idx != -1 {
		pkg := name[:idx]
		base := name[idx+1:]
		if pkg == t.packageName {
			return ast.NewIdent(base)
		}
		// Check if it's dot-imported
		if t.importManager.IsDotImported(pkg) {
			return ast.NewIdent(base)
		}
		// Check if we have an alias for this actual package name
		if alias, ok := t.importManager.GetAlias(pkg); ok {
			pkg = alias
		}
		return &ast.SelectorExpr{
			X:   ast.NewIdent(pkg),
			Sel: ast.NewIdent(base),
		}
	}
	return ast.NewIdent(name)
}

// qualifyTypeExpr recursively transforms a type expression to ensure std types
// are properly qualified with the std. prefix. This is needed for type arguments
// in generic function calls like Unfold[int, Tuple[int, int]].
func (t *galaASTTransformer) qualifyTypeExpr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.Ident:
		// Check if this is a std type
		if registry.IsStdType(e.Name) {
			return t.stdIdent(e.Name)
		}
		return e
	case *ast.IndexExpr:
		// Generic type with single param: T[A]
		return &ast.IndexExpr{
			X:     t.qualifyTypeExpr(e.X),
			Index: t.qualifyTypeExpr(e.Index),
		}
	case *ast.IndexListExpr:
		// Generic type with multiple params: T[A, B]
		indices := make([]ast.Expr, len(e.Indices))
		for i, idx := range e.Indices {
			indices[i] = t.qualifyTypeExpr(idx)
		}
		return &ast.IndexListExpr{
			X:       t.qualifyTypeExpr(e.X),
			Indices: indices,
		}
	case *ast.StarExpr:
		// Pointer type: *T
		return &ast.StarExpr{X: t.qualifyTypeExpr(e.X)}
	case *ast.ArrayType:
		// Array/slice type: []T
		return &ast.ArrayType{
			Len: e.Len,
			Elt: t.qualifyTypeExpr(e.Elt),
		}
	case *ast.MapType:
		// Map type: map[K]V
		return &ast.MapType{
			Key:   t.qualifyTypeExpr(e.Key),
			Value: t.qualifyTypeExpr(e.Value),
		}
	case *ast.SelectorExpr:
		// Already qualified: pkg.Type
		return e
	case *ast.FuncType:
		// Function type: func(A) B
		return t.qualifyFuncType(e)
	default:
		return e
	}
}

// qualifyTypeExprs transforms a slice of type expressions
func (t *galaASTTransformer) qualifyTypeExprs(exprs []ast.Expr) []ast.Expr {
	result := make([]ast.Expr, len(exprs))
	for i, expr := range exprs {
		result[i] = t.qualifyTypeExpr(expr)
	}
	return result
}

// qualifyTypeArgsInExpr qualifies type arguments in an expression if it's an IndexExpr
// or IndexListExpr (e.g., Unfold[int, Tuple[int, int]] -> Unfold[int, std.Tuple[int, int]])
func (t *galaASTTransformer) qualifyTypeArgsInExpr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return &ast.IndexExpr{
			X:     t.qualifyTypeArgsInExpr(e.X),
			Index: t.qualifyTypeExpr(e.Index),
		}
	case *ast.IndexListExpr:
		return &ast.IndexListExpr{
			X:       t.qualifyTypeArgsInExpr(e.X),
			Indices: t.qualifyTypeExprs(e.Indices),
		}
	case *ast.SelectorExpr:
		// For selector expressions like stream.Of, don't qualify the base
		return e
	default:
		return expr
	}
}

// qualifyFuncType transforms function type to ensure std types are qualified
func (t *galaASTTransformer) qualifyFuncType(ft *ast.FuncType) *ast.FuncType {
	var params, results *ast.FieldList
	if ft.Params != nil {
		fields := make([]*ast.Field, len(ft.Params.List))
		for i, f := range ft.Params.List {
			fields[i] = &ast.Field{
				Names: f.Names,
				Type:  t.qualifyTypeExpr(f.Type),
			}
		}
		params = &ast.FieldList{List: fields}
	}
	if ft.Results != nil {
		fields := make([]*ast.Field, len(ft.Results.List))
		for i, f := range ft.Results.List {
			fields[i] = &ast.Field{
				Names: f.Names,
				Type:  t.qualifyTypeExpr(f.Type),
			}
		}
		results = &ast.FieldList{List: fields}
	}
	return &ast.FuncType{
		Params:  params,
		Results: results,
	}
}

func findLeafIf(stmt ast.Stmt) *ast.IfStmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return s
	case *ast.BlockStmt:
		if len(s.List) > 0 {
			return findLeafIf(s.List[len(s.List)-1])
		}
	}
	return nil
}
