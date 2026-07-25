package concurrency

// Capture (free-variable) analysis — PR2 of the compile-time data-race-safety
// work.
//
// Given a GALA closure (either an explicit lambda or the desugared thunk of a
// by-name expression such as `Future(counter + 1)`), FreeVariables computes the
// set of identifiers the closure references from its ENCLOSING scope — the names
// it "captures". It is a pure, syntactic tree walk over the ANTLR parse tree
// (exactly like the transformer), with an explicit lexical scope stack. It does
// not touch the analyzer, the type system, or any transformer state, and it
// never mutates the AST.
//
// A capture is a VALUE-POSITION identifier referenced in the body that is not
// bound within the closure's own scope. Bindings that exclude a name:
//   - the closure's own parameters;
//   - `val` / `var` declarations inside the body;
//   - `bind` / `also` do-notation bindings and `use` resource bindings;
//   - `for` loop variables (`:=` range vars and C-style init vars);
//   - `match` / partial-function case-pattern bindings (`case Some(x)` binds x);
//   - a nested lambda's parameters (which shadow only within the nested scope).
//
// COMPLETENESS. The walk is complete by construction: the precise handlers below
// cover every binding-introducing construct and every identifier reference, and
// ANY parse-tree node without a precise handler falls through to a GENERIC child
// recursion (walk's default branch) that descends into all children. So no
// subtree — hence no reference within it — is ever silently skipped, and the
// analysis stays correct as the grammar grows. This deliberately OVER-approximates
// (it reports spurious frees, e.g. the callee of a bare `foo(...)` call or a
// bracketed type argument, rather than miss a genuine capture — the safe
// direction). PR3 classifies each reported capture (resolving its `val`/`var`
// kind and type and enforcing shareability); a name that resolves to a top-level
// function or a type harmlessly falls out as non-local.
//
// String interpolation (`s"…$counter"`, `f"${x + y}"`) is a SINGLE lexer token,
// so its embedded expressions are not child nodes. walkLiteral re-parses each
// embedded expression (via the shared interpolation splitter + the project
// parser) and walks it through the same machinery, so a variable referenced only
// inside an interpolation is captured too.
//
// NOTE: like the Shareable predicate (PR1), this pass is wired into no
// enforcement. It is called only by its tests. PR3 consumes it.

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Capture is a single free variable a closure references from its enclosing
// scope. Pos is the position of the FIRST value-position reference to the name
// within the body — the caret PR3's diagnostic points at.
type Capture struct {
	Name string
	Pos  transpiler.SourcePos
}

// FreeVariablesInLambda returns the free variables captured by an explicit
// lambda. The lambda's declared parameters are bound (never captured); its body
// may be an expression (`(x) => x + n`) or a block (`(x) => { ... }`).
func FreeVariablesInLambda(lambda grammar.ILambdaExpressionContext) []Capture {
	a := newCaptureAnalyzer()
	a.walkLambda(lambda)
	return a.out
}

// FreeVariablesInExpression returns the free variables of a bare expression —
// the shape a by-name / thunk argument desugars to. `Future(counter + 1)` is
// analyzed by calling this with the expression `counter + 1` and no params,
// which is identical to analyzing the explicit thunk `() => counter + 1`.
//
// paramNames lets a caller seed additional bound names (e.g. an implicit
// parameter of the thunk); pass nil/empty for the common Future/Spawn thunk.
func FreeVariablesInExpression(paramNames []string, expr grammar.IExpressionContext) []Capture {
	a := newCaptureAnalyzer()
	a.pushScope()
	for _, p := range paramNames {
		a.bind(p)
	}
	a.walk(expr)
	a.popScope()
	return a.out
}

// captureAnalyzer holds the walk state: an explicit lexical scope stack
// (innermost scope last), the accumulated captures in first-seen order, and a
// name set that de-duplicates them (keeping the first position).
type captureAnalyzer struct {
	scopes []map[string]bool
	out    []Capture
	seen   map[string]bool
	parser *parser.AntlrGalaParser // re-parses interpolation embedded expressions
}

func newCaptureAnalyzer() *captureAnalyzer {
	return &captureAnalyzer{seen: map[string]bool{}, parser: parser.NewAntlrGalaParser()}
}

func (a *captureAnalyzer) pushScope() {
	a.scopes = append(a.scopes, map[string]bool{})
}

