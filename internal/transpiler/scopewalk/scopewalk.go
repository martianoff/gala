// Package scopewalk is the shared lexical-scope walker over a GALA parse tree.
//
// It answers one question — "which value-position identifiers does this code
// reference that its own scopes do not bind?" — and hands each answer to a
// Visitor. Two passes are built on it:
//
//   - the concurrency capture analysis, which turns the unbound references of a
//     closure into its free-variable (capture) set, and
//   - the analyzer's undefined-symbol check, which asks whether each unbound
//     reference resolves to anything the compilation knows about.
//
// Both need exactly the same notion of "a value-position identifier that no
// enclosing binder introduced", so they share this walk rather than each
// carrying its own. The differences between them are narrow and explicit — see
// Options.
//
// SCOPE MODEL. An explicit scope stack (innermost last) is pushed by every
// binding-introducing construct: lambda and function parameters, blocks,
// `val`/`var`/`:=` declarations, `bind`/`also`/`use` bindings, `for` loop
// variables, `if init;` statements, nested function declarations, and
// `match` / partial-function case patterns. Initializers are walked BEFORE the
// name they bind, so `val x = x + 1` sees the outer `x`.
//
// COMPLETENESS. The walk is complete by construction: nodes that affect scoping
// or contribute references have precise handlers, and every other node falls
// through to a generic child recursion, so no subtree is ever silently skipped
// and the analysis stays correct as the grammar grows.
//
// VALUE POSITIONS ONLY. References are emitted from exactly two places — a bare
// identifier `primary`, and the bare-identifier base of a postfix chain. Type
// positions therefore contribute nothing: a signature's parameter types, a
// `val`'s type annotation, a composite literal's type and a lambda's return
// type are never walked as values. TypeContext is additionally short-circuited
// so a future grammar change cannot leak a type name into the reference stream.
//
// STRING INTERPOLATION. `s"…$counter"` and `f"${x + y}"` are SINGLE lexer
// tokens, so their embedded expressions are not child nodes. With
// Options.ParseInterpolations set, each embedded expression is re-parsed and
// walked in the current scope, so a name referenced only inside an
// interpolation is seen like any other reference.
package scopewalk

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
)

// Use records HOW a reference is used, so a consumer that cares can be
// field-access-sensitive.
//
//   - Whole is true when the name is used AS A VALUE: referenced bare, passed
//     as an argument, used as a call callee, a method receiver, an index or
//     `match` subject — anything other than a pure field read.
//   - Path is the dotted pure field-read chain applied to the name (`x.a.b` →
//     "a.b"), set only when Whole is false.
//
// A consumer that only needs "was this name referenced at all" ignores both.
type Use struct {
	Path  string
	Whole bool
}

// Visitor receives the walk's output.
type Visitor interface {
	// Reference reports a value-position identifier that no scope entered by
	// the walk binds. The blank identifier is never reported. A name used
	// several ways is reported once per use, so consumers that aggregate must
	// de-duplicate.
	Reference(name string, tok antlr.Token, use Use)
}

// Options carries the narrow behavioural differences between the passes built
// on this walker. The zero value is the conservative, over-approximating shape
// the capture analysis wants: report more rather than miss one.
type Options struct {
	// ParseInterpolations re-parses the embedded expressions of interpolated
	// (`s"…"`) and format (`f"…"`) string literals and walks them in the
	// current scope. Off by default because it costs a parse per literal.
	ParseInterpolations bool

	// RequireCleanInterpolationParse drops an embedded expression whose
	// re-parse reported errors, instead of walking ANTLR's error-recovered
	// tree — so a pass that reports on unbound names cannot turn parser
	// recovery into a diagnostic. It is a narrow guard, not a well-formedness
	// gate: the expression parser is not anchored at end-of-input, so a
	// fragment with trailing garbage (`${x +}`) parses its longest valid
	// prefix without error and is walked by either setting.
	RequireCleanInterpolationParse bool

	// SkipCompositeLiteralKeys omits the key half of a keyed composite-literal
	// element (`T{Field: v}` walks `v` but not `Field`). A struct literal's key
	// is a field name rather than a value, so a pass that must not invent
	// references sets this; a pass that would rather over-report leaves it off.
	SkipCompositeLiteralKeys bool

	// BindWholePattern makes a `case` pattern bind EVERY identifier it
	// mentions, constructor names included, instead of binding only the names
	// the pattern introduces. It is the blunt, zero-false-positive fallback for
	// a pass that cannot tolerate a spurious reference; leaving it off gives
	// the precise split, where `case Some(x)` binds `x` and references `Some`.
	BindWholePattern bool

	// SkipTypedPatternArgument suppresses the reference a `x: T` pattern in
	// ARGUMENT position would otherwise contribute. The form is vanishingly
	// rare there and its identifier is not clearly a value.
	SkipTypedPatternArgument bool

	// EnterFunctionScope, when set, is called after the walker pushes the scope
	// for a function declaration and before its parameters are bound. It is the
	// hook for binders this walker does not model itself — type parameters and
	// a method receiver, which only the analyzer's pass needs.
	EnterFunctionScope func(w *Walker, fn grammar.IFunctionDeclarationContext)
}

