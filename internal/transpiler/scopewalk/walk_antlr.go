package scopewalk

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// antlrWalk is the walk over ANTLR's tree (the *grammar.XContext types). It
// shares the Walker's scope stack, options and visitor.
type antlrWalk struct{ *Walker }

// walkParameterDefaults walks the `= expr` default of each parameter, so a
// reference inside a default value is seen.
func (w antlrWalk) walkParameterDefaults(params grammar.IParametersContext) {
	if params == nil {
		return
	}
	pl := params.ParameterList()
	if pl == nil {
		return
	}
	for _, p := range pl.AllParameter() {
		if pc, ok := p.(*grammar.ParameterContext); ok {
			if def := pc.ParamDefault(); def != nil {
				w.walk(def.Expression())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Generic dispatcher
// ---------------------------------------------------------------------------

// walk descends an arbitrary parse-tree node. Nodes that affect scoping or
// contribute references have precise handlers (dispatched here, returning
// without generic recursion so nothing is double-walked); every other node
// falls to the default branch, which recurses into all children.
func (w antlrWalk) walk(node antlr.Tree) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	// A type is never a value: short-circuit so no type name can reach the
	// reference stream through generic recursion.
	case *grammar.TypeContext:
		return

	// Scope-introducing constructs.
	case *grammar.LambdaExpressionContext:
		w.walkLambda(n)
		return
	case *grammar.BlockContext:
		w.walkBlock(n)
		return
	case *grammar.ForStatementContext:
		w.walkForStatement(n)
		return
	case *grammar.IfStatementContext:
		w.walkIfStatement(n)
		return
	case *grammar.FunctionDeclarationContext:
		w.walkFunctionDeclaration(n)
		return
	case *grammar.CaseClauseContext:
		w.walkCaseClause(n)
		return
	case *grammar.PartialFunctionLiteralContext:
		for _, cc := range n.AllCaseClause() {
			w.walkCaseClause(cc)
		}
		return

	// Order-dependent local bindings: walk the initializer first (in the
	// pre-binding scope), then bind, so `val x = x + 1` sees the outer x.
	case *grammar.ValDeclarationContext:
		w.walkValVarDecl(n.ExpressionList(), n.IdentifierList(), n.TuplePattern())
		return
	case *grammar.VarDeclarationContext:
		w.walkValVarDecl(n.ExpressionList(), n.IdentifierList(), n.TuplePattern())
		return
	case *grammar.ShortVarDeclContext:
		if el := n.ExpressionList(); el != nil {
			for _, e := range el.AllExpression() {
				w.walk(e)
			}
		}
		w.BindIdentifierList(n.IdentifierList())
		return
	case *grammar.BindDeclarationContext:
		w.walk(n.Expression())
		w.BindID(n.Identifier())
		return
	case *grammar.AlsoDeclarationContext:
		w.walk(n.Expression())
		w.BindID(n.Identifier())
		return
	case *grammar.UseDeclarationContext:
		w.walk(n.Expression())
		w.BindID(n.Identifier())
		return

	// Reference-contributing leaves.
	case *grammar.PostfixExprContext:
		w.walkPostfixExpr(n)
		return
	case *grammar.PrimaryContext:
		w.walkPrimary(n)
		return
	case *grammar.PostfixSuffixContext:
		w.walkPostfixSuffix(n)
		return
	case *grammar.ArgumentContext:
		w.walkArgument(n)
		return
	}

	// Default: no precise handler — recurse into every child so unhandled
	// forms still surface their references.
	for i := 0; i < node.GetChildCount(); i++ {
		w.walk(node.GetChild(i))
	}
}

// ---------------------------------------------------------------------------
// Scope-introducing handlers
// ---------------------------------------------------------------------------

// walkLambda pushes a fresh scope, binds the lambda's parameters into it, walks
// the body, and pops — so a nested lambda's parameters shadow only within its
// own scope.
func (w antlrWalk) walkLambda(lambda grammar.ILambdaExpressionContext) {
	if lambda == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()

	w.BindParameters(lambda.Parameters())
	// A parameter default (`(x = n) => …`) may reference a sibling parameter,
	// so defaults are walked after the names are bound.
	w.walkParameterDefaults(lambda.Parameters())

	if expr := lambda.Expression(); expr != nil {
		w.walk(expr)
	} else if block := lambda.Block(); block != nil {
		w.walkBlock(block)
	}
	// lambda.Type_() is the optional return-type annotation — a type.
}

// walkBlock introduces a nested scope and walks the block's statements in
// order, so a local declared partway through shadows an enclosing name only for
// references that follow it.
func (w antlrWalk) walkBlock(block grammar.IBlockContext) {
	if block == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()
	for _, s := range block.AllStatement() {
		w.walk(s)
	}
}

// walkValVarDecl walks a `val`/`var` initializer and then binds the declared
// name(s) — RHS first, in the scope that predates the binding.
func (w antlrWalk) walkValVarDecl(exprList grammar.IExpressionListContext, idList grammar.IIdentifierListContext, tuple grammar.ITuplePatternContext) {
	if exprList != nil {
		for _, e := range exprList.AllExpression() {
			w.walk(e)
		}
	}
	w.BindIdentifierList(idList)
	if tuple != nil {
		w.BindIdentifierList(tuple.IdentifierList())
	}
}

// walkFunctionDeclaration handles a `func f(...) = body` (or block-bodied)
// declaration. The name is bound in the ENCLOSING scope, so the function may
// recurse and later statements may call it; its parameters live in a fresh
// nested scope covering only its body. Options.EnterFunctionScope runs inside
// that scope, before the parameters bind, for binders this walker does not
// model (type parameters, a method receiver).
func (w antlrWalk) walkFunctionDeclaration(fn grammar.IFunctionDeclarationContext) {
	if fn == nil {
		return
	}
	w.BindID(fn.Identifier())
	w.PushScope()
	defer w.PopScope()
	if w.opts.EnterFunctionScope != nil {
		w.opts.EnterFunctionScope(w.Walker, fn)
	}
	if sig := fn.Signature(); sig != nil {
		w.BindParameters(sig.Parameters())
		w.walkParameterDefaults(sig.Parameters())
	}
	if block := fn.Block(); block != nil {
		w.walkBlock(block)
	} else if e := fn.Expression(); e != nil {
		w.walk(e)
	}
}

func (w antlrWalk) walkIfStatement(ifs grammar.IIfStatementContext) {
	// An `if init; cond { … }` init binding is visible in the condition and
	// both branches but NOT after the statement, so it gets a scope wrapping
	// the whole statement.
	w.PushScope()
	defer w.PopScope()
	if ss := ifs.SimpleStatement(); ss != nil {
		w.walk(ss)
	}
	if cond := ifs.Expression(); cond != nil {
		w.walk(cond)
	}
	for _, b := range ifs.AllBlock() {
		w.walkBlock(b)
	}
	if elseIf := ifs.IfStatement(); elseIf != nil {
		w.walkIfStatement(elseIf)
	}
}

func (w antlrWalk) walkForStatement(fs grammar.IForStatementContext) {
	// Loop variables live in a scope that wraps the loop body.
	w.PushScope()
	defer w.PopScope()

	if fc := fs.ForClause(); fc != nil {
		// forClause: simpleStatement? ';' expression? ';' simpleStatement?
		for _, ss := range fc.AllSimpleStatement() {
			w.walk(ss)
		}
		if e := fc.Expression(); e != nil {
			w.walk(e)
		}
	} else if rc := fs.RangeClause(); rc != nil {
		// rangeClause: (identifierList (':=' | '='))? 'range' expression
		if e := rc.Expression(); e != nil {
			w.walk(e) // the ranged value resolves before the loop vars bind
		}
		if idList := rc.IdentifierList(); idList != nil {
			if strings.Contains(rc.GetText(), ":=") {
				// `i, v := range xs` — fresh loop-variable bindings.
				w.BindIdentifierList(idList)
			} else {
				// `i, v = range xs` — assignment to existing variables.
				for _, id := range idList.AllIdentifier() {
					w.referenceID(id, Use{Whole: true})
				}
			}
		}
	} else if cond := fs.ForCondition(); cond != nil {
		w.walk(cond.Expression())
	}

	w.walkBlock(fs.Block())
}

// ---------------------------------------------------------------------------
// Reference-contributing handlers
// ---------------------------------------------------------------------------

// walkPostfixExpr handles a `primaryExpr postfixSuffix* ('match' … )?` chain.
// When the base is a bare value-position identifier it classifies HOW the name
// is used — a pure `.field` selector chain as a read path, anything else as a
// whole use. Regardless of the base, the contents of every suffix and every
// `match` arm are walked so nested references are still found.
func (w antlrWalk) walkPostfixExpr(ctx grammar.IPostfixExprContext) {
	if ctx == nil {
		return
	}
	pe := ctx.PrimaryExpr()
	suffixes := ctx.AllPostfixSuffix()
	caseClauses := ctx.AllCaseClause()

	if id := bareIdentPrimary(pe); id != nil {
		path, whole := classifySuffixes(suffixes, len(caseClauses) > 0)
		w.referenceID(id, Use{Path: path, Whole: whole})
	} else {
		// Base is not a bare identifier (literal, tuple/paren, composite
		// literal, lambda, if-expression, partial function): recurse into it.
		w.walk(pe)
	}

	for _, s := range suffixes {
		w.walkPostfixSuffix(s)
	}
	for _, cc := range caseClauses {
		w.walkCaseClause(cc)
	}
}

// classifySuffixes decides whether a bare-identifier base followed by the given
// suffixes (and an optional trailing `match`) is a pure field-read path or a
// whole use. A leading, uninterrupted run of `.field` selectors with no call,
// index, or match is a field-read path; an empty suffix list (a bare
// reference), a trailing match, or any call/index makes it a whole use.
func classifySuffixes(suffixes []grammar.IPostfixSuffixContext, hasMatch bool) (path string, whole bool) {
	if hasMatch || len(suffixes) == 0 {
		return "", true
	}
	parts := make([]string, 0, len(suffixes))
	for i, s := range suffixes {
		sc, ok := s.(*grammar.PostfixSuffixContext)
		if !ok {
			return "", true
		}
		if id := sc.Identifier(); id != nil {
			parts = append(parts, id.GetText())
			continue
		}
		if sc.ExpressionList() != nil {
			// Index `[...]` — a value-position use of the whole receiver.
			return "", true
		}
		// A call `(...)`. Tolerate a SINGLE trailing method call on a
		// field-path receiver: `x.a.b.m(...)` reads the field value `x.a.b` and
		// calls a method on it. The last selector is the method name, so the
		// receiver path is the rest; at least one selector must remain (else it
		// is a method on the whole value, which stays a whole use).
		if i == len(suffixes)-1 && len(parts) >= 2 {
			return strings.Join(parts[:len(parts)-1], "."), false
		}
		return "", true
	}
	return strings.Join(parts, "."), false
}

// bareIdentPrimary returns the identifier context when the primaryExpr is a
// bare value-position identifier (`x`), or nil for any other shape.
func bareIdentPrimary(pe grammar.IPrimaryExprContext) grammar.IIdentifierContext {
	if pe == nil {
		return nil
	}
	prim := pe.Primary()
	if prim == nil {
		return nil
	}
	return prim.Identifier()
}

func (w antlrWalk) walkPrimary(ctx grammar.IPrimaryContext) {
	if ctx == nil {
		return
	}
	if id := ctx.Identifier(); id != nil {
		w.referenceID(id, Use{Whole: true})
		return
	}
	if lit := ctx.Literal(); lit != nil {
		w.walkLiteral(lit)
		return
	}
	if tup := ctx.TupleExpressionList(); tup != nil {
		for _, e := range tup.AllExpression() {
			w.walk(e)
		}
		return
	}
	if cl := ctx.CompositeLiteral(); cl != nil {
		w.walkCompositeLiteral(cl)
	}
}

// walkLiteral walks the embedded expressions of an interpolated (`s"…"`) or
// format (`f"…"`) string (see walkInterpolated). Every other literal
// contributes no references.
func (w antlrWalk) walkLiteral(lit grammar.ILiteralContext) {
	if !w.opts.ParseInterpolations {
		return
	}
	tok := lit.INTERPOLATED_STRING()
	if tok == nil {
		tok = lit.FORMAT_STRING()
	}
	if tok == nil {
		return
	}
	sym := tok.GetSymbol()
	w.walkInterpolated(Site{Line: sym.GetLine(), Column: sym.GetColumn(), Text: tok.GetText()})
}

// referenceID reports the identifier id as a reference, at its token.
func (w antlrWalk) referenceID(id grammar.IIdentifierContext, use Use) {
	name := id.GetText()
	tok := id.GetStart()
	w.reference(name, Site{Line: tok.GetLine(), Column: tok.GetColumn(), Text: name}, use)
}

// walkCompositeLiteral walks the element values of a composite literal. The
// literal's type is skipped; each keyed element's value is a value position.
// The key half is a field name in a struct literal, so
// Options.SkipCompositeLiteralKeys controls whether it is walked too.
func (w antlrWalk) walkCompositeLiteral(ctx grammar.ICompositeLiteralContext) {
	el := ctx.ElementList()
	if el == nil {
		return
	}
	for _, ke := range el.AllKeyedElement() {
		kec, ok := ke.(*grammar.KeyedElementContext)
		if !ok {
			continue
		}
		exprs := kec.AllExpression()
		if w.opts.SkipCompositeLiteralKeys && len(exprs) > 1 {
			exprs = exprs[len(exprs)-1:]
		}
		for _, e := range exprs {
			w.walk(e)
		}
	}
}

func (w antlrWalk) walkPostfixSuffix(ctx grammar.IPostfixSuffixContext) {
	if ctx == nil {
		return
	}
	// `.identifier` is a field/method selector on the preceding value — the
	// selector name is not a reference.
	if ctx.Identifier() != nil {
		return
	}
	if al := ctx.ArgumentList(); al != nil {
		for _, arg := range al.AllArgument() {
			w.walkArgument(arg)
		}
		return
	}
	// `[ expressionList ]` — an index or explicit type arguments. Index
	// expressions are value references; bracketed type arguments route through
	// the same primaries and resolve harmlessly for both consumers.
	if el := ctx.ExpressionList(); el != nil {
		for _, e := range el.AllExpression() {
			w.walk(e)
		}
	}
}

func (w antlrWalk) walkArgument(arg grammar.IArgumentContext) {
	if arg == nil {
		return
	}
	// A leading `identifier =` is a named-argument LABEL, not a reference.
	if lambda := arg.LambdaExpression(); lambda != nil {
		w.walkLambda(lambda)
		return
	}
	// In argument position the `pattern` rule is used as a VALUE expression.
	if pat := arg.Pattern(); pat != nil {
		w.walkPatternAsValue(pat)
	}
}

// walkPatternAsValue walks a `pattern` node in value position (a call
// argument), treating its identifiers as references rather than bindings.
func (w antlrWalk) walkPatternAsValue(pat grammar.IPatternContext) {
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		w.walk(p.Expression())
	case *grammar.RestPatternContext:
		w.walk(p.Expression()) // spread: `xs...`
	case *grammar.TypedPatternContext:
		if w.opts.SkipTypedPatternArgument {
			return
		}
		if id := p.Identifier(); id != nil {
			w.referenceID(id, Use{Whole: true})
		}
	}
}

// ---------------------------------------------------------------------------
// Match / partial-function case clauses
// ---------------------------------------------------------------------------

func (w antlrWalk) walkCaseClause(cc grammar.ICaseClauseContext) {
	if cc == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()

	// Pattern bindings (`case Some(x)` binds x) are in scope for guard & body.
	w.bindPattern(cc.Pattern())

	if guard := cc.GetGuard(); guard != nil {
		w.walk(guard)
	}
	if block := cc.GetBodyBlock(); block != nil {
		w.walkBlock(block)
	} else if stmt := cc.GetBodyStmt(); stmt != nil {
		w.walk(stmt)
	}
}

// bindPattern binds the names a case-clause pattern introduces. Constructor and
// extractor names are NOT bound — they are references, resolved like any other
// name — and their sub-patterns are recursed into. Under
// Options.BindWholePattern every identifier in the pattern is bound instead.
func (w antlrWalk) bindPattern(pat grammar.IPatternContext) {
	if pat == nil {
		return
	}
	if w.opts.BindWholePattern {
		w.BindIdentifiersIn(pat)
		return
	}
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		w.bindPatternExpression(p.Expression())
	case *grammar.RestPatternContext:
		w.bindPatternExpression(p.Expression()) // `rest...` binds rest
	case *grammar.TypedPatternContext:
		w.BindID(p.Identifier()) // `x: T` binds x; T is a type
	}
}