func (a *captureAnalyzer) popScope() {
	a.scopes = a.scopes[:len(a.scopes)-1]
}

// bind records name as locally bound in the innermost scope. A later reference
// to name resolves to this local rather than being reported as a capture.
func (a *captureAnalyzer) bind(name string) {
	if name == "" || name == "_" {
		return
	}
	if len(a.scopes) == 0 {
		a.pushScope()
	}
	a.scopes[len(a.scopes)-1][name] = true
}

// bindID binds the text of an identifier context, if present.
func (a *captureAnalyzer) bindID(id grammar.IIdentifierContext) {
	if id != nil {
		a.bind(id.GetText())
	}
}

// bound reports whether name is bound in any enclosing scope on the stack.
func (a *captureAnalyzer) bound(name string) bool {
	for i := len(a.scopes) - 1; i >= 0; i-- {
		if a.scopes[i][name] {
			return true
		}
	}
	return false
}

// reference records a value-position use of name. If name is not bound in any
// enclosing scope it is a capture; the first such use fixes its diagnostic
// position and later uses of the same name are de-duplicated.
func (a *captureAnalyzer) reference(name string, tok antlr.Token) {
	if name == "" || name == "_" {
		return
	}
	if a.bound(name) || a.seen[name] {
		return
	}
	a.seen[name] = true
	a.out = append(a.out, Capture{Name: name, Pos: transpiler.PosFromToken(tok)})
}

// ---------------------------------------------------------------------------
// Generic dispatcher
// ---------------------------------------------------------------------------

// walk is the single entry point for descending an arbitrary parse-tree node.
// Nodes that affect scoping or contribute references have PRECISE handlers
// (dispatched here and returning without generic recursion, so nothing is
// double-walked); every other node falls to the default branch, which recurses
// into all children so no subtree is ever skipped.
func (a *captureAnalyzer) walk(node antlr.Tree) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	// Scope-introducing constructs.
	case *grammar.LambdaExpressionContext:
		a.walkLambda(n)
		return
	case *grammar.BlockContext:
		a.walkBlock(n)
		return
	case *grammar.ForStatementContext:
		a.walkForStatement(n)
		return
	case *grammar.IfStatementContext:
		a.walkIfStatement(n)
		return
	case *grammar.FunctionDeclarationContext:
		a.walkNestedFunction(n)
		return
	case *grammar.CaseClauseContext:
		a.walkCaseClause(n)
		return
	case *grammar.PartialFunctionLiteralContext:
		for _, cc := range n.AllCaseClause() {
			a.walkCaseClause(cc)
		}
		return

	// Order-dependent local bindings: walk the initializer first (in the
	// pre-binding scope), then bind, so `val x = x + 1` captures the outer x.
	case *grammar.ValDeclarationContext:
		a.walkValVarDecl(n.ExpressionList(), n.IdentifierList(), n.TuplePattern())
		return
	case *grammar.VarDeclarationContext:
		a.walkValVarDecl(n.ExpressionList(), n.IdentifierList(), n.TuplePattern())
		return
	case *grammar.ShortVarDeclContext:
		if el := n.ExpressionList(); el != nil {
			for _, e := range el.AllExpression() {
				a.walk(e)
			}
		}
		a.bindIdentifierList(n.IdentifierList())
		return
	case *grammar.BindDeclarationContext:
		a.walk(n.Expression())
		a.bindID(n.Identifier())
		return
	case *grammar.AlsoDeclarationContext:
		a.walk(n.Expression())
		a.bindID(n.Identifier())
		return
	case *grammar.UseDeclarationContext:
		a.walk(n.Expression())
		a.bindID(n.Identifier())
		return

	// Reference-contributing leaves.
	case *grammar.PrimaryContext:
		a.walkPrimary(n)
		return
	case *grammar.PostfixSuffixContext:
		a.walkPostfixSuffix(n)
		return
	case *grammar.ArgumentContext:
		a.walkArgument(n)
		return
	}

	// Default: no precise handler — recurse into every child so unhandled
	// forms still surface their references (over-approximating, never missing).
	for i := 0; i < node.GetChildCount(); i++ {
		a.walk(node.GetChild(i))
	}
}

// ---------------------------------------------------------------------------
// Scope-introducing handlers
// ---------------------------------------------------------------------------

