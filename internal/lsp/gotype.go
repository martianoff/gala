package lsp

import (
	"fmt"
	"go/ast"
	"strings"
)

// extractTypesFromGoAST walks the transpiler's output Go AST and extracts
// concrete types for every variable declaration. Since the transpiler already
// resolved all types, we just read them from the AST — no re-type-checking needed.
func extractTypesFromGoAST(goFile *ast.File) map[string]string {
	types := make(map[string]string)
	if goFile == nil {
		return types
	}

	for _, decl := range goFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		walkStmtsForTypes(fn.Body.List, types)
	}

	return types
}

func walkStmtsForTypes(stmts []ast.Stmt, types map[string]string) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.DeclStmt:
			genDecl, ok := s.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range genDecl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					// Explicit type annotation in the Go AST
					if vs.Type != nil {
						types[name.Name] = cleanGoType(goExprString(vs.Type))
						continue
					}
					// Infer from the RHS expression's structure
					if i < len(vs.Values) {
						t := goExprType(vs.Values[i])
						if t != "" {
							types[name.Name] = t
						}
					}
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(s.Rhs) {
					t := goExprType(s.Rhs[i])
					if t != "" {
						types[id.Name] = t
					}
				}
			}
		case *ast.BlockStmt:
			walkStmtsForTypes(s.List, types)
		case *ast.IfStmt:
			if s.Body != nil {
				walkStmtsForTypes(s.Body.List, types)
			}
		case *ast.ForStmt:
			if s.Body != nil {
				walkStmtsForTypes(s.Body.List, types)
			}
		case *ast.RangeStmt:
			if s.Body != nil {
				walkStmtsForTypes(s.Body.List, types)
			}
		}
	}
}

// goExprType extracts a display type from a Go AST expression.
// The transpiler generates specific patterns we can recognize.
func goExprType(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		funcName := goExprString(e.Fun)
		// std.NewImmutable(inner) → unwrap and get inner type
		if funcName == "std.NewImmutable" && len(e.Args) > 0 {
			return goExprType(e.Args[0])
		}
		// std.NewConstPtr(inner) → unwrap
		if funcName == "std.NewConstPtr" && len(e.Args) > 0 {
			return goExprType(e.Args[0])
		}
		// Composite literal inside call: Type{fields}
		if len(e.Args) > 0 {
			if cl, ok := e.Args[0].(*ast.CompositeLit); ok {
				return cleanGoType(goExprString(cl.Type))
			}
		}
		// Method/function call: extract from function signature pattern
		// std.Option_Map(x, func) → look at the function name
		if strings.HasPrefix(funcName, "std.") {
			return inferFromStdFuncName(funcName)
		}
		return ""

	case *ast.CompositeLit:
		if e.Type != nil {
			return cleanGoType(goExprString(e.Type))
		}

	case *ast.BasicLit:
		switch {
		case strings.HasPrefix(e.Value, "\""):
			return "string"
		case strings.Contains(e.Value, "."):
			return "float64"
		default:
			return "int"
		}

	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return "bool"
		}
	}
	return ""
}

// inferFromStdFuncName extracts the parent type from std helper function names.
// The transpiler generates names like: std.Option_Map, std.Either_FlatMap, etc.
func inferFromStdFuncName(funcName string) string {
	// Remove "std." prefix
	name := funcName[4:]
	// Find the underscore separator: "Option_Map" → "Option"
	if idx := strings.Index(name, "_"); idx > 0 {
		return name[:idx]
	}
	return ""
}

// goExprString converts a Go AST expression to a readable string.
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
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "interface{}"
	}
	return fmt.Sprintf("%T", expr)
}

// cleanGoType converts Go type strings to GALA display names.
func cleanGoType(name string) string {
	name = strings.ReplaceAll(name, "std.", "")
	// Unwrap Immutable[T] → T
	for strings.HasPrefix(name, "Immutable[") && strings.HasSuffix(name, "]") {
		name = name[10 : len(name)-1]
	}
	return name
}
