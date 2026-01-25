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
	return x.Name == "std" && sel.Sel.Name == "None"
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
