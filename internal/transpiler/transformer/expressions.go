package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

func (t *galaASTTransformer) transformCallExpr(ctx *grammar.ExpressionContext) (ast.Expr, error) {
	// expression '(' argumentList? ')'
	child1 := ctx.GetChild(0)
	x, err := t.transformExpression(child1.(grammar.IExpressionContext))
	if err != nil {
		return nil, err
	}

	var args []ast.Expr
	var namedArgs map[string]ast.Expr
	if ctx.GetChildCount() >= 3 {
		if argListCtx, ok := ctx.GetChild(2).(*grammar.ArgumentListContext); ok {
			// Handle Copy method call with overrides
			if sel, ok := x.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
				return t.transformCopyCall(sel.X, argListCtx)
			}

			// Handle generic method calls or monadic methods: o.Map[T](f) -> Map[T](o, f)
			var receiver ast.Expr
			var method string
			var typeArgs []ast.Expr

			if sel, ok := x.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
					// Not a method call
				} else {
					receiver = sel.X
					method = sel.Sel.Name
				}
			} else if idx, ok := x.(*ast.IndexExpr); ok {
				if sel, ok := idx.X.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
						// Not a method call
					} else {
						receiver = sel.X
						method = sel.Sel.Name
						typeArgs = []ast.Expr{idx.Index}
					}
				}
			} else if idxList, ok := x.(*ast.IndexListExpr); ok {
				if sel, ok := idxList.X.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
						// Not a method call
					} else {
						receiver = sel.X
						method = sel.Sel.Name
						typeArgs = idxList.Indices
					}
				}
			}

			recvTypeName := t.getExprTypeName(receiver)
			isGenericMethod := len(typeArgs) > 0 || (recvTypeName != "" && t.genericMethods[recvTypeName] != nil && t.genericMethods[recvTypeName][method])

			if receiver != nil && isGenericMethod {
				var mArgs []ast.Expr
				for _, argCtx := range argListCtx.AllArgument() {
					arg := argCtx.(*grammar.ArgumentContext)
					pat := arg.Pattern()
					ep, ok := pat.(*grammar.ExpressionPatternContext)
					if !ok {
						return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
					}
					expr, err := t.transformExpression(ep.Expression())
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				}

				var fun ast.Expr
				if recvTypeName != "" {
					fullName := recvTypeName + "_" + method
					if recvTypeName == transpiler.TypeOption || recvTypeName == transpiler.TypeImmutable || recvTypeName == transpiler.TypeTuple || recvTypeName == transpiler.TypeEither {
						fun = t.stdIdent(fullName)
					} else {
						fun = ast.NewIdent(fullName)
					}
				} else {
					fun = ast.NewIdent(method)
				}

				if len(typeArgs) == 1 {
					fun = &ast.IndexExpr{X: fun, Index: typeArgs[0]}
				} else if len(typeArgs) > 1 {
					fun = &ast.IndexListExpr{X: fun, Indices: typeArgs}
				}

				return &ast.CallExpr{
					Fun:  fun,
					Args: append([]ast.Expr{receiver}, mArgs...),
				}, nil
			}

			for _, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				pat := arg.Pattern()
				ep, ok := pat.(*grammar.ExpressionPatternContext)
				if !ok {
					return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
				}
				expr, err := t.transformExpression(ep.Expression())
				if err != nil {
					return nil, err
				}

				if arg.Identifier() != nil {
					if namedArgs == nil {
						namedArgs = make(map[string]ast.Expr)
					}
					namedArgs[arg.Identifier().GetText()] = expr
				} else {
					args = append(args, expr)
				}
			}
		}
	}

	// Handle case where we have TypeName(...) which is a constructor call
	// GALA doesn't seem to have a specific rule for constructor calls,
	// but TypeName(...) should be transformed to TypeName{...} if it's a struct.
	typeName := t.getBaseTypeName(x)
	typeExpr := x

	// Check if the expression being called has an Apply method
	exprTypeName := t.getExprTypeName(x)
	if exprTypeName != "" {
		if typeMeta, ok := t.typeMetas[exprTypeName]; ok {
			if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
				isGeneric := methodMeta.IsGeneric || len(methodMeta.TypeParams) > 0
				if isGeneric {
					fullName := exprTypeName + "_Apply"
					var fun ast.Expr = ast.NewIdent(fullName)
					if exprTypeName == transpiler.TypeOption || exprTypeName == transpiler.TypeImmutable || exprTypeName == transpiler.TypeTuple || exprTypeName == transpiler.TypeEither ||
						exprTypeName == transpiler.FuncSome || exprTypeName == transpiler.FuncNone || exprTypeName == transpiler.FuncLeft || exprTypeName == transpiler.FuncRight {
						fun = t.stdIdent(fullName)
					}
					return &ast.CallExpr{
						Fun:  fun,
						Args: append([]ast.Expr{x}, args...),
					}, nil
				}
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   x,
						Sel: ast.NewIdent("Apply"),
					},
					Args: args,
				}, nil
			}
		}
	}

	if typeName != "" {
		if fieldNames, ok := t.structFields[typeName]; ok {
			// If it has no fields and has Apply method, it might be Implode("apple") -> Implode{}.Apply("apple")
			// or None() -> None{}.Apply()
			if typeMeta, ok := t.typeMetas[typeName]; ok {
				if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
					if len(fieldNames) == 0 {
						var typeArgs []ast.Expr
						realTypeExpr := typeExpr
						if idx, ok := typeExpr.(*ast.IndexExpr); ok {
							typeArgs = []ast.Expr{idx.Index}
							realTypeExpr = idx.X
						} else if idxList, ok := typeExpr.(*ast.IndexListExpr); ok {
							typeArgs = idxList.Indices
							realTypeExpr = idxList.X
						}

						receiver := &ast.CompositeLit{
							Type: realTypeExpr,
						}
						isGeneric := methodMeta.IsGeneric || len(methodMeta.TypeParams) > 0 || len(typeArgs) > 0
						if isGeneric {
							fullName := typeName + "_Apply"
							var fun ast.Expr = ast.NewIdent(fullName)
							if typeName == transpiler.TypeOption || typeName == transpiler.TypeImmutable || typeName == transpiler.TypeTuple || typeName == transpiler.TypeEither ||
								typeName == transpiler.FuncSome || typeName == transpiler.FuncNone || typeName == transpiler.FuncLeft || typeName == transpiler.FuncRight {
								fun = t.stdIdent(fullName)
							}
							if len(typeArgs) == 1 {
								fun = &ast.IndexExpr{X: fun, Index: typeArgs[0]}
							} else if len(typeArgs) > 1 {
								fun = &ast.IndexListExpr{X: fun, Indices: typeArgs}
							}
							return &ast.CallExpr{
								Fun:  fun,
								Args: append([]ast.Expr{receiver}, args...),
							}, nil
						}
						return &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   receiver,
								Sel: ast.NewIdent("Apply"),
							},
							Args: args,
						}, nil
					}
				}
			}

			var elts []ast.Expr
			immutFlags := t.structImmutFields[typeName]

			if namedArgs != nil {
				for i, fn := range fieldNames {
					if val, ok := namedArgs[fn]; ok {
						if i < len(immutFlags) && immutFlags[i] {
							val = &ast.CallExpr{
								Fun:  t.stdIdent(transpiler.FuncNewImmutable),
								Args: []ast.Expr{val},
							}
						}
						elts = append(elts, &ast.KeyValueExpr{
							Key:   ast.NewIdent(fn),
							Value: val,
						})
					}
				}
			} else {
				for i, arg := range args {
					if i < len(fieldNames) {
						if i < len(immutFlags) && immutFlags[i] {
							arg = &ast.CallExpr{
								Fun:  t.stdIdent(transpiler.FuncNewImmutable),
								Args: []ast.Expr{arg},
							}
						}
						elts = append(elts, &ast.KeyValueExpr{
							Key:   ast.NewIdent(fieldNames[i]),
							Value: arg,
						})
					}
				}
			}
			return &ast.CompositeLit{
				Type: typeExpr,
				Elts: elts,
			}, nil
		}
	}

	if namedArgs != nil {
		return nil, galaerr.NewSemanticError("named arguments only supported for Copy method or struct construction")
	}

	return &ast.CallExpr{Fun: x, Args: args}, nil
}

