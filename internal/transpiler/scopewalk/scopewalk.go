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
//     which already sees the name, the site and the use), or
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
//
// TWO TREES. The entry points take ANTLR contexts. Given a NativeWalker for
// their parse (WithNative), they walk galop's native tree instead (package
// nativewalk), which mirrors the walk over ANTLR's (walk_antlr.go) handler for
// handler; nativewalk's TestWalksAgree holds the two to the same output on the
// GALA sources the parser's engine test parses (std, the examples and the
// other libraries). ANTLR's walk stays for what has no native tree: a parse
// ANTLR's parser made (with ANTLR's engine, or on a syntax error), an
// interpolation fragment (re-parsed into ANTLR's tree alone), and a caller
// that holds none.
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

// Site is the token a reference is reported at: normally the identifier
// itself, but the whole string literal for a name inside an interpolation (see
// WalkInterpolated). It carries what a consumer reads off the token, whichever
// tree the walk ran on.
type Site struct {
	Line   int    // 1-based
	Column int    // 0-based, in code points
	Text   string // the token's text
}

// NoSite reports whether s is no site: the zero Site, for a name with no
// token to report it at (a context the transformer made up). A consumer that
// reports positions skips it.
func (s Site) NoSite() bool { return s == Site{} }

// Visitor receives the walk's output.
type Visitor interface {
	// Reference reports a value-position identifier that no scope entered by
	// the walk binds. The blank identifier is never reported. A name used
	// several ways is reported once per use, so consumers that aggregate must
	// de-duplicate.
	Reference(name string, at Site, use Use)
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

	// posOverride, when set, replaces the site reported for a reference. A
	// re-parsed interpolation fragment is its own token stream, so its tokens
	// carry positions relative to the FRAGMENT, not the file — reporting one
	// verbatim points the caret at line 1, column 1 of the source. While a
	// fragment is being walked this holds the enclosing string literal's
	// token, so a reference inside `s"…"` is attributed to the literal that
	// contains it.
	posOverride *Site

	// native walks the contexts of one parse on its native tree, or is nil
	// (see WithNative).
	native NativeWalker
}

// NativeWalker walks the ANTLR contexts of one parse on the parse's native
// tree, for a Walker. Package nativewalk makes one; this package stays free of
// the native tree.
type NativeWalker interface {
	// Walk is Walker.Walk of node on the native tree. It reports false, having
	// walked nothing, when node is no context of the parse.
	Walk(w *Walker, node antlr.Tree) bool
	// WalkParameterDefaults is Walker.WalkParameterDefaults likewise.
	WalkParameterDefaults(w *Walker, params antlr.Tree) bool
}

// New returns a Walker that reports to v.
func New(v Visitor, opts Options) *Walker {
	return &Walker{visitor: v, opts: opts}
}

// WithNative makes w walk on the native tree through nw, and returns w. An
// entry point given a context of nw's parse then walks the context's native
// node; one given any other context (an interpolation fragment's, another
// parse's), or a Walker with no NativeWalker (nw nil: no native tree, or the
// ANTLR-only build), walks ANTLR's tree. The two walks report the same
// references at the same sites.
func (w *Walker) WithNative(nw NativeWalker) *Walker {
	w.native = nw
	return w
}

// Options is w's options, for a NativeWalker.
func (w *Walker) Options() Options { return w.opts }

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

// Report emits an unbound value-position reference (for the walks, and a
// NativeWalker): a name no scope binds goes to the Visitor. Inside a re-parsed
// interpolation fragment the reported token is the enclosing string literal —
// see posOverride.
func (w *Walker) Report(name string, at Site, use Use) {
	if name == "" || name == "_" || w.Bound(name) {
		return
	}
	if w.posOverride != nil {
		at = *w.posOverride
	}
	w.visitor.Reference(name, at, use)
}

