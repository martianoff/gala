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
// ON ADDING AN OPTION. Options is already at the edge of what a shared walker
// can carry before it stops being a walker and becomes a configuration object.
// Every current flag earns its place by marking a spot where the two passes
// genuinely disagree about what the LANGUAGE means — whether a composite
// literal's key is a value, whether a pattern's constructor is a reference —
// and each is set opposite ways by the two callers. Before adding a sixth, ask
// whether the new behaviour is instead:
//
//   - a property of the consumer, not the walk (decide it in Visitor.Reference,
//     which already sees the name, the token and the use), or
//   - something both passes would want (then just change the walk), or
//   - a binder this walker does not model (use EnterFunctionScope, or push a
//     scope and Bind before calling in).
//
// A flag that only one caller ever sets to a non-zero value, or that encodes a
// preference rather than a disagreement about meaning, belongs in the consumer.
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
	"github.com/antlr4-go/antlr/v4"

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
// Entry points
// ---------------------------------------------------------------------------

// Walk descends an arbitrary parse-tree node. Nodes that affect scoping or
// contribute references have precise handlers; every other node recurses into
// all its children.
func (w *Walker) Walk(node antlr.Tree) { antlrWalk{w}.walk(node) }

// WalkLambda pushes a fresh scope, binds the lambda's parameters into it, walks
// the body, and pops — so a nested lambda's parameters shadow only within its
// own scope.
func (w *Walker) WalkLambda(lambda grammar.ILambdaExpressionContext) {
	antlrWalk{w}.walkLambda(lambda)
}

// WalkBlock introduces a nested scope and walks the block's statements in
// order, so a local declared partway through shadows an enclosing name only for
// references that follow it.
func (w *Walker) WalkBlock(block grammar.IBlockContext) { antlrWalk{w}.walkBlock(block) }

// WalkFunctionDeclaration handles a `func f(...) = body` (or block-bodied)
// declaration. The name is bound in the ENCLOSING scope, so the function may
// recurse and later statements may call it; its parameters live in a fresh
// nested scope covering only its body. Options.EnterFunctionScope runs inside
// that scope, before the parameters bind, for binders this walker does not
// model (type parameters, a method receiver).
func (w *Walker) WalkFunctionDeclaration(fn grammar.IFunctionDeclarationContext) {
	antlrWalk{w}.walkFunctionDeclaration(fn)
}

// WalkParameterDefaults walks the `= expr` default of each parameter, so a
// reference inside a default value is seen.
func (w *Walker) WalkParameterDefaults(params grammar.IParametersContext) {
	antlrWalk{w}.walkParameterDefaults(params)
}
