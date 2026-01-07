package transformer

import (
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"strings"

	"github.com/antlr4-go/antlr/v4"
)

func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// Simplified type handling
	if ctx.Identifier() != nil {
		typeName := ctx.Identifier().GetText()
		if typeName == "_" {
			return ast.NewIdent("any"), nil
		}
		var ident ast.Expr = ast.NewIdent(typeName)
		if typeName == transpiler.TypeOption || typeName == transpiler.TypeTuple || typeName == transpiler.TypeEither {
			ident = t.stdIdent(typeName)
		}

		if ctx.TypeArguments() != nil {
			// Generic type: T[A, B] -> *ast.IndexExpr or *ast.IndexListExpr
			args := ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext).AllType_()
			var argExprs []ast.Expr
			for _, arg := range args {
				ae, err := t.transformType(arg)
				if err != nil {
					return nil, err
				}
				argExprs = append(argExprs, ae)
			}

			if len(argExprs) == 1 {
				return &ast.IndexExpr{X: ident, Index: argExprs[0]}, nil
			} else {
				return &ast.IndexListExpr{X: ident, Indices: argExprs}, nil
			}
		}
		return ident, nil
	}
	if ctx.GetChildCount() > 0 && ctx.GetChild(0).(antlr.ParseTree).GetText() == "*" {
		typ, err := t.transformType(ctx.GetChild(1).(grammar.ITypeContext))
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: typ}, nil
	}
	return ast.NewIdent(ctx.GetText()), nil
}

func (t *galaASTTransformer) getExprType(expr ast.Expr) ast.Expr {
	if expr == nil {
		return ast.NewIdent("any")
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return ast.NewIdent("bool")
		}
		typeName := t.getType(e.Name)
		if typeName != "" {
			return ast.NewIdent(typeName)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LOR, token.LAND, token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return ast.NewIdent("bool")
		default:
			return t.getExprType(e.X)
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return ast.NewIdent("bool")
		}
	}
	typeName := t.getExprTypeName(expr)
	if typeName != "" {
		if typeName == transpiler.TypeOption || typeName == transpiler.TypeImmutable || typeName == transpiler.TypeTuple || typeName == transpiler.TypeEither {
			return t.stdIdent(typeName)
		}
		return ast.NewIdent(typeName)
	}
	return ast.NewIdent("any")
}

func (t *galaASTTransformer) wrapWithAssertion(expr ast.Expr, targetType ast.Expr) ast.Expr {
	if targetType == nil {
		return expr
	}

	// Don't wrap if target type is 'any'
	if id, ok := targetType.(*ast.Ident); ok && id.Name == "any" {
		return expr
	}

	// If it's a CallExpr to a FuncLit (like match generates), or a Get_ call, we should assert
	if call, ok := expr.(*ast.CallExpr); ok {
		isFuncLit := false
		if _, ok := call.Fun.(*ast.FuncLit); ok {
			isFuncLit = true
		}

		isGetter := false
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if strings.HasPrefix(sel.Sel.Name, "Get_") {
				isGetter = true
			}
		}

		if isFuncLit || isGetter {
			return &ast.TypeAssertExpr{
				X:    expr,
				Type: targetType,
			}
		}
	}
	return expr
}

func (t *galaASTTransformer) extractTypeParams(typ ast.Expr) []*ast.Field {
	var params []*ast.Field
	switch e := typ.(type) {
	case *ast.IndexExpr:
		if id, ok := e.Index.(*ast.Ident); ok {
			params = append(params, &ast.Field{
				Names: []*ast.Ident{id},
				Type:  ast.NewIdent("any"),
			})
		}
	case *ast.IndexListExpr:
		for _, index := range e.Indices {
			if id, ok := index.(*ast.Ident); ok {
				params = append(params, &ast.Field{
					Names: []*ast.Ident{id},
					Type:  ast.NewIdent("any"),
				})
			}
		}
	}
	return params
}

func (t *galaASTTransformer) getBaseTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.ArrayType:
		return "[]" + t.getBaseTypeName(e.Elt)
	case *ast.IndexExpr:
		return t.getBaseTypeName(e.X)
	case *ast.IndexListExpr:
		return t.getBaseTypeName(e.X)
	case *ast.StarExpr:
		return t.getBaseTypeName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.FuncType:
		return "func"
	}
	return ""
}

func (t *galaASTTransformer) getExprTypeName(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return t.getType(e.Name)
	case *ast.IndexExpr:
		xTypeName := t.getExprTypeName(e.X)
		if strings.HasPrefix(xTypeName, "[]") {
			return xTypeName[2:]
		}
		return ""
	case *ast.SelectorExpr:
		xTypeName := t.getExprTypeName(e.X)
		if xTypeName != "" && t.structFieldTypes[xTypeName] != nil {
			if fType, ok := t.structFieldTypes[xTypeName][e.Sel.Name]; ok && fType != "" {
				return fType
			}
		}
	case *ast.CallExpr:
		// Handle b.Get() or std.Some()
		fun := e.Fun
		if idx, ok := fun.(*ast.IndexExpr); ok {
			fun = idx.X
		} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
			fun = idxList.X
		}

		if sel, ok := fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == transpiler.MethodGet {
				return t.getExprTypeName(sel.X)
			}

			xTypeName := t.getExprTypeName(sel.X)
			if xTypeName != "" {
				if typeMeta, ok := t.typeMetas[xTypeName]; ok {
					if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
						return methodMeta.ReturnType
					}
				}
			}

			if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight {
				return transpiler.TypeEither
			}
			if sel.Sel.Name == transpiler.TypeTuple {
				return transpiler.TypeTuple
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") {
				return transpiler.TypeEither
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") {
				return transpiler.TypeOption
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeTuple+"_") {
				return transpiler.TypeTuple
			}
			if _, ok := t.structFields[sel.Sel.Name]; ok {
				return sel.Sel.Name
			}
		}
		if id, ok := fun.(*ast.Ident); ok {
			if id.Name == transpiler.FuncLeft || id.Name == transpiler.FuncRight {
				return transpiler.TypeEither
			}
			if id.Name == transpiler.TypeTuple {
				return transpiler.TypeTuple
			}
			if strings.HasPrefix(id.Name, transpiler.TypeEither+"_") || strings.HasPrefix(id.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(id.Name, transpiler.FuncRight+"_") {
				return transpiler.TypeEither
			}
			if strings.HasPrefix(id.Name, transpiler.TypeOption+"_") || strings.HasPrefix(id.Name, transpiler.FuncSome+"_") || strings.HasPrefix(id.Name, transpiler.FuncNone+"_") {
				return transpiler.TypeOption
			}
			if strings.HasPrefix(id.Name, transpiler.TypeTuple+"_") {
				return transpiler.TypeTuple
			}
			if id.Name == "len" {
				return "int"
			}
			if _, ok := t.structFields[id.Name]; ok {
				return id.Name
			}
			if fMeta, ok := t.functions[id.Name]; ok {
				return fMeta.ReturnType
			}

			// Handle generic methods transformed to standalone functions: Receiver_Method
			if idx := strings.Index(id.Name, "_"); idx != -1 {
				receiverType := id.Name[:idx]
				methodName := id.Name[idx+1:]
				if meta, ok := t.typeMetas[receiverType]; ok {
					if mMeta, ok := meta.Methods[methodName]; ok {
						return mMeta.ReturnType
					}
				}
			}
		}
	case *ast.CompositeLit:
		return t.getBaseTypeName(e.Type)
	}
	return ""
}