// walkLambda pushes a fresh scope, binds the lambda's parameters into it, walks
// the body, and pops. Used both for the top-level analyzed lambda and for every
// nested lambda encountered during the walk — so a nested lambda's params shadow
// only within its own scope, and a name bound by an OUTER (still-on-stack) lambda
// param is not reported as a capture.
func (a *captureAnalyzer) walkLambda(lambda grammar.ILambdaExpressionContext) {
	if lambda == nil {
		return
	}
	a.pushScope()
	defer a.popScope()

	a.bindParameters(lambda.Parameters())
	// A parameter default (`(x = counter) => …`) references the enclosing scope;
	// walk defaults after binding the param names so they see sibling params.
	a.walkParameterDefaults(lambda.Parameters())

	if expr := lambda.Expression(); expr != nil {
		a.walk(expr)
	} else if block := lambda.Block(); block != nil {
		a.walkBlock(block)
	}
	// lambda.Type_() is the optional return-type annotation — a type, not a value.
}

// bindParameters binds each named parameter of a parameter list. Type-only
// parameters (function-type positions with no identifier) bind nothing.
func (a *captureAnalyzer) bindParameters(params grammar.IParametersContext) {
	if params == nil {
		return
	}
	pl := params.ParameterList()
	if pl == nil {
		return
	}
	for _, p := range pl.AllParameter() {
		if pc, ok := p.(*grammar.ParameterContext); ok {
			a.bindID(pc.Identifier())
		}
	}
}

// walkParameterDefaults walks the `= expr` default of each parameter, so a
// capture inside a default value is detected.
func (a *captureAnalyzer) walkParameterDefaults(params grammar.IParametersContext) {
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
				a.walk(def.Expression())
			}
		}
	}
}

// walkBlock introduces a nested lexical scope and walks the block's statements
// in order, so a local declared partway through shadows the enclosing name only
// for references that follow it.
func (a *captureAnalyzer) walkBlock(block grammar.IBlockContext) {
	if block == nil {
		return
	}
	a.pushScope()
	defer a.popScope()
	for _, s := range block.AllStatement() {
		a.walk(s)
	}
}

// walkValVarDecl walks a `val`/`var` initializer and then binds the declared
// name(s). The RHS is walked FIRST, in the scope that predates the binding, so
// `val x = x + 1` captures the outer x while later references resolve to the new
// local (correct lexical shadowing).
func (a *captureAnalyzer) walkValVarDecl(exprList grammar.IExpressionListContext, idList grammar.IIdentifierListContext, tuple grammar.ITuplePatternContext) {
	if exprList != nil {
		for _, e := range exprList.AllExpression() {
			a.walk(e)
		}
	}
	a.bindIdentifierList(idList)
	if tuple != nil {
		a.bindIdentifierList(tuple.IdentifierList())
	}
}

func (a *captureAnalyzer) bindIdentifierList(idList grammar.IIdentifierListContext) {
	if idList == nil {
		return
	}
	for _, id := range idList.AllIdentifier() {
		a.bind(id.GetText())
	}
}

// walkNestedFunction handles a `func f(...) = body` (or block-bodied) declared
// inside a block. The function name is bound in the enclosing scope (so it may
// recurse and later statements may reference it); its parameters live in a fresh
// nested scope covering only its body.
func (a *captureAnalyzer) walkNestedFunction(fn grammar.IFunctionDeclarationContext) {
	a.bindID(fn.Identifier())
	a.pushScope()
	defer a.popScope()
	if sig := fn.Signature(); sig != nil {
		a.bindParameters(sig.Parameters())
		a.walkParameterDefaults(sig.Parameters())
	}
	if block := fn.Block(); block != nil {
		a.walkBlock(block)
	} else if e := fn.Expression(); e != nil {
		a.walk(e)
	}
}

func (a *captureAnalyzer) walkIfStatement(ifs grammar.IIfStatementContext) {
	// An `if init; cond { … }` init binding is visible in the condition and both
	// branches but NOT after the statement, so it gets a scope wrapping the whole
	// statement (letting it fall to generic recursion would leak the binding).
	a.pushScope()
	defer a.popScope()
	if ss := ifs.SimpleStatement(); ss != nil {
		a.walk(ss)
	}
	if cond := ifs.Expression(); cond != nil {
		a.walk(cond)
	}
	for _, b := range ifs.AllBlock() {
		a.walkBlock(b)
	}
	if elseIf := ifs.IfStatement(); elseIf != nil {
		a.walkIfStatement(elseIf)
	}
}

