package transformer

import (
	"go/ast"
	"strings"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// bind.go implements the `bind` monadic do-notation.
//
// A block that contains `bind name = e` statements is lowered to a chain of
// FlatMap calls, so that
//
//	bind o = fetchOrder(id)
//	bind p = charge(o)
//	Success(Receipt(o, p))
//
// becomes
//
//	M_FlatMap[R, A](fetchOrder(id), func(o A) M[R] {
//	    return M_FlatMap[R, B](charge(o), func(p B) M[R] {
//	        return Success(Receipt(o, p))
//	    })
//	})
//
// where M is the block's monad (Try/Option/Either/Future or any user type with a
// FlatMap method) and M[R] is the enclosing function's return type. The lowering
// is structural: no type is special-cased, so a user-defined monad works exactly
// like std's. See docs/BIND_NOTATION.MD.

// bindDeclFromStatement returns the BindDeclaration context if the statement is a
// `bind name = expr`, else nil.
func bindDeclFromStatement(stmtCtx grammar.IStatementContext) grammar.IBindDeclarationContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	if bd := dc.BindDeclaration(); bd != nil {
		return bd
	}
	return nil
}

// alsoDeclFromStatement returns the AlsoDeclaration context if the statement is an
// `also name = expr`, else nil.
func alsoDeclFromStatement(stmtCtx grammar.IStatementContext) grammar.IAlsoDeclarationContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	if ad := dc.AlsoDeclaration(); ad != nil {
		return ad
	}
	return nil
}

// trailingBindValueExpr extracts the value expression from a block's trailing
// statement (e.g. `Success(...)`), or nil if the statement is not a bare
// expression.
func trailingBindValueExpr(stmtCtx grammar.IStatementContext) grammar.IExpressionContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	ss, ok := dc.SimpleStatement().(*grammar.SimpleStatementContext)
	if !ok || ss == nil {
		return nil
	}
	return ss.Expression()
}

// desugarBindChain lowers a run of statements beginning with a `bind` into a
// FlatMap call expression of type resultType (the enclosing monad M[R]).
func (t *galaASTTransformer) desugarBindChain(stmts []grammar.IStatementContext, resultType transpiler.Type) (ast.Expr, error) {
	bindCtx := bindDeclFromStatement(stmts[0])
	name := bindCtx.Identifier().GetText()

	// Transform the bound expression e : M[A].
	recvExpr, err := t.transformExpression(bindCtx.Expression())
	if err != nil {
		return nil, err
	}
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(recvExpr)
	// Normalize a current-package-qualified key (e.g. `main.Box`) to its bare
	// form (`Box`) so metadata lookup and the emitted `Box_FlatMap` name match.
	// std keys keep their `std.` prefix; imported packages keep theirs.
	lookupBaseName = strings.TrimPrefix(lookupBaseName, t.packageName+".")

	// Structural bindability check: M must expose a FlatMap method. No type is
	// special-cased — std and user monads resolve through the same metadata.
	typeMeta, _ := t.getTypeMetaResolved(lookupBaseName)
	if typeMeta == nil || typeMeta.Methods["FlatMap"] == nil {
		return nil, t.semanticErrorAt(bindCtx, "cannot `bind`: type "+recvType.String()+" has no FlatMap method (not a bindable monad)")
	}

	// The bound value's element type A. An explicit annotation on the bind wins.
	elemType := t.monadElemType(recvType)
	if bindCtx.Type_() != nil {
		te, terr := t.transformType(bindCtx.Type_())
		if terr != nil {
			return nil, terr
		}
		elemType = t.astTypeToTranspilerType(te)
	}

	// Same-monad requirement for sequential bind: the bound expression's monad
	// must match the block's monad. (Heterogeneous lift is a separate feature.)
	// Compare normalized (local-package-stripped) base names on both sides.
	resultBase := strings.TrimPrefix(resultType.BaseName(), t.packageName+".")
	if !resultType.IsNil() && lookupBaseName != resultBase {
		return nil, t.semanticErrorAt(bindCtx, "cannot `bind` a "+lookupBaseName+" inside a "+resultBase+" block (heterogeneous bind is not supported)")
	}

	// Register the bound name for the continuation. It is registered like a
	// function parameter (raw, un-wrapped) rather than a `val` (Immutable-wrapped)
	// because the value it names is the FlatMap callback's raw parameter, so reads
	// must not emit `.Get()`.
	t.addVar(name, elemType)
	body, err := t.buildBindBody(stmts[1:], resultType)
	if err != nil {
		return nil, err
	}

	lambda := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ast.NewIdent(name)},
				Type:  t.typeToExpr(elemType),
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(resultType)}}},
		},
		Body: body,
	}

	// Method-level type arg U = R (the result element). The receiver element T
	// is appended by emitGenericMethodFreeFunc from recvType's own type args.
	var methodTypeArgs []ast.Expr
	if rElem := t.monadElemType(resultType); !rElem.IsNil() {
		methodTypeArgs = []ast.Expr{t.typeToExpr(rElem)}
	}

	return t.emitGenericMethodFreeFunc("FlatMap", recvExpr, recvType, lookupBaseName, methodTypeArgs, nil, []ast.Expr{lambda}, false), nil
}

// buildBindBody builds the body block of a bind continuation lambda from the
// statements following a `bind`. Regular statements are emitted as-is; a nested
// `bind` recurses; the trailing statement becomes `return <value>`.
func (t *galaASTTransformer) buildBindBody(stmts []grammar.IStatementContext, resultType transpiler.Type) (*ast.BlockStmt, error) {
	body := &ast.BlockStmt{}
	for i, s := range stmts {
		if alsoDeclFromStatement(s) != nil {
			return nil, t.semanticErrorAt(s.(*grammar.StatementContext), "`also` (applicative bind) is not yet implemented")
		}
		if bindDeclFromStatement(s) != nil {
			expr, err := t.desugarBindChain(stmts[i:], resultType)
			if err != nil {
				return nil, err
			}
			body.List = append(body.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
			return body, nil
		}
		if i == len(stmts)-1 {
			expr, err := t.transformTrailingBindValue(s, resultType)
			if err != nil {
				return nil, err
			}
			body.List = append(body.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
			return body, nil
		}
		stmt, err := t.transformStatement(s.(*grammar.StatementContext))
		if err != nil {
			return nil, err
		}
		body.List = append(body.List, stmt)
	}
	return body, nil
}

// transformTrailingBindValue transforms the block's trailing value expression
// (the monad's result) with resultType pushed as the expected type so that
// constructors like Success/Some infer their type argument.
func (t *galaASTTransformer) transformTrailingBindValue(stmtCtx grammar.IStatementContext, resultType transpiler.Type) (ast.Expr, error) {
	exprCtx := trailingBindValueExpr(stmtCtx)
	if exprCtx == nil {
		return nil, t.semanticErrorAt(stmtCtx.(*grammar.StatementContext), "a `bind` block must end with a value expression")
	}
	if !resultType.IsNil() {
		release := t.expectedArgTypes.push(resultType)
		defer release()
	}
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}
	return t.unwrapImmutable(expr), nil
}

// monadElemType returns the single element type of a monad type M[A], or NilType
// if it has no type argument.
func (t *galaASTTransformer) monadElemType(monadType transpiler.Type) transpiler.Type {
	args := t.getReceiverTypeArgStrings(monadType)
	if len(args) == 0 {
		return transpiler.NilType{}
	}
	return transpiler.ParseType(args[0])
}
