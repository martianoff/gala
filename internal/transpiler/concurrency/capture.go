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
// within the body — the caret the diagnostic points at.
//
// Paths and Whole record HOW the closure uses the variable, so the enforcement
// pass can be field-access-sensitive:
//
//   - Whole is true when the variable is ever used AS A VALUE: referenced bare,
//     passed as a function argument, returned/stored, used as a call callee, a
//     method receiver (`x.m(...)` — a method may touch mutable internals), an
//     index/`match` subject, or any form other than a pure field read. A Whole
//     use requires the variable's ENTIRE type be Shareable.
//   - Paths lists the distinct pure field-read access chains applied to the
//     variable (`x.a` → "a", `x.a.b` → "a.b"), i.e. selector chains NOT followed
//     by a call, index, or match. When the variable is never used Whole, only
//     these field paths need be Shareable — reading an immutable field of an
//     otherwise-unshareable value is genuinely race-free.
//
// A variable used both ways carries Whole=true (the conservative direction);
// the enforcement pass then ignores Paths and checks the whole type.
type Capture struct {
	Name  string
	Pos   transpiler.SourcePos
	Paths []string
	Whole bool
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
	index  map[string]int          // capture name -> its position in out (for accumulating usage info)
	parser *parser.AntlrGalaParser // re-parses interpolation embedded expressions
}

func newCaptureAnalyzer() *captureAnalyzer {
	return &captureAnalyzer{index: map[string]int{}, parser: parser.NewAntlrGalaParser()}
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

// ensureCapture returns the index of the capture named name, creating it (with
// its diagnostic position fixed to the first-seen token) if absent. Callers must
// have already confirmed name is unbound and not the blank identifier.
func (a *captureAnalyzer) ensureCapture(name string, tok antlr.Token) int {
	if idx, ok := a.index[name]; ok {
		return idx
	}
	a.out = append(a.out, Capture{Name: name, Pos: transpiler.PosFromToken(tok)})
	idx := len(a.out) - 1
	a.index[name] = idx
	return idx
}

// reference records a WHOLE-VALUE use of name — a bare reference, a call callee,
// a method receiver, an index/match subject, or a function argument. If name is
// not bound in any enclosing scope it is a capture; the first such use fixes its
// diagnostic position, and the capture is marked used-whole (which requires its
// entire type be Shareable at enforcement time).
func (a *captureAnalyzer) reference(name string, tok antlr.Token) {
	if name == "" || name == "_" {
		return
	}
	if a.bound(name) {
		return
	}
	idx := a.ensureCapture(name, tok)
	a.out[idx].Whole = true
}

// recordPath records a pure field-read access path (`x.a`, `x.a.b`) on a capture
// candidate. tok is the position of the BASE identifier (the caret target), and
// path is the dotted selector chain after it ("a", "a.b"). Distinct paths are
// de-duplicated. A capture accumulating only paths (never marked Whole) is
// checked field-sensitively.
func (a *captureAnalyzer) recordPath(name string, tok antlr.Token, path string) {
	if name == "" || name == "_" {
		return
	}
	if a.bound(name) {
		return
	}
	idx := a.ensureCapture(name, tok)
	for _, p := range a.out[idx].Paths {
		if p == path {
			return
		}
	}
	a.out[idx].Paths = append(a.out[idx].Paths, path)
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
	case *grammar.PostfixExprContext:
		a.walkPostfixExpr(n)
		return
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

// walkPostfixExpr is the field-access-sensitive handler for a
// `primaryExpr postfixSuffix* ('match' '{' … '}')?` chain. When the base is a
// bare value-position identifier (the capture candidate) it classifies HOW the
// identifier is used — a pure `.field` selector chain is recorded as a read PATH,
// anything else (a call, an index, a trailing `match`, or a bare reference) as a
// WHOLE use. Regardless of the base, the contents of every suffix (call
// arguments, index expressions) and every `match` arm are still walked so nested
// captures inside them are found.
func (a *captureAnalyzer) walkPostfixExpr(ctx grammar.IPostfixExprContext) {
	if ctx == nil {
		return
	}
	pe := ctx.PrimaryExpr()
	suffixes := ctx.AllPostfixSuffix()
	caseClauses := ctx.AllCaseClause()

	if id := bareIdentPrimary(pe); id != nil {
		name := id.GetText()
		tok := id.GetStart()
		if path, whole := classifySuffixes(suffixes, len(caseClauses) > 0); whole {
			a.reference(name, tok)
		} else {
			a.recordPath(name, tok, path)
		}
	} else {
		// Base is not a bare identifier (literal, tuple/paren, composite literal,
		// lambda, if-expression, partial function): recurse into it normally.
		a.walk(pe)
	}

	// Walk the contents of each suffix (a call's arguments, an index's
	// expressions); selector identifiers carry no reference and are ignored.
	for _, s := range suffixes {
		a.walkPostfixSuffix(s)
	}
	// A trailing `match { … }` — walk each arm for its own captures.
	for _, cc := range caseClauses {
		a.walkCaseClause(cc)
	}
}

// classifySuffixes decides whether a bare-identifier base followed by the given
// postfix suffixes (and an optional trailing `match`) is a pure field-read PATH
// or a WHOLE use. A leading, uninterrupted run of `.field` selectors with no
// call, index, or match is a field-read path (the dotted chain, e.g. "a.b"); an
// empty suffix list (a bare reference), a trailing match, or any call/index makes
// it a whole use. Anything it cannot classify is conservatively a whole use.
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
		// A non-selector suffix: an index `[...]` or a call `(...)`.
		if sc.ExpressionList() != nil {
			// Index `[...]` — a value-position use of the whole receiver.
			return "", true
		}
		// A call `(...)`. Tolerate a SINGLE trailing method call on a field-path
		// receiver: `x.a.b.m(...)` reads the immutable field value `x.a.b` and
		// calls a method on it. The method runs on that (shareable) value, so it
		// cannot race — the enforcement pass verifies safety via the receiver
		// path's leaf type exactly as for a plain field read. The last selector
		// is the method name, so the receiver path is the rest; at least one
		// selector must remain (else it is a method on the whole captured value,
		// which stays a whole use). The call's arguments are walked separately by
		// the caller, so captures inside them are still found.
		if i == len(suffixes)-1 && len(parts) >= 2 {
			return strings.Join(parts[:len(parts)-1], "."), false
		}
		return "", true
	}
	return strings.Join(parts, "."), false
}

// bareIdentPrimary returns the identifier context when the primaryExpr is a bare
// value-position identifier (`x`), or nil for any other primaryExpr shape (a
// literal/tuple/composite primary, a lambda, an if-expression, or a partial
// function). Such a bare identifier is the capture candidate a postfix chain
// applies to.
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
