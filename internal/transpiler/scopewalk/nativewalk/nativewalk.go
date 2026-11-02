// Package nativewalk is scopewalk's walk on galop's native tree
// (internal/parser/native): a caller enters it with a node, and it reports
// to a scopewalk.Walker's Visitor, under the Walker's options, in its scopes.
// An interpolation fragment is parsed into a native tree of its own
// (parser.ParseExpressionTree) and walked the same way.
package nativewalk

import (
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// EnterFunctionScope, when a walk has one, is called after the walk pushes the
// scope for a function declaration and before it binds the declaration's
// parameters. It is the hook for binders the walk does not model itself —
// type parameters and a method receiver, which only the analyzer's pass needs.
type EnterFunctionScope func(w *scopewalk.Walker, fn native.FunctionDeclarationNode)

// Walker is the walk on the native tree of a scopewalk.Walker: it reports to
// that Walker's Visitor, under its options, in its scopes.
type Walker struct{ w walk }

// New is w's walk on the native tree. enter, when not nil, is called for
// each function declaration (see EnterFunctionScope).
func New(w *scopewalk.Walker, enter EnterFunctionScope) Walker {
	return Walker{walk{w, w.Options(), enter}}
}

// Walk descends node n. Nodes that affect scoping or contribute references
// have precise handlers; every other node recurses into all its children. A
// lambda pushes a fresh scope, binds its parameters into it, walks its body
// and pops, so a nested lambda's parameters shadow only within its own scope;
// a block introduces a nested scope and walks its statements in order, so a
// local declared partway through shadows an enclosing name only for the
// references that follow it.
func (nw Walker) Walk(n native.GalaNodeRef) { nw.w.walk(n) }

// WalkFunctionDeclaration handles a `func f(...) = body` (or block-bodied)
// declaration. The name is bound in the ENCLOSING scope, so the function may
// recurse and later statements may call it; its parameters live in a fresh
// nested scope covering only its body. The EnterFunctionScope hook runs inside
// that scope, before the parameters bind.
func (nw Walker) WalkFunctionDeclaration(fn native.FunctionDeclarationNode) {
	nw.w.walkFunctionDeclaration(fn)
}

// WalkParameterDefaults walks the `= expr` default of each parameter, so a
// reference inside a default value is seen.
func (nw Walker) WalkParameterDefaults(params native.ParametersNode) {
	nw.w.walkParameterDefaults(params)
}

// The binders: each binds names in w's innermost scope.

// BindID binds the name identifier id spells, if there is one.
func BindID(w *scopewalk.Walker, id native.IdentifierNode) {
	if id.Ok() {
		w.Bind(parser.NativeText(id.GalaNodeRef))
	}
}

// BindIdentifierList binds every name of an identifier list.
func BindIdentifierList(w *scopewalk.Walker, idList native.IdentifierListNode) {
	for id := range parser.NativeKids(idList.GalaNodeRef, native.GalaKIND_identifier) {
		w.Bind(parser.NativeText(id))
	}
}

// BindParameters binds each named parameter of a parameter list. Type-only
// parameters (function-type positions with no identifier) bind nothing.
func BindParameters(w *scopewalk.Walker, params native.ParametersNode) {
	for p := range parser.NativeKids(params.ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		BindID(w, native.ParameterNode{GalaNodeRef: p}.Identifier())
	}
}

// BindIdentifiersIn binds every identifier under node n. It is the blunt
// instrument for a construct whose binder/reference split the walk does not
// model — a method receiver's type arguments (`func (s Stack[T]) …` introduces
// T), or a whole `case` pattern under Options.BindWholePattern. Over-binding
// can only mask a reference, never invent one.
func BindIdentifiersIn(w *scopewalk.Walker, n native.GalaNodeRef) {
	if !n.Ok() {
		return
	}
	n.Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() != native.GalaKIND_identifier {
			return true
		}
		w.Bind(parser.NativeText(n))
		return false
	})
}