// Walker holds the scope stack and drives the traversal.
type Walker struct {
	visitor Visitor
	opts    Options
	scopes  []map[string]bool

	// interpParser re-parses interpolation bodies; created on first use so a
	// consumer that leaves ParseInterpolations off never pays for it.
	interpParser *parser.AntlrGalaParser

	// posOverride, when set, replaces the token reported for a reference. A
	// re-parsed interpolation fragment is its own token stream, so its tokens
	// carry positions relative to the FRAGMENT, not the file — reporting one
	// verbatim points the caret at line 1, column 1 of the source. While a
	// fragment is being walked this holds the enclosing string literal's
	// token, so a reference inside `s"…"` is attributed to the literal that
	// contains it.
	posOverride antlr.Token
}

// New returns a Walker that reports to v.
func New(v Visitor, opts Options) *Walker {
	return &Walker{visitor: v, opts: opts}
}

// ---------------------------------------------------------------------------
// Scope stack
// ---------------------------------------------------------------------------

// PushScope opens a nested lexical scope.
func (w *Walker) PushScope() { w.scopes = append(w.scopes, map[string]bool{}) }

// PopScope closes the innermost scope.
func (w *Walker) PopScope() {
	if len(w.scopes) > 0 {
		w.scopes = w.scopes[:len(w.scopes)-1]
	}
}

// Bind records name as locally bound in the innermost scope, so a later
// reference resolves to it instead of being reported.
func (w *Walker) Bind(name string) {
	if name == "" || name == "_" {
		return
	}
	if len(w.scopes) == 0 {
		w.PushScope()
	}
	w.scopes[len(w.scopes)-1][name] = true
}

// BindID binds the text of an identifier context, if present.
func (w *Walker) BindID(id grammar.IIdentifierContext) {
	if id != nil {
		w.Bind(id.GetText())
	}
}

// BindIdentifierList binds every name in an identifier list.
func (w *Walker) BindIdentifierList(idList grammar.IIdentifierListContext) {
	if idList == nil {
		return
	}
	for _, id := range idList.AllIdentifier() {
		w.Bind(id.GetText())
	}
}

// BindIdentifiersIn binds every `identifier` leaf under node. It is the blunt
// instrument for a construct whose binder/reference split the walker does not
// model — a method receiver's type arguments (`func (s Stack[T]) …` introduces
// T), or a whole `case` pattern under Options.BindWholePattern. Over-binding
// can only mask a reference, never invent one.
func (w *Walker) BindIdentifiersIn(node antlr.Tree) {
	for _, name := range identifiersUnder(node) {
		w.Bind(name)
	}
}

// Bound reports whether name is bound in any enclosing scope.
func (w *Walker) Bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

// BindParameters binds each named parameter of a parameter list. Type-only
// parameters (function-type positions with no identifier) bind nothing.
func (w *Walker) BindParameters(params grammar.IParametersContext) {
	if params == nil {
		return
	}
	pl := params.ParameterList()
	if pl == nil {
		return
	}
	for _, p := range pl.AllParameter() {
		if pc, ok := p.(*grammar.ParameterContext); ok {
			w.BindID(pc.Identifier())
		}
	}
}

// WalkParameterDefaults walks the `= expr` default of each parameter, so a
// reference inside a default value is seen.
func (w *Walker) WalkParameterDefaults(params grammar.IParametersContext) {
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
				w.Walk(def.Expression())
			}
		}
	}
}