// bindPatternExpression binds the names introduced by an expression-shaped
// pattern: a bare identifier binds itself; a tuple `(a, b)` binds each
// element's names; an extractor call `Ctor(sub…)` (or `pkg.Ctor(sub…)`,
// `Ctor[T](sub…)`) binds the sub-patterns' names but not the constructor.
// Literals bind nothing.
func (w antlrWalk) bindPatternExpression(expr grammar.IExpressionContext) {
	pf := singlePostfixExpr(expr)
	if pf == nil {
		return
	}
	suffixes := pf.AllPostfixSuffix()
	if len(suffixes) == 0 {
		// No call/selector suffix: a bare identifier binding, or a
		// parenthesised (tuple) pattern.
		prim := primaryOfPrimaryExpr(pf.PrimaryExpr())
		if prim == nil {
			return
		}
		if id := prim.Identifier(); id != nil {
			w.Bind(id.GetText())
			return
		}
		if tup := prim.TupleExpressionList(); tup != nil {
			for _, e := range tup.AllExpression() {
				w.bindPatternExpression(e)
			}
		}
		return
	}
	// Extractor/constructor pattern: the primary is the constructor NAME (not a
	// binding). Each argument-list suffix carries the sub-patterns.
	for _, s := range suffixes {
		sc, ok := s.(*grammar.PostfixSuffixContext)
		if !ok {
			continue
		}
		if al := sc.ArgumentList(); al != nil {
			for _, arg := range al.AllArgument() {
				ac, ok := arg.(*grammar.ArgumentContext)
				if !ok {
					continue
				}
				if sub := ac.Pattern(); sub != nil {
					w.bindPattern(sub)
				}
			}
		}
	}
}