// walk is one walk on the native tree, for a scopewalk.Walker.
type walk struct {
	*scopewalk.Walker
	opts  scopewalk.Options
	enter EnterFunctionScope
}

// ---------------------------------------------------------------------------
// Tokens and binders
// ---------------------------------------------------------------------------

// isToken reports whether t is a token whose text is text.
func isToken(t native.GalaTerminal, text string) bool { return t.Ok() && t.GetText() == text }

// siteOf is the site of token tok, whose text is text.
func siteOf(tok native.GalaToken, text string) scopewalk.Site {
	return scopewalk.Site{Line: int(tok.Line), Column: int(tok.Column), Text: text}
}

// IdentifierSite is the name identifier id spells and the site of its token.
// An identifier of no token has no site.
func IdentifierSite(id native.GalaNodeRef) (string, scopewalk.Site) {
	name := parser.NativeText(id)
	if id.StopIndex() < id.StartIndex() {
		return name, scopewalk.Site{}
	}
	return name, siteOf(id.Start(), name)
}

// referenceID reports the identifier id as a reference, at its token; at no
// site for an identifier of no token.
func (w walk) referenceID(id native.IdentifierNode, use scopewalk.Use) {
	name, at := IdentifierSite(id.GalaNodeRef)
	w.Report(name, at, use)
}

func (w walk) bindID(id native.IdentifierNode) { BindID(w.Walker, id) }

func (w walk) bindIdentifierList(idList native.IdentifierListNode) {
	BindIdentifierList(w.Walker, idList)
}

func (w walk) bindParameters(params native.ParametersNode) { BindParameters(w.Walker, params) }

func (w walk) walkParameterDefaults(params native.ParametersNode) {
	for _, p := range params.ParameterList().AllParameter() {
		if def := p.ParamDefault(); def.Ok() {
			w.walk(def.Expression().GalaNodeRef)
		}
	}
}

// ---------------------------------------------------------------------------
// Generic dispatcher
// ---------------------------------------------------------------------------

// walk is Walker.Walk of n: precise handlers for the nodes that scope or
// reference, every child for the rest.
func (w walk) walk(n native.GalaNodeRef) {
	if !n.Ok() {
		return
	}
	switch n.Kind() {
	// A type is never a value.
	case native.GalaKIND_type:
		return

	// Scope-introducing constructs.
	case native.GalaKIND_lambdaExpression:
		w.walkLambda(native.LambdaExpressionNode{GalaNodeRef: n})
		return
	case native.GalaKIND_block:
		w.walkBlock(native.BlockNode{GalaNodeRef: n})
		return
	case native.GalaKIND_forStatement:
		w.walkForStatement(native.ForStatementNode{GalaNodeRef: n})
		return
	case native.GalaKIND_ifStatement:
		w.walkIfStatement(native.IfStatementNode{GalaNodeRef: n})
		return
	case native.GalaKIND_functionDeclaration:
		w.walkFunctionDeclaration(native.FunctionDeclarationNode{GalaNodeRef: n})
		return
	case native.GalaKIND_caseClause:
		w.walkCaseClause(native.CaseClauseNode{GalaNodeRef: n})
		return
	case native.GalaKIND_partialFunctionLiteral:
		for _, cc := range (native.PartialFunctionLiteralNode{GalaNodeRef: n}).AllCaseClause() {
			w.walkCaseClause(cc)
		}
		return

	// Order-dependent local bindings: initializer first, then the names.
	case native.GalaKIND_valDeclaration:
		d := native.ValDeclarationNode{GalaNodeRef: n}
		w.walkValVarDecl(d.ExpressionList(), d.IdentifierList(), d.TuplePattern())
		return
	case native.GalaKIND_varDeclaration:
		d := native.VarDeclarationNode{GalaNodeRef: n}
		w.walkValVarDecl(d.ExpressionList(), d.IdentifierList(), d.TuplePattern())
		return
	case native.GalaKIND_shortVarDecl:
		d := native.ShortVarDeclNode{GalaNodeRef: n}
		for _, e := range d.ExpressionList().AllExpression() {
			w.walk(e.GalaNodeRef)
		}
		w.bindIdentifierList(d.IdentifierList())
		return
	case native.GalaKIND_bindDeclaration:
		d := native.BindDeclarationNode{GalaNodeRef: n}
		w.walk(d.Expression().GalaNodeRef)
		w.bindID(d.Identifier())
		return
	case native.GalaKIND_alsoDeclaration:
		d := native.AlsoDeclarationNode{GalaNodeRef: n}
		w.walk(d.Expression().GalaNodeRef)
		w.bindID(d.Identifier())
		return
	case native.GalaKIND_useDeclaration:
		d := native.UseDeclarationNode{GalaNodeRef: n}
		w.walk(d.Expression().GalaNodeRef)
		w.bindID(d.Identifier())
		return
	// A type declaration binds its name for the rest of its block: a struct
	// type's name is also its constructor (`Inner(1)`), a value position. Its
	// body is types, but walks like any other node.
	case native.GalaKIND_typeDeclaration:
		w.bindID(native.TypeDeclarationNode{GalaNodeRef: n}.Identifier())
		// No return: the body still gets the generic recursion.

	// Reference-contributing leaves.
	case native.GalaKIND_postfixExpr:
		w.walkPostfixExpr(native.PostfixExprNode{GalaNodeRef: n})
		return
	case native.GalaKIND_primary:
		w.walkPrimary(native.PrimaryNode{GalaNodeRef: n})
		return
	case native.GalaKIND_postfixSuffix:
		w.walkPostfixSuffix(native.PostfixSuffixNode{GalaNodeRef: n})
		return
	case native.GalaKIND_argument:
		w.walkArgument(native.ArgumentNode{GalaNodeRef: n})
		return
	}

	// Default: every child node (a token child contributes nothing).
	for i := 0; i < n.ChildCount(); i++ {
		w.walk(n.Child(i).Node())
	}
}