// reference emits an unbound value-position reference. Inside a re-parsed
// interpolation fragment the reported token is the enclosing string literal —
// see posOverride.
func (w *Walker) reference(name string, tok antlr.Token, use Use) {
	if name == "" || name == "_" || w.Bound(name) {
		return
	}
	if w.posOverride != nil {
		tok = w.posOverride
	}
	w.visitor.Reference(name, tok, use)
}

// ---------------------------------------------------------------------------
// Generic dispatcher
// ---------------------------------------------------------------------------

// Walk descends an arbitrary parse-tree node. Nodes that affect scoping or
// contribute references have precise handlers (dispatched here, returning
// without generic recursion so nothing is double-walked); every other node
// falls to the default branch, which recurses into all children.
func (w *Walker) Walk(node antlr.Tree) {
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
		w.WalkLambda(n)
		return
	case *grammar.BlockContext:
		w.WalkBlock(n)
		return
	case *grammar.ForStatementContext:
		w.walkForStatement(n)
		return
	case *grammar.IfStatementContext:
		w.walkIfStatement(n)
		return
	case *grammar.FunctionDeclarationContext:
		w.WalkFunctionDeclaration(n)
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
				w.Walk(e)
			}
		}
		w.BindIdentifierList(n.IdentifierList())
		return
	case *grammar.BindDeclarationContext:
		w.Walk(n.Expression())
		w.BindID(n.Identifier())
		return
	case *grammar.AlsoDeclarationContext:
		w.Walk(n.Expression())
		w.BindID(n.Identifier())
		return
	case *grammar.UseDeclarationContext:
		w.Walk(n.Expression())
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
		w.Walk(node.GetChild(i))
	}
}

// ---------------------------------------------------------------------------
// Scope-introducing handlers
// ---------------------------------------------------------------------------

// WalkLambda pushes a fresh scope, binds the lambda's parameters into it, walks
// the body, and pops — so a nested lambda's parameters shadow only within its
// own scope.
func (w *Walker) WalkLambda(lambda grammar.ILambdaExpressionContext) {
	if lambda == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()

	w.BindParameters(lambda.Parameters())
	// A parameter default (`(x = n) => …`) may reference a sibling parameter,
	// so defaults are walked after the names are bound.
	w.WalkParameterDefaults(lambda.Parameters())

	if expr := lambda.Expression(); expr != nil {
		w.Walk(expr)
	} else if block := lambda.Block(); block != nil {
		w.WalkBlock(block)
	}
	// lambda.Type_() is the optional return-type annotation — a type.
}

// WalkBlock introduces a nested scope and walks the block's statements in
// order, so a local declared partway through shadows an enclosing name only for
// references that follow it.
func (w *Walker) WalkBlock(block grammar.IBlockContext) {
	if block == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()
	for _, s := range block.AllStatement() {
		w.Walk(s)
	}
}

// walkValVarDecl walks a `val`/`var` initializer and then binds the declared
// name(s) — RHS first, in the scope that predates the binding.
func (w *Walker) walkValVarDecl(exprList grammar.IExpressionListContext, idList grammar.IIdentifierListContext, tuple grammar.ITuplePatternContext) {
	if exprList != nil {
		for _, e := range exprList.AllExpression() {
			w.Walk(e)
		}
	}
	w.BindIdentifierList(idList)
	if tuple != nil {
		w.BindIdentifierList(tuple.IdentifierList())
	}
}

// WalkFunctionDeclaration handles a `func f(...) = body` (or block-bodied)
// declaration. The name is bound in the ENCLOSING scope, so the function may
// recurse and later statements may call it; its parameters live in a fresh
// nested scope covering only its body. Options.EnterFunctionScope runs inside
// that scope, before the parameters bind, for binders this walker does not
// model (type parameters, a method receiver).
func (w *Walker) WalkFunctionDeclaration(fn grammar.IFunctionDeclarationContext) {
	if fn == nil {
		return
	}
	w.BindID(fn.Identifier())
	w.PushScope()
	defer w.PopScope()
	if w.opts.EnterFunctionScope != nil {
		w.opts.EnterFunctionScope(w, fn)
	}
	if sig := fn.Signature(); sig != nil {
		w.BindParameters(sig.Parameters())
		w.WalkParameterDefaults(sig.Parameters())
	}
	if block := fn.Block(); block != nil {
		w.WalkBlock(block)
	} else if e := fn.Expression(); e != nil {
		w.Walk(e)
	}
}