func (t *galaASTTransformer) transformExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}

	// expression: primary
	if p := ctx.Primary(); p != nil {
		return t.transformPrimary(p.(*grammar.PrimaryContext))
	}

	// expression: lambdaExpression
	if l := ctx.LambdaExpression(); l != nil {
		return t.transformLambda(l.(*grammar.LambdaExpressionContext))
	}

	// expression: ifExpression
	if i := ctx.IfExpression(); i != nil {
		return t.transformIfExpression(i.(*grammar.IfExpressionContext))
	}

	// expression: expression 'match' '{' caseClause+ '}'
	// We check if it's a match by checking the number of children and existence of MATCH token
	if ctx.GetChildCount() >= 4 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			if ctx.GetChild(i).(antlr.ParseTree).GetText() == "match" {
				return t.transformMatchExpression(ctx)
			}
		}
	}

	// Handle recursive expression patterns
	// Since there are no labels, we check the number of children and the tokens
	childCount := ctx.GetChildCount()
	if childCount == 2 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)

		if _, ok := child1.(*grammar.UnaryOpContext); ok {
			expr, err := t.transformExpression(child2.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			opText := child1.(antlr.ParseTree).GetText()
			if opText == "*" {
				return &ast.StarExpr{X: expr}, nil
			}
			return &ast.UnaryExpr{
				Op: t.getUnaryToken(opText),
				X:  expr,
			}, nil
		}
	}

	if childCount == 3 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)
		child3 := ctx.GetChild(2)

		c2Text := child2.(antlr.ParseTree).GetText()

		if c2Text == "." {
			// expression '.' identifier
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			selName := child3.(antlr.ParseTree).GetText()
			selExpr := &ast.SelectorExpr{
				X:   x,
				Sel: ast.NewIdent(selName),
			}
			if t.immutFields[selName] {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   selExpr,
						Sel: ast.NewIdent("Get"),
					},
				}, nil
			}
			return selExpr, nil
		}

		if c2Text == "(" && child3.(antlr.ParseTree).GetText() == ")" {
			// expression '(' ')'
			return t.transformCallExpr(ctx.(*grammar.ExpressionContext))
		}

		// expression binaryOp expression
		// Note: child2 might be the binaryOp rule or a terminal.
		// In our grammar, binaryOp is a rule.
		if _, ok := child2.(*grammar.BinaryOpContext); ok {
			left, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			right, err := t.transformExpression(child3.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			return &ast.BinaryExpr{
				X:  left,
				Op: t.getBinaryToken(c2Text),
				Y:  right,
			}, nil
		}
	}

	if childCount == 4 {
		child2 := ctx.GetChild(1)
		child4 := ctx.GetChild(3)

		c2Text := child2.(antlr.ParseTree).GetText()
		c4Text := child4.(antlr.ParseTree).GetText()

		if c2Text == "(" && c4Text == ")" {
			// expression '(' argumentList? ')'
			return t.transformCallExpr(ctx.(*grammar.ExpressionContext))
		}

		if c2Text == "[" && c4Text == "]" {
			// expression '[' expressionList ']'
			child1 := ctx.GetChild(0)
			child3 := ctx.GetChild(2)
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			indices, err := t.transformExpressionList(child3.(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(indices) == 1 {
				return &ast.IndexExpr{X: x, Index: indices[0]}, nil
			} else {
				return &ast.IndexListExpr{X: x, Indices: indices}, nil
			}
		}
	}

	return nil, galaerr.NewSemanticError(fmt.Sprintf("expression transformation not fully implemented for %T: %s", ctx, ctx.GetText()))
}

func (t *galaASTTransformer) transformExpressionList(ctx *grammar.ExpressionListContext) ([]ast.Expr, error) {
	var exprs []ast.Expr
	for _, eCtx := range ctx.AllExpression() {
		e, err := t.transformExpression(eCtx)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, nil
}

func (t *galaASTTransformer) getBinaryToken(op string) token.Token {
	switch op {
	case "||":
		return token.LOR
	case "&&":
		return token.LAND
	case "==":
		return token.EQL
	case "!=":
		return token.NEQ
	case "<":
		return token.LSS
	case "<=":
		return token.LEQ
	case ">":
		return token.GTR
	case ">=":
		return token.GEQ
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "|":
		return token.OR
	case "^":
		return token.XOR
	case "*":
		return token.MUL
	case "/":
		return token.QUO
	case "%":
		return token.REM
	case "<<":
		return token.SHL
	case ">>":
		return token.SHR
	case "&":
		return token.AND
	case "&^":
		return token.AND_NOT
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) getUnaryToken(op string) token.Token {
	switch op {
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "!":
		return token.NOT
	case "^":
		return token.XOR
	case "&":
		return token.AND
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) transformPrimary(ctx *grammar.PrimaryContext) (ast.Expr, error) {
	if ctx.Identifier() != nil {
		name := ctx.Identifier().GetText()
		if name == transpiler.FuncSome || name == transpiler.FuncNone || name == transpiler.FuncLeft || name == transpiler.FuncRight ||
			name == transpiler.TypeTuple || name == transpiler.TypeEither {
			return t.stdIdent(name), nil
		}
		ident := ast.NewIdent(name)
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
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	if ctx.Expression() != nil {
		return t.transformExpression(ctx.Expression())
	}
	return nil, nil
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

func (t *galaASTTransformer) transformLambda(ctx *grammar.LambdaExpressionContext) (ast.Expr, error) {
	t.pushScope()
	defer t.popScope()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var body *ast.BlockStmt
	var retType ast.Expr = ast.NewIdent("any")

	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		// Add return nil to ensure Go compiler is happy with 'any' return type
		b.List = append(b.List, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("nil")}})
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		retType = t.getExprType(expr)
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: fieldList,
			Results: &ast.FieldList{
				List: []*ast.Field{
					{Type: retType},
				},
			},
		},
		Body: body,
	}, nil
}

func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	// 'if' '(' cond ')' thenExpr 'else' elseExpr
	cond, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}
	thenExpr, err := t.transformExpression(ctx.Expression(1))
	if err != nil {
		return nil, err
	}
	elseExpr, err := t.transformExpression(ctx.Expression(2))
	if err != nil {
		return nil, err
	}

	// Transpile to IIFE: func() any { if cond { return thenExpr }; return elseExpr }()
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: cond,
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{Results: []ast.Expr{thenExpr}},
							},
						},
					},
					&ast.ReturnStmt{Results: []ast.Expr{elseExpr}},
				},
			},
		},
	}, nil
}
