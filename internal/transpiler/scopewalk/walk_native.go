package scopewalk

// This file is built only with galop's parser (see BUILD.bazel): it reads the
// native tree. walk_native_off.go stands in for it in the ANTLR-only build.

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// nativeWalk is the walk over galop's native tree (internal/parser/native).
// It is antlrWalk node for node: the same handlers, visiting the same nodes in
// the same order, so it reports the same references at the same sites. Each
// handler sits under the name of its ANTLR twin.
type nativeWalk struct{ *Walker }

// nativeNode is node's native node, when the Walker has a native tree and
// node is a context of the parse it belongs to.
//
// It finds the node by its path from the root rather than with Native.Node,
// whose context-to-node map spans the whole file: the walks start from a few
// contexts (the top-level declarations, a lambda), each a short climb from
// the root. The native tree has ANTLR's tree's shape, child for child, so the
// indexes of the climb lead back down to the node; Native.Ctx confirms it.
func (w *Walker) nativeNode(node antlr.Tree) (native.GalaNodeRef, bool) {
	ctx, ok := node.(antlr.ParserRuleContext)
	if !ok || w.native == nil {
		return native.GalaNodeRef{}, false
	}
	tree, err := w.native.Tree()
	if err != nil {
		return native.GalaNodeRef{}, false
	}
	n := tree.Root()
	rootCtx := w.native.Ctx(n)
	var buf [32]int
	path := buf[:0] // child indexes from ctx up to the root
	for c := ctx; c != rootCtx; {
		parent, ok := c.GetParent().(antlr.ParserRuleContext)
		if !ok {
			return native.GalaNodeRef{}, false // another tree's context
		}
		i := childIndex(parent, c)
		if i < 0 {
			return native.GalaNodeRef{}, false
		}
		path = append(path, i)
		c = parent
	}
	for k := len(path) - 1; k >= 0 && n.Ok(); k-- {
		if path[k] >= n.ChildCount() {
			return native.GalaNodeRef{}, false
		}
		n = n.Child(path[k]).Node()
	}
	return n, n.Ok() && w.native.Ctx(n) == ctx
}

// childIndex is the index of child among parent's children, or -1.
func childIndex(parent, child antlr.ParserRuleContext) int {
	for i := 0; i < parent.GetChildCount(); i++ {
		if parent.GetChild(i) == antlr.Tree(child) {
			return i
		}
	}
	return -1
}

// walkNative walks node on the native tree, and reports whether it could.
func (w *Walker) walkNative(node antlr.Tree) bool {
	n, ok := w.nativeNode(node)
	if ok {
		nativeWalk{w}.walk(n)
	}
	return ok
}

// walkParameterDefaultsNative is WalkParameterDefaults on the native tree,
// and reports whether it could.
func (w *Walker) walkParameterDefaultsNative(params antlr.Tree) bool {
	n, ok := w.nativeNode(params)
	if ok {
		nativeWalk{w}.walkParameterDefaults(native.ParametersNode{GalaNodeRef: n})
	}
	return ok
}

// ---------------------------------------------------------------------------
// Tokens and binders
// ---------------------------------------------------------------------------

// textOf is n's text. A node of one token, an identifier's, is that token's
// text, a slice of the source.
func textOf(n native.GalaNodeRef) string {
	if i := n.StartIndex(); n.StopIndex() == i {
		return n.Tree().Text(i)
	}
	return n.GetText()
}

// siteOf is the site of token tok, whose text is text.
func siteOf(tok native.GalaToken, text string) Site {
	return Site{Line: int(tok.Line), Column: int(tok.Column), Text: text}
}

// referenceID reports the identifier id as a reference, at its token.
func (w nativeWalk) referenceID(id native.IdentifierNode, use Use) {
	name := textOf(id.GalaNodeRef)
	w.reference(name, siteOf(id.Start(), name), use)
}

func (w nativeWalk) bindID(id native.IdentifierNode) {
	if id.Ok() {
		w.Bind(textOf(id.GalaNodeRef))
	}
}

func (w nativeWalk) bindIdentifierList(idList native.IdentifierListNode) {
	for _, id := range idList.AllIdentifier() {
		w.Bind(textOf(id.GalaNodeRef))
	}
}

func (w nativeWalk) bindParameters(params native.ParametersNode) {
	for _, p := range params.ParameterList().AllParameter() {
		w.bindID(p.Identifier())
	}
}

