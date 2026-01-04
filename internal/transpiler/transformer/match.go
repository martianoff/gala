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
		patExprCtx := ccCtx.Expression(0)
		if patExprCtx.GetText() == "_" {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match")
			}
			foundDefault = true

			// Transform the body of default case
			if ccCtx.Block() != nil {
				b, err := t.transformBlock(ccCtx.Block().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
			} else if len(ccCtx.AllExpression()) > 1 {
				expr, err := t.transformExpression(ccCtx.Expression(1))
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

func (t *galaASTTransformer) transformPattern(patCtx grammar.IExpressionContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	if patCtx.GetText() == "_" {
		return ast.NewIdent("true"), nil, nil
	}

	// Simple Binding
	if p, ok := patCtx.Primary().(*grammar.PrimaryContext); ok && p.Identifier() != nil {
		name := p.Identifier().GetText()
		t.currentScope.vals[name] = false // Treat as var to avoid .Get() wrapping
		assign := &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(name)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{objExpr},
		}
		return ast.NewIdent("true"), []ast.Stmt{assign}, nil
	}

	// Extractor
	if patCtx.GetChildCount() >= 3 && patCtx.GetChild(1).(antlr.ParseTree).GetText() == "(" {
		extractorCtx := patCtx.GetChild(0).(grammar.IExpressionContext)
		extName := extractorCtx.GetText()

		var unapplyFun ast.Expr = t.stdIdent("UnapplyFull")
		var patternExpr ast.Expr

		if extName == "Some" {
			unapplyFun = t.stdIdent("UnapplySome")
		} else if extName == "None" {
			unapplyFun = t.stdIdent("UnapplyNone")
		} else {
			var err error
			patternExpr, err = t.transformExpression(extractorCtx)
			if err != nil {
				return nil, nil, err
			}
			// If it's a type name, use composite lit
			if id, ok := patternExpr.(*ast.Ident); ok {
				if _, ok := t.structFields[id.Name]; ok {
					patternExpr = &ast.CompositeLit{Type: id}
				}
			}
		}

		var argList *grammar.ArgumentListContext
		if ctx, ok := patCtx.GetChild(2).(*grammar.ArgumentListContext); ok {
			argList = ctx
		}

		resName := t.nextTempVar()
		okName := t.nextTempVar()

		// Only use resName if there are nested patterns that need it
		lhsRes := ast.NewIdent("_")
		if argList != nil && len(argList.AllArgument()) > 0 {
			hasNonUnderscore := false
			for _, argCtx := range argList.AllArgument() {
				if argCtx.(*grammar.ArgumentContext).Expression().GetText() != "_" {
					hasNonUnderscore = true
					break
				}
			}
			if hasNonUnderscore {
				lhsRes = ast.NewIdent(resName)
			}
		}

		args := []ast.Expr{objExpr}
		if patternExpr != nil {
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

				if arg.Expression().GetText() == "_" {
					continue
				}

				valExpr := &ast.CallExpr{
					Fun: t.stdIdent("GetSafe"),
					Args: []ast.Expr{
						ast.NewIdent(resName),
						&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)},
					},
				}

				subCond, subBindings, err := t.transformPattern(arg.Expression(), valExpr)
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
	patExpr, err := t.transformExpression(patCtx)
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

func (t *galaASTTransformer) transformCaseClause(ctx *grammar.CaseClauseContext, paramName string) (ast.Stmt, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Expression(0)
	cond, bindings, err := t.transformPattern(patCtx, ast.NewIdent(paramName))
	if err != nil {
		return nil, err
	}

	var body []ast.Stmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b.List
	} else if len(ctx.AllExpression()) > 1 {
		expr, err := t.transformExpression(ctx.Expression(1))
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