// ---------------------------------------------------------------------------
// Scope-introducing handlers
// ---------------------------------------------------------------------------

func (w walk) walkLambda(lambda native.LambdaExpressionNode) {
	if !lambda.Ok() {
		return
	}
	w.PushScope()
	defer w.PopScope()

	w.bindParameters(lambda.Parameters())
	w.walkParameterDefaults(lambda.Parameters())

	if expr := lambda.Expression(); expr.Ok() {
		w.walk(expr.GalaNodeRef)
	} else if block := lambda.Block(); block.Ok() {
		w.walkBlock(block)
	}
}

func (w walk) walkBlock(block native.BlockNode) {
	if !block.Ok() {
		return
	}
	w.PushScope()
	defer w.PopScope()
	for _, s := range block.AllStatement() {
		w.walk(s.GalaNodeRef)
	}
}

func (w walk) walkValVarDecl(exprList native.ExpressionListNode, idList native.IdentifierListNode, tuple native.TuplePatternNode) {
	for _, e := range exprList.AllExpression() {
		w.walk(e.GalaNodeRef)
	}
	w.bindIdentifierList(idList)
	w.bindIdentifierList(tuple.IdentifierList())
}

// walkFunctionDeclaration is Walker.WalkFunctionDeclaration of fn.
func (w walk) walkFunctionDeclaration(fn native.FunctionDeclarationNode) {
	if !fn.Ok() {
		return
	}
	w.bindID(fn.Identifier())
	w.PushScope()
	defer w.PopScope()
	if w.enter != nil {
		w.enter(w.Walker, fn)
	}
	if sig := fn.Signature(); sig.Ok() {
		w.bindParameters(sig.Parameters())
		w.walkParameterDefaults(sig.Parameters())
	}
	if block := fn.Block(); block.Ok() {
		w.walkBlock(block)
	} else if e := fn.Expression(); e.Ok() {
		w.walk(e.GalaNodeRef)
	}
}