func (w *Walker) walkIfStatement(ifs grammar.IIfStatementContext) {
	// An `if init; cond { … }` init binding is visible in the condition and
	// both branches but NOT after the statement, so it gets a scope wrapping
	// the whole statement.
	w.PushScope()
	defer w.PopScope()
	if ss := ifs.SimpleStatement(); ss != nil {
		w.Walk(ss)
	}
	if cond := ifs.Expression(); cond != nil {
		w.Walk(cond)
	}
	for _, b := range ifs.AllBlock() {
		w.WalkBlock(b)
	}
	if elseIf := ifs.IfStatement(); elseIf != nil {
		w.walkIfStatement(elseIf)
	}
}

func (w *Walker) walkForStatement(fs grammar.IForStatementContext) {
	// Loop variables live in a scope that wraps the loop body.
	w.PushScope()
	defer w.PopScope()

	if fc := fs.ForClause(); fc != nil {
		// forClause: simpleStatement? ';' expression? ';' simpleStatement?
		for _, ss := range fc.AllSimpleStatement() {
			w.Walk(ss)
		}
		if e := fc.Expression(); e != nil {
			w.Walk(e)
		}
	} else if rc := fs.RangeClause(); rc != nil {
		// rangeClause: (identifierList (':=' | '='))? 'range' expression
		if e := rc.Expression(); e != nil {
			w.Walk(e) // the ranged value resolves before the loop vars bind
		}
		if idList := rc.IdentifierList(); idList != nil {
			if strings.Contains(rc.GetText(), ":=") {
				// `i, v := range xs` — fresh loop-variable bindings.
				w.BindIdentifierList(idList)
			} else {
				// `i, v = range xs` — assignment to existing variables.
				for _, id := range idList.AllIdentifier() {
					w.reference(id.GetText(), id.GetStart(), Use{Whole: true})
				}
			}
		}
	} else if cond := fs.ForCondition(); cond != nil {
		w.Walk(cond.Expression())
	}

	w.WalkBlock(fs.Block())
}

// ---------------------------------------------------------------------------
// Reference-contributing handlers
// ---------------------------------------------------------------------------

// walkPostfixExpr handles a `primaryExpr postfixSuffix* ('match' … )?` chain.
// When the base is a bare value-position identifier it classifies HOW the name
// is used — a pure `.field` selector chain as a read path, anything else as a
// whole use. Regardless of the base, the contents of every suffix and every
// `match` arm are walked so nested references are still found.
func (w *Walker) walkPostfixExpr(ctx grammar.IPostfixExprContext) {
	if ctx == nil {
		return
	}
	pe := ctx.PrimaryExpr()
	suffixes := ctx.AllPostfixSuffix()
	caseClauses := ctx.AllCaseClause()

	if id := bareIdentPrimary(pe); id != nil {
		path, whole := classifySuffixes(suffixes, len(caseClauses) > 0)
		w.reference(id.GetText(), id.GetStart(), Use{Path: path, Whole: whole})
	} else {
		// Base is not a bare identifier (literal, tuple/paren, composite
		// literal, lambda, if-expression, partial function): recurse into it.
		w.Walk(pe)
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

func (w *Walker) walkPrimary(ctx grammar.IPrimaryContext) {
	if ctx == nil {
		return
	}
	if id := ctx.Identifier(); id != nil {
		w.reference(id.GetText(), id.GetStart(), Use{Whole: true})
		return
	}
	if lit := ctx.Literal(); lit != nil {
		w.walkLiteral(lit)
		return
	}
	if tup := ctx.TupleExpressionList(); tup != nil {
		for _, e := range tup.AllExpression() {
			w.Walk(e)
		}
		return
	}
	if cl := ctx.CompositeLiteral(); cl != nil {
		w.walkCompositeLiteral(cl)
	}
}

// walkLiteral descends into an interpolated (`s"…"`) or format (`f"…"`) string.
// Such a literal is a single token whose embedded `${…}` / `$name` expressions
// are NOT parse-tree children, so each is re-parsed and walked in the CURRENT
// scope. Every other literal contributes no references.
//
// A re-parsed fragment is its own token stream: its tokens' line/column are
// relative to the fragment, so reporting one verbatim would put a caret on line
// 1 of the file. For the duration of the walk every reference is therefore
// attributed to the string literal token itself, which is the position a reader
// needs — the literal that contains the offending name. Resolving to the exact
// column inside the literal would need Split to preserve each part's span in
// the original text, which it does not (it unescapes as it goes).
func (w *Walker) walkLiteral(lit grammar.ILiteralContext) {
	if !w.opts.ParseInterpolations {
		return
	}
	var tok antlr.TerminalNode
	if t := lit.INTERPOLATED_STRING(); t != nil {
		tok = t
	} else if t := lit.FORMAT_STRING(); t != nil {
		tok = t
	} else {
		return
	}
	raw := tok.GetText()
	if len(raw) < 3 {
		return
	}
	if w.interpParser == nil {
		w.interpParser = parser.NewAntlrGalaParser()
	}

	prev := w.posOverride
	w.posOverride = tok.GetSymbol()
	defer func() { w.posOverride = prev }()

	// Strip the `s"`/`f"` prefix and the closing `"`, matching the transformer.
	content := raw[2 : len(raw)-1]
	for _, part := range interpolation.Split(content) {
		if part.IsLiteral {
			continue
		}
		expr, errs := w.interpParser.ParseExpression(part.Text)
		if expr == nil {
			continue
		}
		if len(errs) > 0 && w.opts.RequireCleanInterpolationParse {
			continue
		}
		w.Walk(expr)
	}
}

// walkCompositeLiteral walks the element values of a composite literal. The
// literal's type is skipped; each keyed element's value is a value position.
// The key half is a field name in a struct literal, so
// Options.SkipCompositeLiteralKeys controls whether it is walked too.
func (w *Walker) walkCompositeLiteral(ctx grammar.ICompositeLiteralContext) {
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
			w.Walk(e)
		}
	}
}

