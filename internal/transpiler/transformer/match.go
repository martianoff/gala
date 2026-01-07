package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
)

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	// expression 'match' '{' caseClause+ '}'
	// Use children because it's not a distinct context type
	exprCtx := ctx.GetChild(0).(grammar.IExpressionContext)
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}

	paramName := "obj"
	if primary := exprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			paramName = p.Identifier().GetText()
		}
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, "")

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false

	// case clauses start from child 3 (0: expr, 1: match, 2: {, 3: case...)
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		// Check if it's a default case
		patCtx := ccCtx.Pattern()
		if patCtx.GetText() == "_" {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match")
			}
			foundDefault = true

			// Transform the body of default case
			if ccCtx.GetBodyBlock() != nil {
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
			} else if ccCtx.GetBody() != nil {
				expr, err := t.transformExpression(ccCtx.GetBody())
				if err != nil {
					return nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
			}
			continue
		}

		clause, err := t.transformCaseClause(ccCtx, paramName)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
	}

	if !foundDefault {
		return nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
	}

	t.needsStdImport = true
	// Transpile to IIFE: func(obj any) any { ... }(expr)
	var body []ast.Stmt
	// Add clauses as if-else chain
	var rootIf ast.Stmt
	var currentIf *ast.IfStmt

	for _, clause := range clauses {
		if rootIf == nil {
			rootIf = clause
			currentIf = findLeafIf(clause)
		} else {
			if currentIf != nil {
				currentIf.Else = clause
				currentIf = findLeafIf(clause)
			}
		}
	}

	if rootIf != nil {
		if len(defaultBody) > 0 {
			if currentIf != nil {
				currentIf.Else = &ast.BlockStmt{List: defaultBody}
			}
		}
		body = []ast.Stmt{rootIf}
	} else {
		body = defaultBody
	}

	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent(paramName)},
							Type:  ast.NewIdent("any"),
						},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: body,
			},
		},
		Args: []ast.Expr{expr},
	}, nil
}

func (t *galaASTTransformer) transformPattern(patCtx grammar.IPatternContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	if patCtx.GetText() == "_" {
		return ast.NewIdent("true"), nil, nil
	}

	switch ctx := patCtx.(type) {
	case *grammar.ExpressionPatternContext:
		return t.transformExpressionPattern(ctx.Expression(), objExpr)
	case *grammar.TypedPatternContext:
		return t.transformTypedPattern(ctx, objExpr)
	default:
		return nil, nil, fmt.Errorf("unknown pattern type: %T", patCtx)
	}
}