func (w walk) walkIfStatement(ifs native.IfStatementNode) {
	w.PushScope()
	defer w.PopScope()
	if ss := ifs.SimpleStatement(); ss.Ok() {
		w.walk(ss.GalaNodeRef)
	}
	if cond := ifs.Expression(); cond.Ok() {
		w.walk(cond.GalaNodeRef)
	}
	for _, b := range ifs.AllBlock() {
		w.walkBlock(b)
	}
	if elseIf := ifs.IfStatement(); elseIf.Ok() {
		w.walkIfStatement(elseIf)
	}
}

func (w walk) walkForStatement(fs native.ForStatementNode) {
	w.PushScope()
	defer w.PopScope()

	if fc := fs.ForClause(); fc.Ok() {
		for _, ss := range fc.AllSimpleStatement() {
			w.walk(ss.GalaNodeRef)
		}
		if e := fc.Expression(); e.Ok() {
			w.walk(e.GalaNodeRef)
		}
	} else if rc := fs.RangeClause(); rc.Ok() {
		if e := rc.Expression(); e.Ok() {
			w.walk(e.GalaNodeRef)
		}
		if idList := rc.IdentifierList(); idList.Ok() {
			// rangeClause: (identifierList (':=' | '='))? 'range' expression
			if rc.ChildCount() > 1 && isToken(rc.Child(1).Terminal(), ":=") {
				w.bindIdentifierList(idList)
			} else {
				for _, id := range idList.AllIdentifier() {
					w.referenceID(id, scopewalk.Use{Whole: true})
				}
			}
		}
	} else if cond := fs.ForCondition(); cond.Ok() {
		w.walk(cond.Expression().GalaNodeRef)
	}

	w.walkBlock(fs.Block())
}

// ---------------------------------------------------------------------------
// Reference-contributing handlers
// ---------------------------------------------------------------------------

func (w walk) walkPostfixExpr(pf native.PostfixExprNode) {
	pe := pf.PrimaryExpr()
	suffixes := pf.AllPostfixSuffix()
	caseClauses := pf.AllCaseClause()

	if id := pe.Primary().Identifier(); id.Ok() {
		path, whole := scopewalk.ClassifySuffixes(suffixes, len(caseClauses) > 0, nativeSuffix)
		w.referenceID(id, scopewalk.Use{Path: path, Whole: whole})
	} else {
		w.walk(pe.GalaNodeRef)
	}

	for _, s := range suffixes {
		w.walkPostfixSuffix(s)
	}
	for _, cc := range caseClauses {
		w.walkCaseClause(cc)
	}
}

// nativeSuffix is the shape of a native postfix suffix (see scopewalk.ClassifySuffixes).
func nativeSuffix(s native.PostfixSuffixNode) scopewalk.Suffix {
	if id := s.Identifier(); id.Ok() {
		return scopewalk.Suffix{Selector: parser.NativeText(id.GalaNodeRef)}
	}
	return scopewalk.Suffix{Index: s.ExpressionList().Ok()}
}

func (w walk) walkPrimary(prim native.PrimaryNode) {
	if id := prim.Identifier(); id.Ok() {
		w.referenceID(id, scopewalk.Use{Whole: true})
		return
	}
	if lit := prim.Literal(); lit.Ok() {
		w.walkLiteral(lit)
		return
	}
	if tup := prim.TupleExpressionList(); tup.Ok() {
		for _, e := range tup.AllExpression() {
			w.walk(e.GalaNodeRef)
		}
		return
	}
	if cl := prim.CompositeLiteral(); cl.Ok() {
		w.walkCompositeLiteral(cl)
	}
}

func (w walk) walkLiteral(lit native.LiteralNode) {
	if !w.opts.ParseInterpolations {
		return
	}
	tok := lit.INTERPOLATED_STRING()
	if !tok.Ok() {
		tok = lit.FORMAT_STRING()
	}
	if !tok.Ok() {
		return
	}
	// Each embedded expression is parsed on its own, into a native tree
	// (parser.ParseExpressionTree), and walked in the current scope.
	w.WalkInterpolatedParts(siteOf(tok.Token(), tok.GetText()), func(part string) {
		expr, errs := parser.ParseExpressionTree(part)
		if !expr.Ok() || len(errs) > 0 && w.opts.RequireCleanInterpolationParse {
			return
		}
		w.walk(expr.GalaNodeRef)
	})
}

