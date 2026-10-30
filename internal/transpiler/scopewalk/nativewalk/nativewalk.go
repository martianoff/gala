// Package nativewalk walks scopewalk's walk on galop's native tree
// (internal/parser/native).
//
// The walk here is scopewalk's ANTLR walk (walk_antlr.go) node for node: the
// same handlers, visiting the same nodes in the same order, so it reports the
// same references at the same sites. Each handler sits under the name of its
// ANTLR twin. TestWalksAgree holds the two together.
//
// A caller on the native tree enters it here, with a node, where a caller on
// ANTLR's tree enters scopewalk's walk with a context: the walk never crosses
// from one tree to the other (an interpolation fragment, re-parsed into
// ANTLR's tree alone, is the one walk of ANTLR's tree it makes).
package nativewalk

import (
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// EnterFunctionScope is scopewalk.Options.EnterFunctionScope on the native
// tree: called after the walk pushes the scope for a function declaration
// and before it binds the declaration's parameters.
type EnterFunctionScope func(w *scopewalk.Walker, fn native.FunctionDeclarationNode)

// Walker is the walk on the native tree of a scopewalk.Walker: it reports to
// that Walker's Visitor, under its options, in its scopes.
type Walker struct {
	w     *scopewalk.Walker
	enter EnterFunctionScope
}

// New is w's walk on the native tree. enter, which may be nil, stands in for
// w's Options.EnterFunctionScope, whose hook takes an ANTLR context and is
// never called on this tree.
func New(w *scopewalk.Walker, enter EnterFunctionScope) Walker {
	return Walker{w: w, enter: enter}
}

// Walk is scopewalk.Walker.Walk of node n.
func (nw Walker) Walk(n native.GalaNodeRef) { nw.walk().walk(n) }

// WalkFunctionDeclaration is scopewalk.Walker.WalkFunctionDeclaration of fn.
func (nw Walker) WalkFunctionDeclaration(fn native.FunctionDeclarationNode) {
	nw.walk().walkFunctionDeclaration(fn)
}

// WalkParameterDefaults is scopewalk.Walker.WalkParameterDefaults of params.
func (nw Walker) WalkParameterDefaults(params native.ParametersNode) {
	nw.walk().walkParameterDefaults(params)
}

func (nw Walker) walk() walk { return walk{nw.w, nw.w.Options(), nw.enter} }

// BindIdentifiersIn is scopewalk.Walker.BindIdentifiersIn of node n: it binds
// every identifier under n in w's innermost scope.
func BindIdentifiersIn(w *scopewalk.Walker, n native.GalaNodeRef) {
	if !n.Ok() {
		return
	}
	n.Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() != native.GalaKIND_identifier {
			return true
		}
		w.Bind(textOf(n))
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

// textOf is n's text (parser.NativeText).
func textOf(n native.GalaNodeRef) string { return parser.NativeText(n) }

// isToken reports whether t is a token whose text is text.
func isToken(t native.GalaTerminal, text string) bool { return t.Ok() && t.GetText() == text }

// siteOf is the site of token tok, whose text is text.
func siteOf(tok native.GalaToken, text string) scopewalk.Site {
	return scopewalk.Site{Line: int(tok.Line), Column: int(tok.Column), Text: text}
}

// IdentifierSite is the name identifier id spells and the site of its token:
// what scopewalk.TokenSite gives for its context's start token. An identifier
// of no token has no site.
func IdentifierSite(id native.GalaNodeRef) (string, scopewalk.Site) {
	name := textOf(id)
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

func (w walk) bindID(id native.IdentifierNode) {
	if id.Ok() {
		w.Bind(textOf(id.GalaNodeRef))
	}
}

func (w walk) bindIdentifierList(idList native.IdentifierListNode) {
	for _, id := range idList.AllIdentifier() {
		w.Bind(textOf(id.GalaNodeRef))
	}
}

func (w walk) bindParameters(params native.ParametersNode) {
	for _, p := range params.ParameterList().AllParameter() {
		w.bindID(p.Identifier())
	}
}

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

// walk is antlrWalk.walk: precise handlers for the nodes that scope or
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

// walkFunctionDeclaration calls the walk's EnterFunctionScope, the native
// tree's stand-in for Options.EnterFunctionScope.
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
		return scopewalk.Suffix{Selector: textOf(id.GalaNodeRef)}
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
	w.WalkInterpolated(siteOf(tok.Token(), tok.GetText()))
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
			w.Bind(textOf(id.GalaNodeRef))
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