func (w nativeWalk) walkParameterDefaults(params native.ParametersNode) {
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
func (w nativeWalk) walk(n native.GalaNodeRef) {
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

func (w nativeWalk) walkLambda(lambda native.LambdaExpressionNode) {
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

func (w nativeWalk) walkBlock(block native.BlockNode) {
	if !block.Ok() {
		return
	}
	w.PushScope()
	defer w.PopScope()
	for _, s := range block.AllStatement() {
		w.walk(s.GalaNodeRef)
	}
}

func (w nativeWalk) walkValVarDecl(exprList native.ExpressionListNode, idList native.IdentifierListNode, tuple native.TuplePatternNode) {
	for _, e := range exprList.AllExpression() {
		w.walk(e.GalaNodeRef)
	}
	w.bindIdentifierList(idList)
	w.bindIdentifierList(tuple.IdentifierList())
}

// walkFunctionDeclaration hands Options.EnterFunctionScope the declaration's
// ANTLR context: the hook's callers are not on the native tree yet.
func (w nativeWalk) walkFunctionDeclaration(fn native.FunctionDeclarationNode) {
	if !fn.Ok() {
		return
	}
	w.bindID(fn.Identifier())
	w.PushScope()
	defer w.PopScope()
	if w.opts.EnterFunctionScope != nil {
		ctx, ok := w.native.Ctx(fn.GalaNodeRef).(grammar.IFunctionDeclarationContext)
		if !ok {
			// Every node has the context of its rule (TestEnginesAgree checks
			// the bridge); skipping the hook would turn its binders into
			// undefined names.
			panic("scopewalk: a native function declaration has no ANTLR context")
		}
		w.opts.EnterFunctionScope(w.Walker, ctx)
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

func (w nativeWalk) walkIfStatement(ifs native.IfStatementNode) {
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

func (w nativeWalk) walkForStatement(fs native.ForStatementNode) {
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
			if op := rc.Child(1).Terminal(); op.Ok() && op.GetText() == ":=" {
				w.bindIdentifierList(idList)
			} else {
				for _, id := range idList.AllIdentifier() {
					w.referenceID(id, Use{Whole: true})
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

func (w nativeWalk) walkPostfixExpr(pf native.PostfixExprNode) {
	pe := pf.PrimaryExpr()
	suffixes := pf.AllPostfixSuffix()
	caseClauses := pf.AllCaseClause()

	if id := pe.Primary().Identifier(); id.Ok() {
		path, whole := classifySuffixes(suffixes, len(caseClauses) > 0, nativeSuffix)
		w.referenceID(id, Use{Path: path, Whole: whole})
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

// nativeSuffix is the shape of a native postfix suffix (see classifySuffixes).
func nativeSuffix(s native.PostfixSuffixNode) suffix {
	if id := s.Identifier(); id.Ok() {
		return suffix{selector: textOf(id.GalaNodeRef)}
	}
	return suffix{index: s.ExpressionList().Ok()}
}

func (w nativeWalk) walkPrimary(prim native.PrimaryNode) {
	if id := prim.Identifier(); id.Ok() {
		w.referenceID(id, Use{Whole: true})
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

func (w nativeWalk) walkLiteral(lit native.LiteralNode) {
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
	w.walkInterpolated(siteOf(tok.Token(), tok.GetText()))
}

func (w nativeWalk) walkCompositeLiteral(cl native.CompositeLiteralNode) {
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

func (w nativeWalk) walkPostfixSuffix(s native.PostfixSuffixNode) {
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

func (w nativeWalk) walkArgument(arg native.ArgumentNode) {
	if lambda := arg.LambdaExpression(); lambda.Ok() {
		w.walkLambda(lambda)
		return
	}
	if pat := arg.Pattern(); pat.Ok() {
		w.walkPatternAsValue(pat)
	}
}

func (w nativeWalk) walkPatternAsValue(pat native.PatternNode) {
	if p, ok := pat.AsExpressionPattern(); ok {
		w.walk(p.Expression().GalaNodeRef)
	} else if p, ok := pat.AsRestPattern(); ok {
		w.walk(p.Expression().GalaNodeRef)
	} else if p, ok := pat.AsTypedPattern(); ok && !w.opts.SkipTypedPatternArgument {
		if id := p.Identifier(); id.Ok() {
			w.referenceID(id, Use{Whole: true})
		}
	}
}

// ---------------------------------------------------------------------------
// Match / partial-function case clauses
// ---------------------------------------------------------------------------

func (w nativeWalk) walkCaseClause(cc native.CaseClauseNode) {
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

func (w nativeWalk) bindPattern(pat native.PatternNode) {
	if !pat.Ok() {
		return
	}
	if w.opts.BindWholePattern {
		pat.Walk(func(n native.GalaNodeRef) bool {
			if n.Kind() != native.GalaKIND_identifier {
				return true
			}
			w.Bind(textOf(n))
			return false
		})
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

func (w nativeWalk) bindPatternExpression(expr native.ExpressionNode) {
	pf, ok := singleNativePostfixExpr(expr)
	if !ok {
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

// singleNativePostfixExpr is singlePostfixExpr on the native tree.
func singleNativePostfixExpr(expr native.ExpressionNode) (native.PostfixExprNode, bool) {
	none := native.PostfixExprNode{}
	ands := expr.OrExpr().AllAndExpr()
	if len(ands) != 1 {
		return none, false
	}
	eqs := ands[0].AllEqualityExpr()
	if len(eqs) != 1 {
		return none, false
	}
	rels := eqs[0].AllRelationalExpr()
	if len(rels) != 1 {
		return none, false
	}
	adds := rels[0].AllAdditiveExpr()
	if len(adds) != 1 {
		return none, false
	}
	muls := adds[0].AllMultiplicativeExpr()
	if len(muls) != 1 {
		return none, false
	}
	uns := muls[0].AllUnaryExpr()
	if len(uns) != 1 {
		return none, false
	}
	pf := uns[0].PostfixExpr()
	return pf, pf.Ok()
}