func (a *captureAnalyzer) walkForStatement(fs grammar.IForStatementContext) {
	// Loop variables live in a scope that wraps the loop body.
	a.pushScope()
	defer a.popScope()

	if fc := fs.ForClause(); fc != nil {
		// forClause: simpleStatement? ';' expression? ';' simpleStatement?
		for _, ss := range fc.AllSimpleStatement() {
			a.walk(ss)
		}
		if e := fc.Expression(); e != nil {
			a.walk(e)
		}
	} else if rc := fs.RangeClause(); rc != nil {
		// rangeClause: (identifierList (':=' | '='))? 'range' expression
		if e := rc.Expression(); e != nil {
			a.walk(e) // the ranged value resolves before the loop vars bind
		}
		if idList := rc.IdentifierList(); idList != nil {
			if strings.Contains(rc.GetText(), ":=") {
				// `i, v := range xs` — fresh loop-variable bindings.
				a.bindIdentifierList(idList)
			} else {
				// `i, v = range xs` — assignment to existing (possibly captured) vars.
				for _, id := range idList.AllIdentifier() {
					a.reference(id.GetText(), id.GetStart())
				}
			}
		}
	} else if cond := fs.ForCondition(); cond != nil {
		a.walk(cond.Expression())
	}

	a.walkBlock(fs.Block())
}

// ---------------------------------------------------------------------------
// Reference-contributing handlers
// ---------------------------------------------------------------------------

func (a *captureAnalyzer) walkPrimary(ctx grammar.IPrimaryContext) {
	if ctx == nil {
		return
	}
	if id := ctx.Identifier(); id != nil {
		// A bare value-position identifier — the capture candidate.
		a.reference(id.GetText(), id.GetStart())
		return
	}
	if lit := ctx.Literal(); lit != nil {
		a.walkLiteral(lit)
		return
	}
	if tup := ctx.TupleExpressionList(); tup != nil {
		for _, e := range tup.AllExpression() {
			a.walk(e)
		}
		return
	}
	if cl := ctx.CompositeLiteral(); cl != nil {
		a.walkCompositeLiteral(cl)
	}
}

// walkLiteral handles literals. All but interpolated/format strings contribute
// no references. An interpolated (`s"…"`) or format (`f"…"`) string is a single
// token whose embedded `${…}` / `$name` expressions are NOT parse-tree children,
// so each embedded expression is re-parsed and walked in the CURRENT scope — a
// variable referenced only inside an interpolation is captured just like any
// other reference, and one that resolves to a local is not.
func (a *captureAnalyzer) walkLiteral(lit grammar.ILiteralContext) {
	var raw string
	if tok := lit.INTERPOLATED_STRING(); tok != nil {
		raw = tok.GetText()
	} else if tok := lit.FORMAT_STRING(); tok != nil {
		raw = tok.GetText()
	} else {
		return
	}
	if len(raw) < 3 {
		return
	}
	// Strip the `s"`/`f"` prefix and the closing `"`, matching the transformer.
	content := raw[2 : len(raw)-1]
	for _, part := range interpolation.Split(content) {
		if part.IsLiteral {
			continue
		}
		expr, _ := a.parser.ParseExpression(part.Text)
		if expr != nil {
			a.walk(expr) // best-effort even if the fragment had parse errors
		}
	}
}

// walkCompositeLiteral walks the element values of a composite literal. The
// literal's `type` (its Type_) is a type, not a value, and is skipped; each
// keyed element's expressions are value positions.
func (a *captureAnalyzer) walkCompositeLiteral(ctx grammar.ICompositeLiteralContext) {
	el := ctx.ElementList()
	if el == nil {
		return
	}
	for _, ke := range el.AllKeyedElement() {
		if kec, ok := ke.(*grammar.KeyedElementContext); ok {
			for _, e := range kec.AllExpression() {
				a.walk(e)
			}
		}
	}
}

