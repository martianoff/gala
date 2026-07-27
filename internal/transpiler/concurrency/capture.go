package concurrency

// Capture (free-variable) analysis — PR2 of the compile-time data-race-safety
// work.
//
// Given a GALA closure (either an explicit lambda or the desugared thunk of a
// by-name expression such as `Future(counter + 1)`), FreeVariables computes the
// set of identifiers the closure references from its ENCLOSING scope — the names
// it "captures".
//
// The traversal itself — the lexical scope stack, every binding-introducing
// construct, and the value-position reference detection — lives in
// internal/transpiler/scopewalk, shared with the analyzer's undefined-symbol
// check. Both passes need the same notion of "a value-position identifier that
// no enclosing binder introduced"; this file only turns the resulting stream of
// unbound references into captures, classifying each by HOW it is used.
//
// Bindings that exclude a name (all handled by the shared walker):
//   - the closure's own parameters;
//   - `val` / `var` declarations inside the body;
//   - `bind` / `also` do-notation bindings and `use` resource bindings;
//   - `for` loop variables (`:=` range vars and C-style init vars);
//   - `match` / partial-function case-pattern bindings (`case Some(x)` binds x);
//   - a nested lambda's parameters (which shadow only within the nested scope).
//
// This deliberately OVER-approximates (it reports spurious frees, e.g. the
// callee of a bare `foo(...)` call or a bracketed type argument, rather than
// miss a genuine capture — the safe direction). PR3 classifies each reported
// capture (resolving its `val`/`var` kind and type and enforcing shareability);
// a name that resolves to a top-level function or a type harmlessly falls out
// as non-local. The shared walker's over-approximating options are selected
// here for exactly that reason.
//
// String interpolation (`s"…$counter"`, `f"${x + y}"`) is a SINGLE lexer token,
// so its embedded expressions are not child nodes. The shared walker re-parses
// each embedded expression and walks it through the same machinery, so a
// variable referenced only inside an interpolation is captured too.
//
// NOTE: like the Shareable predicate (PR1), this pass is wired into no
// enforcement. It is called only by its tests. PR3 consumes it.

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/scopewalk"
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

// captureOptions is the shared walker's configuration for capture analysis.
// Every choice here is the over-approximating one: a spurious capture is
// harmless (PR3 discards it as non-local), a missed one is a data race.
//
//   - ParseInterpolations: a variable referenced only inside `s"…$x"` is a real
//     capture and must be seen.
//   - SkipCompositeLiteralKeys stays false: walking a keyed element's key can
//     only add a spurious name, never hide one.
//   - BindWholePattern stays false: the precise split reports a constructor
//     name as a reference (harmless) while binding the names the pattern
//     actually introduces (required for correctness).
//   - SkipTypedPatternArgument stays false: `x: T` in argument position is
//     unusual, and treating x as a reference is the safe direction.
var captureOptions = scopewalk.Options{ParseInterpolations: true}

// FreeVariablesInLambda returns the free variables captured by an explicit
// lambda. The lambda's declared parameters are bound (never captured); its body
// may be an expression (`(x) => x + n`) or a block (`(x) => { ... }`).
func FreeVariablesInLambda(lambda grammar.ILambdaExpressionContext) []Capture {
	c := newCaptureCollector()
	c.walker.WalkLambda(lambda)
	return c.out
}

// FreeVariablesInExpression returns the free variables of a bare expression —
// the shape a by-name / thunk argument desugars to. `Future(counter + 1)` is
// analyzed by calling this with the expression `counter + 1` and no params,
// which is identical to analyzing the explicit thunk `() => counter + 1`.
//
// paramNames lets a caller seed additional bound names (e.g. an implicit
// parameter of the thunk); pass nil/empty for the common Future/Spawn thunk.
func FreeVariablesInExpression(paramNames []string, expr grammar.IExpressionContext) []Capture {
	c := newCaptureCollector()
	c.walker.PushScope()
	for _, p := range paramNames {
		c.walker.Bind(p)
	}
	c.walker.Walk(expr)
	c.walker.PopScope()
	return c.out
}

// captureCollector turns the shared walker's unbound-reference stream into the
// capture set, de-duplicating by name and keeping the first-seen position.
type captureCollector struct {
	out    []Capture
	index  map[string]int // capture name -> its position in out
	walker *scopewalk.Walker
}

var _ scopewalk.Visitor = (*captureCollector)(nil)

func newCaptureCollector() *captureCollector {
	c := &captureCollector{index: map[string]int{}}
	c.walker = scopewalk.New(c, captureOptions)
	return c
}

// Reference records one unbound value-position use of name. A whole-value use
// marks the capture Whole (its entire type must be Shareable); a pure field
// read accumulates the distinct access path instead.
func (c *captureCollector) Reference(name string, tok antlr.Token, use scopewalk.Use) {
	idx, ok := c.index[name]
	if !ok {
		c.out = append(c.out, Capture{Name: name, Pos: transpiler.PosFromToken(tok)})
		idx = len(c.out) - 1
		c.index[name] = idx
	}
	if use.Whole {
		c.out[idx].Whole = true
		return
	}
	for _, p := range c.out[idx].Paths {
		if p == use.Path {
			return
		}
	}
	c.out[idx].Paths = append(c.out[idx].Paths, use.Path)
}
