package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains struct construction and literal transformation logic extracted from expressions.go
// Functions: transformPrimary, transformCompositeLiteral, transformLiteral

func (t *galaASTTransformer) transformPrimary(ctx *grammar.PrimaryContext) (ast.Expr, error) {
	if ctx.Identifier() != nil {
		name := ctx.Identifier().GetText()
		ident := ast.NewIdent(name)

		// First check if it's a local variable - if so, don't try to resolve as std type
		if t.isVal(name) || t.isVar(name) {
			if t.isVal(name) {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ident,
						Sel: ast.NewIdent(transpiler.MethodGet),
					},
				}, nil
			}
			return ident, nil
		}

		// Check if this identifier is a std package type (not a variable with std type)
		// Only check typeMetas directly to see if std.name exists as a type definition
		// NOTE: Direct access is intentional here - we need exact match, not resolution
		if _, isStdType := t.typeMetas["std."+name]; isStdType {
			return t.stdIdent(name), nil
		}
		// Check if it's a std function (from metadata)
		resolvedFunc := t.getFunction(name)
		if resolvedFunc != nil && resolvedFunc.Package == registry.StdPackageName {
			return t.stdIdent(name), nil
		}
		// Check if it's a known std exported function (defined in Go, not GALA)
		if registry.IsStdFunction(name) {
			return t.stdIdent(name), nil
		}
		return ident, nil
	}
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	// Handle composite literal (e.g., map[K]V{}, struct{}{})
	if ctx.CompositeLiteral() != nil {
		return t.transformCompositeLiteral(ctx.CompositeLiteral().(*grammar.CompositeLiteralContext))
	}
	for i := 0; i < ctx.GetChildCount(); i++ {
		if exprListCtx, ok := ctx.GetChild(i).(grammar.IExpressionListContext); ok {
			exprs, err := t.transformExpressionList(exprListCtx.(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(exprs) == 1 {
				return &ast.ParenExpr{X: exprs[0]}, nil
			}
			// Multiple expressions in parentheses -> tuple literal syntax
			return t.transformTupleLiteral(exprs)
		}
	}
	return nil, nil
}

func (t *galaASTTransformer) transformCompositeLiteral(ctx *grammar.CompositeLiteralContext) (ast.Expr, error) {
	// Transform the type
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}

	// Reject slice literals - users should use collection_immutable.Array, collection_mutable.Array, or std.SliceOf() or std.SliceEmpty() for Go interop
	if _, isArray := typeExpr.(*ast.ArrayType); isArray {
		return nil, fmt.Errorf("slice literals are not supported in GALA; use collection_immutable.Array or collection_mutable.Array for type-safe maps, or std.SliceOf() or std.SliceEmpty() for Go interoperability")
	}

	// Reject map literals - users should use collection_immutable.HashMap, collection_mutable.HashMap or std.MapEmpty() for Go interop
	if _, isMap := typeExpr.(*ast.MapType); isMap {
		return nil, fmt.Errorf("map literals are not supported in GALA; use collection_immutable.HashMap or collection_mutable.HashMap for type-safe maps, or std.MapEmpty()/std.MapPut() for Go interoperability")
	}

	// Transform the elements
	var elts []ast.Expr
	if ctx.ElementList() != nil {
		elemList := ctx.ElementList().(*grammar.ElementListContext)
		for _, keyedElem := range elemList.AllKeyedElement() {
			kv := keyedElem.(*grammar.KeyedElementContext)
			exprs := kv.AllExpression()
			if len(exprs) == 2 {
				// Key-value pair
				key, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				value, err := t.transformExpression(exprs[1].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				elts = append(elts, &ast.KeyValueExpr{Key: key, Value: value})
			} else if len(exprs) == 1 {
				// Value only
				value, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				elts = append(elts, value)
			}
		}
	}

	return &ast.CompositeLit{
		Type: typeExpr,
		Elts: elts,
	}, nil
}

func (t *galaASTTransformer) transformLiteral(ctx *grammar.LiteralContext) (ast.Expr, error) {
	if ctx.INT_LIT() != nil {
		return &ast.BasicLit{Kind: token.INT, Value: ctx.INT_LIT().GetText()}, nil
	}
	if ctx.FLOAT_LIT() != nil {
		return &ast.BasicLit{Kind: token.FLOAT, Value: ctx.FLOAT_LIT().GetText()}, nil
	}
	if ctx.STRING() != nil {
		return &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}, nil
	}
	if ctx.GetText() == "true" || ctx.GetText() == "false" {
		return ast.NewIdent(ctx.GetText()), nil
	}
	if ctx.GetText() == "nil" {
		return ast.NewIdent("nil"), nil
	}
	return nil, nil
}