// identifiersUnder returns every `identifier` leaf under node. It backs
// Options.BindWholePattern.
func identifiersUnder(node antlr.Tree) []string {
	var out []string
	var rec func(antlr.Tree)
	rec = func(t antlr.Tree) {
		if t == nil {
			return
		}
		if id, ok := t.(*grammar.IdentifierContext); ok {
			out = append(out, id.GetText())
			return
		}
		for i := 0; i < t.GetChildCount(); i++ {
			rec(t.GetChild(i))
		}
	}
	rec(node)
	return out
}

// ---------------------------------------------------------------------------
// Parse-tree navigation helpers
// ---------------------------------------------------------------------------

// singlePostfixExpr descends an expression that is a single, operator-free
// chain down to its postfixExpr, returning nil if the expression branches.
func singlePostfixExpr(ctx grammar.IExpressionContext) grammar.IPostfixExprContext {
	if ctx == nil {
		return nil
	}
	or := ctx.OrExpr()
	if or == nil {
		return nil
	}
	ands := or.AllAndExpr()
	if len(ands) != 1 {
		return nil
	}
	eqs := ands[0].AllEqualityExpr()
	if len(eqs) != 1 {
		return nil
	}
	rels := eqs[0].AllRelationalExpr()
	if len(rels) != 1 {
		return nil
	}
	adds := rels[0].AllAdditiveExpr()
	if len(adds) != 1 {
		return nil
	}
	muls := adds[0].AllMultiplicativeExpr()
	if len(muls) != 1 {
		return nil
	}
	uns := muls[0].AllUnaryExpr()
	if len(uns) != 1 {
		return nil
	}
	return uns[0].PostfixExpr()
}

// primaryOfPrimaryExpr returns the PrimaryContext of a primaryExpr, or nil when
// the primaryExpr is a lambda / if-expression / partial-function alternative.
func primaryOfPrimaryExpr(pe grammar.IPrimaryExprContext) grammar.IPrimaryContext {
	if pe == nil {
		return nil
	}
	return pe.Primary()
}
