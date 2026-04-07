package lsp

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"strings"
)

// extractTypesFromGoAST type-checks the transpiler's output Go AST and extracts
// concrete types for every variable declaration using go/types.
//
// The transpiler already resolved all GALA types, but the Go AST nodes don't
// carry type info directly — go/types resolves it from the fully-typed Go code.
func extractTypesFromGoAST(fset *token.FileSet, goFile *ast.File) map[string]string {
	result := make(map[string]string)
	if goFile == nil {
		return result
	}

	if fset == nil {
		fset = token.NewFileSet()
	}

	// Type-check the Go AST
	conf := types.Config{
		Importer: importer.Default(),
		Error:    func(err error) {}, // Ignore errors — best effort
	}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	conf.Check("main", fset, []*ast.File{goFile}, info)

	// Extract variable types from the type checker's results
	for ident, obj := range info.Defs {
		if obj == nil {
			continue
		}
		v, ok := obj.(*types.Var)
		if !ok {
			continue
		}
		typStr := cleanGoTypeForDisplay(v.Type().String())
		if typStr != "" {
			result[ident.Name] = typStr
		}
	}

	return result
}

// cleanGoTypeForDisplay converts Go type strings to GALA display names.
func cleanGoTypeForDisplay(typeStr string) string {
	result := typeStr

	// Remove full import paths: martianoff/gala/std.Option → Option
	for strings.Contains(result, "/") {
		idx := strings.Index(result, "/")
		start := idx
		for start > 0 && result[start-1] != '[' && result[start-1] != ',' && result[start-1] != '(' && result[start-1] != ' ' {
			start--
		}
		dotIdx := strings.Index(result[idx:], ".")
		if dotIdx < 0 {
			break
		}
		dotIdx += idx
		result = result[:start] + result[dotIdx+1:]
	}

	// Remove "std." if still present
	result = strings.ReplaceAll(result, "std.", "")

	// Unwrap Immutable[T] → T
	for strings.HasPrefix(result, "Immutable[") && strings.HasSuffix(result, "]") {
		result = result[10 : len(result)-1]
	}

	return result
}

// goExprString converts a Go AST expression to a string (for debug/display).
func goExprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return goExprString(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return goExprString(e.X) + "[" + goExprString(e.Index) + "]"
	case *ast.IndexListExpr:
		var indices []string
		for _, idx := range e.Indices {
			indices = append(indices, goExprString(idx))
		}
		return goExprString(e.X) + "[" + strings.Join(indices, ", ") + "]"
	case *ast.ArrayType:
		return "[]" + goExprString(e.Elt)
	case *ast.StarExpr:
		return "*" + goExprString(e.X)
	case *ast.MapType:
		return "map[" + goExprString(e.Key) + "]" + goExprString(e.Value)
	}
	return fmt.Sprintf("%T", expr)
}
