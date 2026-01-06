package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"strings"
)

func (t *galaASTTransformer) transformSimpleStatement(ctx grammar.ISimpleStatementContext) (ast.Stmt, error) {
	if exprCtx := ctx.Expression(); exprCtx != nil {
		expr, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	}
	if assignCtx := ctx.Assignment(); assignCtx != nil {
		return t.transformAssignment(assignCtx.(*grammar.AssignmentContext))
	}
	if shortCtx := ctx.ShortVarDecl(); shortCtx != nil {
		return t.transformShortVarDecl(shortCtx.(*grammar.ShortVarDeclContext))
	}
	return nil, nil
}

func (t *galaASTTransformer) transformStatement(ctx *grammar.StatementContext) (ast.Stmt, error) {
	if declCtx := ctx.Declaration(); declCtx != nil {
		decl, stmt, err := t.transformDeclaration(declCtx)
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			return stmt, nil
		}
		if decl != nil {
			return &ast.DeclStmt{Decl: decl}, nil
		}
		return nil, nil
	}
	if retCtx := ctx.ReturnStatement(); retCtx != nil {
		var results []ast.Expr
		if retCtx.Expression() != nil {
			expr, err := t.transformExpression(retCtx.Expression())
			if err != nil {
				return nil, err
			}
			results = append(results, expr)
		}
		return &ast.ReturnStmt{Results: results}, nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformAssignment(ctx *grammar.AssignmentContext) (ast.Stmt, error) {
	lhsCtx := ctx.GetChild(0).(*grammar.ExpressionListContext)
	for _, exprCtx := range lhsCtx.AllExpression() {
		if p := exprCtx.Primary(); p != nil {
			pc := p.(*grammar.PrimaryContext)
			if pc.Identifier() != nil {
				name := pc.Identifier().GetText()
				if t.isVal(name) {
					return nil, galaerr.NewSemanticError(fmt.Sprintf("cannot assign to immutable variable %s", name))
				}
			}
		}
		if exprCtx.GetChildCount() == 3 && exprCtx.GetChild(1).(antlr.ParseTree).GetText() == "." {
			selName := exprCtx.GetChild(2).(antlr.ParseTree).GetText()
			xExpr, err := t.transformExpression(exprCtx.GetChild(0).(grammar.IExpressionContext))
			if err == nil {
				typeName := t.getExprTypeName(xExpr)
				baseTypeName := typeName
				if idx := strings.Index(typeName, "["); idx != -1 {
					baseTypeName = typeName[:idx]
				}
				if idx := strings.LastIndex(baseTypeName, "."); idx != -1 {
					baseTypeName = baseTypeName[idx+1:]
				}

				if fields, ok := t.structFields[baseTypeName]; ok {
					for i, f := range fields {
						if f == selName {
							if t.structImmutFields[baseTypeName][i] {
								return nil, galaerr.NewSemanticError(fmt.Sprintf("cannot assign to immutable field %s", selName))
							}
							break
						}
					}
				}
			}
		}
	}

	lhsExprs, err := t.transformExpressionList(lhsCtx)
	if err != nil {
		return nil, err
	}
	rhsExprs, err := t.transformExpressionList(ctx.GetChild(2).(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	op := ctx.GetChild(1).(antlr.TerminalNode).GetText()
	var tok token.Token
	switch op {
	case "=":
		tok = token.ASSIGN
	case "+=":
		tok = token.ADD_ASSIGN
	case "-=":
		tok = token.SUB_ASSIGN
	case "*=":
		tok = token.MUL_ASSIGN
	case "/=":
		tok = token.QUO_ASSIGN
	default:
		return nil, galaerr.NewSemanticError(fmt.Sprintf("unknown assignment operator: %s", op))
	}

	return &ast.AssignStmt{
		Lhs: lhsExprs,
		Tok: tok,
		Rhs: rhsExprs,
	}, nil
}

func (t *galaASTTransformer) transformShortVarDecl(ctx *grammar.ShortVarDeclContext) (ast.Stmt, error) {
	idsCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	rhsExprs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	lhs := make([]ast.Expr, 0)
	wrappedRhs := make([]ast.Expr, 0)
	for i, idCtx := range idsCtx {
		name := idCtx.GetText()
		typeName := t.getExprTypeName(rhsExprs[i])
		t.addVal(name, typeName)
		lhs = append(lhs, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = rhsExprs[i]
		} else {
			val = &ast.IndexExpr{X: rhsExprs[0], Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
		}

		if t.isNoneCall(val) {
			return nil, galaerr.NewSemanticError("variable assigned to None() must have an explicit type")
		}

		wrappedRhs = append(wrappedRhs, &ast.CallExpr{
			Fun:  t.stdIdent("NewImmutable"),
			Args: []ast.Expr{val},
		})
	}

	return &ast.AssignStmt{
		Lhs: lhs,
		Tok: token.DEFINE,
		Rhs: wrappedRhs,
	}, nil
}

func (t *galaASTTransformer) transformBlock(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	t.pushScope()
	defer t.popScope()
	block := &ast.BlockStmt{}
	for _, stmtCtx := range ctx.AllStatement() {
		stmt, err := t.transformStatement(stmtCtx.(*grammar.StatementContext))
		if err != nil {
			return nil, err
		}
		block.List = append(block.List, stmt)
	}
	return block, nil
}

func (t *galaASTTransformer) transformIfStatement(ctx *grammar.IfStatementContext) (ast.Stmt, error) {
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	body, err := t.transformBlock(ctx.Block(0).(*grammar.BlockContext))
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Cond: cond,
		Body: body,
	}

	if ctx.SimpleStatement() != nil {
		init, err := t.transformSimpleStatement(ctx.SimpleStatement().(*grammar.SimpleStatementContext))
		if err != nil {
			return nil, err
		}
		stmt.Init = init
	}

	if ctx.ELSE() != nil {
		if ctx.Block(1) != nil {
			elseBody, err := t.transformBlock(ctx.Block(1).(*grammar.BlockContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		} else if ctx.IfStatement() != nil {
			elseIf, err := t.transformIfStatement(ctx.IfStatement().(*grammar.IfStatementContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
		}
	}

	return stmt, nil
}