func (w *Walker) walkPostfixSuffix(ctx grammar.IPostfixSuffixContext) {
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
			w.Walk(e)
		}
	}
}

func (w *Walker) walkArgument(arg grammar.IArgumentContext) {
	if arg == nil {
		return
	}
	// A leading `identifier =` is a named-argument LABEL, not a reference.
	if lambda := arg.LambdaExpression(); lambda != nil {
		w.WalkLambda(lambda)
		return
	}
	// In argument position the `pattern` rule is used as a VALUE expression.
	if pat := arg.Pattern(); pat != nil {
		w.walkPatternAsValue(pat)
	}
}

// walkPatternAsValue walks a `pattern` node in value position (a call
// argument), treating its identifiers as references rather than bindings.
func (w *Walker) walkPatternAsValue(pat grammar.IPatternContext) {
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		w.Walk(p.Expression())
	case *grammar.RestPatternContext:
		w.Walk(p.Expression()) // spread: `xs...`
	case *grammar.TypedPatternContext:
		if w.opts.SkipTypedPatternArgument {
			return
		}
		if id := p.Identifier(); id != nil {
			w.reference(id.GetText(), id.GetStart(), Use{Whole: true})
		}
	}
}

// ---------------------------------------------------------------------------
// Match / partial-function case clauses
// ---------------------------------------------------------------------------

func (w *Walker) walkCaseClause(cc grammar.ICaseClauseContext) {
	if cc == nil {
		return
	}
	w.PushScope()
	defer w.PopScope()

	// Pattern bindings (`case Some(x)` binds x) are in scope for guard & body.
	w.bindPattern(cc.Pattern())

	if guard := cc.GetGuard(); guard != nil {
		w.Walk(guard)
	}
	if block := cc.GetBodyBlock(); block != nil {
		w.WalkBlock(block)
	} else if stmt := cc.GetBodyStmt(); stmt != nil {
		w.Walk(stmt)
	}
}

// bindPattern binds the names a case-clause pattern introduces. Constructor and
// extractor names are NOT bound — they are references, resolved like any other
// name — and their sub-patterns are recursed into. Under
// Options.BindWholePattern every identifier in the pattern is bound instead.
func (w *Walker) bindPattern(pat grammar.IPatternContext) {
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
func (w *Walker) bindPatternExpression(expr grammar.IExpressionContext) {
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