// WalkInterpolated walks the embedded expressions of lit, an interpolated
// (`s"…"`) or format (`f"…"`) string literal. Such a literal is a single
// token whose embedded `${…}` / `$name` expressions are NOT parse-tree
// children, so each is re-parsed and walked in the CURRENT scope.
//
// A re-parsed fragment is its own token stream: its tokens' line/column are
// relative to the fragment, so reporting one verbatim would put a caret on line
// 1 of the file. For the duration of the walk every reference is therefore
// attributed to the string literal token itself, which is the position a reader
// needs — the literal that contains the offending name. Resolving to the exact
// column inside the literal would need Split to preserve each part's span in
// the original text, which it does not (it unescapes as it goes).
//
// FOLLOW-UP: threading a byte offset (and length) for each embedded part
// through interpolation.Split would let a consumer add it to this token's
// start and point the caret at the offending name inside the literal rather
// than at the literal as a whole. The transformer would benefit too — it emits
// per-part Sprintf arguments and has the same coarse position. The obstacle is
// only that Split builds its parts by unescaping into a strings.Builder, so
// the mapping back to source columns is lost; recording spans as it scans is
// mechanical but touches a shared, well-tested splitter.
func (w *Walker) WalkInterpolated(lit Site) {
	raw := lit.Text
	if len(raw) < 3 {
		return
	}
	if w.interpParser == nil {
		w.interpParser = parser.NewAntlrGalaParser()
	}

	prev := w.posOverride
	w.posOverride = &lit
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
		// A fragment's parse is ANTLR's tree alone, whatever tree the
		// literal came from.
		antlrWalk{w}.walk(expr)
	}
}

// ---------------------------------------------------------------------------
// Entry points
// ---------------------------------------------------------------------------

// Walk descends an arbitrary parse-tree node. Nodes that affect scoping or
// contribute references have precise handlers; every other node recurses into
// all its children.
func (w *Walker) Walk(node antlr.Tree) {
	if w.native == nil || !w.native.Walk(w, node) {
		antlrWalk{w}.walk(node)
	}
}

// WalkLambda, WalkBlock and WalkFunctionDeclaration are Walk on a node of
// their kind, which Walk dispatches to its handler.

// WalkLambda pushes a fresh scope, binds the lambda's parameters into it, walks
// the body, and pops — so a nested lambda's parameters shadow only within its
// own scope.
func (w *Walker) WalkLambda(lambda grammar.ILambdaExpressionContext) { w.Walk(lambda) }

// WalkBlock introduces a nested scope and walks the block's statements in
// order, so a local declared partway through shadows an enclosing name only for
// references that follow it.
func (w *Walker) WalkBlock(block grammar.IBlockContext) { w.Walk(block) }

// WalkFunctionDeclaration handles a `func f(...) = body` (or block-bodied)
// declaration. The name is bound in the ENCLOSING scope, so the function may
// recurse and later statements may call it; its parameters live in a fresh
// nested scope covering only its body. Options.EnterFunctionScope runs inside
// that scope, before the parameters bind, for binders this walker does not
// model (type parameters, a method receiver).
func (w *Walker) WalkFunctionDeclaration(fn grammar.IFunctionDeclarationContext) { w.Walk(fn) }

// WalkParameterDefaults walks the `= expr` default of each parameter, so a
// reference inside a default value is seen. A parameter list is no node Walk
// dispatches, so it has a handler of its own on each tree.
func (w *Walker) WalkParameterDefaults(params grammar.IParametersContext) {
	if w.native == nil || !w.native.WalkParameterDefaults(w, params) {
		antlrWalk{w}.walkParameterDefaults(params)
	}
}

// Suffix is what ClassifySuffixes reads off a postfix suffix, whichever tree
// it is on: the name of a `.name` selector, or whether it is an index `[…]`
// (neither: a call `(…)`).
type Suffix struct {
	Selector string
	Index    bool
}

// ClassifySuffixes decides whether a bare-identifier base followed by the given
// suffixes (and an optional trailing `match`) is a pure field-read path or a
// whole use. A leading, uninterrupted run of `.field` selectors with no call,
// index, or match is a field-read path; an empty suffix list (a bare
// reference), a trailing match, or any call/index makes it a whole use. shape
// reads a suffix of either tree.
func ClassifySuffixes[S any](suffixes []S, hasMatch bool, shape func(S) Suffix) (path string, whole bool) {
	if hasMatch || len(suffixes) == 0 {
		return "", true
	}
	parts := make([]string, 0, len(suffixes))
	for i, s := range suffixes {
		sh := shape(s)
		if sh.Selector != "" {
			parts = append(parts, sh.Selector)
			continue
		}
		if sh.Index {
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