func (a *captureAnalyzer) walkPostfixSuffix(ctx grammar.IPostfixSuffixContext) {
	if ctx == nil {
		return
	}
	// `.identifier` is a field/method selector on the preceding value — the
	// selector name is NOT a capture, so it is intentionally ignored here.
	if ctx.Identifier() != nil {
		return
	}
	if al := ctx.ArgumentList(); al != nil {
		for _, arg := range al.AllArgument() {
			a.walkArgument(arg)
		}
		return
	}
	// `[ expressionList ]` — index or explicit type args. Indexing exprs are value
	// references; bracketed type args are over-approximated as references (a type
	// name harmlessly resolves to non-local in PR3).
	if el := ctx.ExpressionList(); el != nil {
		for _, e := range el.AllExpression() {
			a.walk(e)
		}
	}
}

func (a *captureAnalyzer) walkArgument(arg grammar.IArgumentContext) {
	if arg == nil {
		return
	}
	// A leading `identifier =` is a named-argument LABEL (present only with `=`),
	// not a value reference — skip it.
	if lambda := arg.LambdaExpression(); lambda != nil {
		a.walkLambda(lambda)
		return
	}
	// In argument position the `pattern` rule is used as a VALUE expression
	// (references), not as a binding pattern.
	if pat := arg.Pattern(); pat != nil {
		a.walkPatternAsValue(pat)
	}
}

// walkPatternAsValue walks a `pattern` node that appears in value position (a
// call argument), treating its identifiers as references rather than bindings.
func (a *captureAnalyzer) walkPatternAsValue(pat grammar.IPatternContext) {
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		a.walk(p.Expression())
	case *grammar.RestPatternContext:
		a.walk(p.Expression()) // spread: `xs...`
	case *grammar.TypedPatternContext:
		// `x: T` in argument position is unusual; treat x as a reference.
		if id := p.Identifier(); id != nil {
			a.reference(id.GetText(), id.GetStart())
		}
	}
}

// ---------------------------------------------------------------------------
// Match / partial-function case clauses
// ---------------------------------------------------------------------------

func (a *captureAnalyzer) walkCaseClause(cc grammar.ICaseClauseContext) {
	if cc == nil {
		return
	}
	a.pushScope()
	defer a.popScope()

	// Pattern bindings (`case Some(x)` binds x) are in scope for the guard & body.
	a.bindPattern(cc.Pattern())

	if guard := cc.GetGuard(); guard != nil {
		a.walk(guard)
	}
	if block := cc.GetBodyBlock(); block != nil {
		a.walkBlock(block)
	} else if stmt := cc.GetBodyStmt(); stmt != nil {
		a.walk(stmt)
	}
}

// bindPattern extracts the binding names of a case-clause pattern (the opposite
// of walkPatternAsValue): the identifiers a pattern introduces, not the ones it
// references. Constructor/extractor names and their package qualifiers are NOT
// bound; their sub-patterns are recursed into.
func (a *captureAnalyzer) bindPattern(pat grammar.IPatternContext) {
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		a.bindPatternExpression(p.Expression())
	case *grammar.RestPatternContext:
		a.bindPatternExpression(p.Expression()) // `rest...` binds rest
	case *grammar.TypedPatternContext:
		a.bindID(p.Identifier()) // `x: T` binds x; T is a type
	}
}

// bindPatternExpression binds the names introduced by an expression-shaped
// pattern: a bare identifier binds itself; a tuple `(a, b)` binds each element's
// names; an extractor call `Ctor(sub…)` (or `pkg.Ctor(sub…)`, `Ctor[T](sub…)`)
// binds the sub-patterns' names but not the constructor. Literals bind nothing.
func (a *captureAnalyzer) bindPatternExpression(expr grammar.IExpressionContext) {
	pf := singlePostfixExpr(expr)
	if pf == nil {
		return
	}
	suffixes := pf.AllPostfixSuffix()
	if len(suffixes) == 0 {
		// No call/selector suffix: either a bare identifier binding or a
		// parenthesised (tuple) pattern.
		prim := primaryOfPrimaryExpr(pf.PrimaryExpr())
		if prim == nil {
			return
		}
		if id := prim.Identifier(); id != nil {
			a.bind(id.GetText())
			return
		}
		if tup := prim.TupleExpressionList(); tup != nil {
			for _, e := range tup.AllExpression() {
				a.bindPatternExpression(e)
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
					a.bindPattern(sub)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Small parse-tree navigation helpers
// ---------------------------------------------------------------------------

// singlePostfixExpr descends an expression that is a single, operator-free chain
// down to its postfixExpr, returning nil if the expression branches (a binary
// operator, which never occurs in the patterns this is used for).
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