func (w walk) walkCompositeLiteral(cl native.CompositeLiteralNode) {
	for _, ke := range cl.ElementList().AllKeyedElement() {
		exprs := ke.AllExpression()
		if w.opts.SkipCompositeLiteralKeys && len(exprs) > 1 {
			exprs = exprs[len(exprs)-1:]
		}
		for _, e := range exprs {
			w.walk(e.GalaNodeRef)
		}
	}
}

func (w walk) walkPostfixSuffix(s native.PostfixSuffixNode) {
	if s.Identifier().Ok() {
		return
	}
	if al := s.ArgumentList(); al.Ok() {
		for _, arg := range al.AllArgument() {
			w.walkArgument(arg)
		}
		return
	}
	for _, e := range s.ExpressionList().AllExpression() {
		w.walk(e.GalaNodeRef)
	}
}

func (w walk) walkArgument(arg native.ArgumentNode) {
	if lambda := arg.LambdaExpression(); lambda.Ok() {
		w.walkLambda(lambda)
		return
	}
	if pat := arg.Pattern(); pat.Ok() {
		w.walkPatternAsValue(pat)
	}
}

func (w walk) walkPatternAsValue(pat native.PatternNode) {
	if p, ok := pat.AsExpressionPattern(); ok {
		w.walk(p.Expression().GalaNodeRef)
	} else if p, ok := pat.AsRestPattern(); ok {
		w.walk(p.Expression().GalaNodeRef)
	} else if p, ok := pat.AsTypedPattern(); ok && !w.opts.SkipTypedPatternArgument {
		if id := p.Identifier(); id.Ok() {
			w.referenceID(id, scopewalk.Use{Whole: true})
		}
	}
}

// ---------------------------------------------------------------------------
// Match / partial-function case clauses
// ---------------------------------------------------------------------------

func (w walk) walkCaseClause(cc native.CaseClauseNode) {
	w.PushScope()
	defer w.PopScope()

	w.bindPattern(cc.Pattern())

	if guard := cc.GetGuard(); guard.Ok() {
		w.walk(guard.GalaNodeRef)
	}
	if block := cc.GetBodyBlock(); block.Ok() {
		w.walkBlock(block)
	} else if stmt := cc.GetBodyStmt(); stmt.Ok() {
		w.walk(stmt.GalaNodeRef)
	}
}

func (w walk) bindPattern(pat native.PatternNode) {
	if !pat.Ok() {
		return
	}
	if w.opts.BindWholePattern {
		BindIdentifiersIn(w.Walker, pat.GalaNodeRef)
		return
	}
	if p, ok := pat.AsExpressionPattern(); ok {
		w.bindPatternExpression(p.Expression())
	} else if p, ok := pat.AsRestPattern(); ok {
		w.bindPatternExpression(p.Expression())
	} else if p, ok := pat.AsTypedPattern(); ok {
		w.bindID(p.Identifier())
	}
}

func (w walk) bindPatternExpression(expr native.ExpressionNode) {
	pf := parser.NativeSolePostfixExpr(expr)
	if !pf.Ok() {
		return
	}
	suffixes := pf.AllPostfixSuffix()
	if len(suffixes) == 0 {
		prim := pf.PrimaryExpr().Primary()
		if id := prim.Identifier(); id.Ok() {
			w.Bind(parser.NativeText(id.GalaNodeRef))
			return
		}
		for _, e := range prim.TupleExpressionList().AllExpression() {
			w.bindPatternExpression(e)
		}
		return
	}
	for _, s := range suffixes {
		for _, arg := range s.ArgumentList().AllArgument() {
			if sub := arg.Pattern(); sub.Ok() {
				w.bindPattern(sub)
			}
		}
	}
}