func (t *galaASTTransformer) transformExpressionPattern(patExprCtx grammar.IExpressionContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	if patExprCtx.GetText() == "_" {
		return ast.NewIdent("true"), nil, nil
	}

	// Simple Binding
	if primary := patExprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			name := p.Identifier().GetText()
			t.currentScope.vals[name] = false // Treat as var to avoid .Get() wrapping
			assign := &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(name)},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{objExpr},
			}
			return ast.NewIdent("true"), []ast.Stmt{assign}, nil
		}
	}

	// Extractor
	if patExprCtx.GetChildCount() >= 3 && patExprCtx.GetChild(1).(antlr.ParseTree).GetText() == "(" {
		extractorCtx := patExprCtx.GetChild(0).(grammar.IExpressionContext)
		patternExpr, err := t.transformExpression(extractorCtx)
		if err != nil {
			return nil, nil, err
		}

		var unapplyFun ast.Expr = t.stdIdent("UnapplyFull")

		// If it's a type name, use composite lit
		rawName := t.getBaseTypeName(patternExpr)
		typeName := t.getType(rawName)
		if typeName == "" {
			typeName = rawName
		}

		if _, ok := t.structFields[typeName]; ok {
			if typeName == "std.Tuple" || typeName == "Tuple" {
				unapplyFun = t.stdIdent("UnapplyTuple")
			} else {
				patternExpr = &ast.CompositeLit{Type: t.ident(rawName)}
			}
		}

		var argList *grammar.ArgumentListContext
		if ctx, ok := patExprCtx.GetChild(2).(*grammar.ArgumentListContext); ok {
			argList = ctx
		}

		resName := t.nextTempVar()
		okName := t.nextTempVar()

		// Only use resName if there are nested patterns that need it
		lhsRes := ast.NewIdent("_")
		if argList != nil && len(argList.AllArgument()) > 0 {
			hasNonUnderscore := false
			for _, argCtx := range argList.AllArgument() {
				if argCtx.(*grammar.ArgumentContext).Pattern().GetText() != "_" {
					hasNonUnderscore = true
					break
				}
			}
			if hasNonUnderscore {
				lhsRes = ast.NewIdent(resName)
			}
		}

		args := []ast.Expr{objExpr}
		// If it's a specialized Unapply, it only takes one arg (the object)
		// For UnapplyFull, it takes (obj, pattern)
		isUnapplyFull := false
		if sel, ok := unapplyFun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "UnapplyFull" {
				isUnapplyFull = true
			}
		}

		if isUnapplyFull && patternExpr != nil {
			args = append(args, patternExpr)
		}

		init := &ast.AssignStmt{
			Lhs: []ast.Expr{lhsRes, ast.NewIdent(okName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CallExpr{
					Fun:  unapplyFun,
					Args: args,
				},
			},
		}

		var allBindings []ast.Stmt
		allBindings = append(allBindings, init)

		var conds []ast.Expr
		conds = append(conds, ast.NewIdent(okName))

		// Handle arguments (nested patterns)
		if argList != nil {
			for i, argCtx := range argList.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)

				if arg.Pattern().GetText() == "_" {
					continue
				}

				valExpr := &ast.CallExpr{
					Fun: t.stdIdent("GetSafe"),
					Args: []ast.Expr{
						ast.NewIdent(resName),
						&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)},
					},
				}

				subCond, subBindings, err := t.transformPattern(arg.Pattern(), valExpr)
				if err != nil {
					return nil, nil, err
				}
				if subCond != nil {
					conds = append(conds, subCond)
				}
				allBindings = append(allBindings, subBindings...)
			}
		}

		var finalCond ast.Expr = conds[0]
		for i := 1; i < len(conds); i++ {
			finalCond = &ast.BinaryExpr{
				X:  finalCond,
				Op: token.LAND,
				Y:  conds[i],
			}
		}

		return finalCond, allBindings, nil
	}

	// Literal or other
	patExpr, err := t.transformExpression(patExprCtx)
	if err != nil {
		return nil, nil, err
	}
	cond := &ast.CallExpr{
		Fun: t.stdIdent("UnapplyCheck"),
		Args: []ast.Expr{
			objExpr,
			patExpr,
		},
	}
	return cond, nil, nil
}

func (t *galaASTTransformer) transformTypedPattern(ctx *grammar.TypedPatternContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	name := ctx.Identifier().GetText()
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, nil, err
	}

	typeName := t.getBaseTypeName(typeExpr)
	if qName := t.getType(typeName); qName != "" {
		typeName = qName
	}
	t.addVar(name, typeName)

	okName := t.nextTempVar()

	// v, ok := std.As[T](obj)
	asCall := &ast.CallExpr{
		Fun: &ast.IndexExpr{
			X:     t.stdIdent("As"),
			Index: typeExpr,
		},
		Args: []ast.Expr{objExpr},
	}

	assign := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name), ast.NewIdent(okName)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{asCall},
	}

	return ast.NewIdent(okName), []ast.Stmt{assign}, nil
}

func (t *galaASTTransformer) transformCaseClause(ctx *grammar.CaseClauseContext, paramName string) (ast.Stmt, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPattern(patCtx, ast.NewIdent(paramName))
	if err != nil {
		return nil, err
	}

	if ctx.GetGuard() != nil {
		guard, err := t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guard,
		}
	}

	var body []ast.Stmt
	if ctx.GetBodyBlock() != nil {
		b, err := t.transformBlock(ctx.GetBodyBlock().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b.List
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, err
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
	}

	bodyBlock := &ast.BlockStmt{List: body}

	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, nil
	}

	return ifStmt, nil
}
