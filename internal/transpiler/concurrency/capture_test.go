package concurrency

import (
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
)

// --- parse helpers -----------------------------------------------------------

// wrapBody embeds a function body inside a minimal, parseable GALA source file.
// A blank line after the package clause satisfies the parser's spacing rule.
func wrapBody(body string) string {
	return "package p\n\nfunc f() {\n" + body + "\n}\n"
}

func parseSource(t *testing.T, src string) antlr.Tree {
	t.Helper()
	tree, _, err := parser.NewAntlrGalaParser().Parse(src)
	require.NoError(t, err, "snippet failed to parse:\n%s", src)
	return tree
}

// walkTree visits node and all descendants in pre-order.
func walkTree(node antlr.Tree, visit func(antlr.Tree)) {
	visit(node)
	for i := 0; i < node.GetChildCount(); i++ {
		walkTree(node.GetChild(i), visit)
	}
}

// firstLambda parses src and returns the FIRST (outermost, by pre-order)
// lambda expression in the tree.
func firstLambda(t *testing.T, src string) grammar.ILambdaExpressionContext {
	t.Helper()
	tree := parseSource(t, src)
	var found grammar.ILambdaExpressionContext
	walkTree(tree, func(n antlr.Tree) {
		if found != nil {
			return
		}
		if l, ok := n.(*grammar.LambdaExpressionContext); ok {
			found = l
		}
	})
	require.NotNil(t, found, "no lambda found in:\n%s", src)
	return found
}

// firstValInitExpr parses src and returns the initializer expression of the
// first `val` declaration — used to obtain a bare expression context for the
// thunk-sugar entry point.
func firstValInitExpr(t *testing.T, src string) grammar.IExpressionContext {
	t.Helper()
	tree := parseSource(t, src)
	var found grammar.IExpressionContext
	walkTree(tree, func(n antlr.Tree) {
		if found != nil {
			return
		}
		if vd, ok := n.(*grammar.ValDeclarationContext); ok {
			if el := vd.ExpressionList(); el != nil {
				exprs := el.AllExpression()
				if len(exprs) > 0 {
					found = exprs[0]
				}
			}
		}
	})
	require.NotNil(t, found, "no val initializer found in:\n%s", src)
	return found
}

// names extracts just the capture names, preserving first-seen order. It
// returns nil (not an empty slice) when there are no captures, so table cases
// can write `want: nil` for a closed closure.
func names(caps []Capture) []string {
	if len(caps) == 0 {
		return nil
	}
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = c.Name
	}
	return out
}

// --- lambda capture tests ----------------------------------------------------

func TestFreeVariablesInLambda(t *testing.T) {
	tests := []struct {
		name string
		// lambda is the GALA lambda expression under analysis; it is embedded as
		// `val g = <lambda>` inside a function body before parsing.
		lambda string
		want   []string
	}{
		{
			name:   "captures a simple outer val",
			lambda: "() => counter + 1",
			want:   []string{"counter"},
		},
		{
			name:   "excludes own parameters",
			lambda: "(x, y) => x + y",
			want:   nil,
		},
		{
			name:   "closed lambda has no captures",
			lambda: "(f, x) => f(x) + x",
			want:   nil,
		},
		{
			name:   "excludes a local val, captures the rest",
			lambda: "() => { val a = 5; a + b }",
			want:   []string{"b"},
		},
		{
			name:   "excludes a local var, captures the rest",
			lambda: "() => { var a = 5; a + b }",
			want:   []string{"b"},
		},
		{
			// Outer param x, inner param x: the inner shadows within its own scope,
			// so x is never a capture; counter (free in both) is the only capture,
			// counted once.
			name:   "nested lambda inner param shadows",
			lambda: "(x) => (x) => x + counter",
			want:   []string{"counter"},
		},
		{
			// A name bound by the OUTER lambda param is free in the inner lambda but
			// must not be reported as a capture of the outer lambda.
			name:   "nested lambda uses outer param",
			lambda: "(a) => (b) => a + b",
			want:   nil,
		},
		{
			name:   "match-case pattern bindings excluded",
			lambda: "(opt) => opt match {\ncase Some(x) => x + base\ncase _ => base\n}",
			want:   []string{"base"},
		},
		{
			// Cons(h, t) binds h and t; only acc is captured.
			name:   "match-case multi-binding pattern excluded",
			lambda: "(lst) => lst match {\ncase Cons(h, t) => h + acc\ncase _ => acc\n}",
			want:   []string{"acc"},
		},
		{
			name:   "for range variable excluded",
			lambda: "() => { for i := range items { acc(i) } }",
			want:   []string{"items", "acc"},
		},
		{
			// bind and also bound names are excluded; their RHS and the trailing
			// expression's free names are captured.
			name:   "bind and also bound names excluded",
			lambda: "() => {\nbind a = source\nalso b = other\ncombine(a, b, c)\n}",
			want:   []string{"source", "other", "combine", "c"},
		},
		{
			name:   "use binding excluded",
			lambda: "() => { use fh = openFile; read(fh) }",
			want:   []string{"openFile", "read"},
		},
		{
			// Outer x is referenced BEFORE an inner `val x` shadows it, so x is a
			// capture; the later reference resolves to the local (no double count).
			name:   "shadowing: outer ref before local val",
			lambda: "() => {\nsink(x)\nval x = 5\nsink(x)\n}",
			want:   []string{"sink", "x"},
		},
		{
			// A nested bare call: both the callee foo (a free identifier) and its
			// argument counter are captured; PR3 classifies foo out if top-level.
			name:   "nested call captures callee and argument",
			lambda: "() => foo(counter)",
			want:   []string{"foo", "counter"},
		},
		{
			// recv.method(arg): the method name is not a capture; only the receiver
			// and the argument are.
			name:   "method name is not a capture",
			lambda: "() => obj.compute(n)",
			want:   []string{"obj", "n"},
		},
		{
			// Interpolation embeds its expressions in a single token; the walker
			// re-parses them, so a var used only inside `s"…"` is still captured.
			name:   "interpolated string captures embedded var",
			lambda: `() => s"n=$counter"`,
			want:   []string{"counter"},
		},
		{
			name:   "format string captures embedded expression vars",
			lambda: `() => f"${x + y}"`,
			want:   []string{"x", "y"},
		},
		{
			name:   "format string with explicit spec captures var",
			lambda: `() => f"${count}%04d"`,
			want:   []string{"count"},
		},
		{
			// A local referenced only inside an interpolation is not a capture.
			name:   "interpolation referencing a local captures nothing extra",
			lambda: `() => { val v = 1; sink(s"val=$v") }`,
			want:   []string{"sink"},
		},
		{
			// A nested lambda inside `${…}` binds its own param; the interpolation
			// still contributes the genuinely free names.
			name:   "nested lambda inside interpolation",
			lambda: `() => s"${ items.Map((i) => i + base) }"`,
			want:   []string{"items", "base"},
		},
		{
			// if-EXPRESSION has no precise handler; generic child recursion must
			// still surface every free name in it.
			name:   "if-expression reached via generic recursion",
			lambda: "() => if (cond) a else b",
			want:   []string{"cond", "a", "b"},
		},
		{
			// Unary op + parenthesised arithmetic: no named handler for these
			// precedence levels — generic recursion covers them.
			name:   "unary and nested arithmetic via generic recursion",
			lambda: "() => -(m + n)",
			want:   []string{"m", "n"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lambda := firstLambda(t, wrapBody("val g = "+tc.lambda))
			got := names(FreeVariablesInLambda(lambda))
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestFreeVariablesInExpression covers the thunk-sugar entry point: a by-name
// argument such as Future(counter + 1) is analyzed as the bare expression
// `counter + 1` with no parameters, giving the same answer as the explicit
// thunk `() => counter + 1`.
func TestFreeVariablesInExpression(t *testing.T) {
	t.Run("bare expression captures free names", func(t *testing.T) {
		expr := firstValInitExpr(t, wrapBody("val g = counter + 1"))
		got := names(FreeVariablesInExpression(nil, expr))
		assert.Equal(t, []string{"counter"}, got)
	})

	t.Run("thunk sugar matches explicit thunk", func(t *testing.T) {
		// The desugared thunk `counter + 1` and the explicit `() => counter + 1`
		// must produce identical captures.
		expr := firstValInitExpr(t, wrapBody("val g = counter + 1"))
		lambda := firstLambda(t, wrapBody("val g = () => counter + 1"))
		assert.Equal(t,
			names(FreeVariablesInExpression(nil, expr)),
			names(FreeVariablesInLambda(lambda)),
		)
	})

	t.Run("seeded param names are treated as bound", func(t *testing.T) {
		expr := firstValInitExpr(t, wrapBody("val g = counter + step"))
		got := names(FreeVariablesInExpression([]string{"step"}, expr))
		assert.Equal(t, []string{"counter"}, got)
	})
}

// findCapture returns the capture named name, or nil.
func findCapture(caps []Capture, name string) *Capture {
	for i := range caps {
		if caps[i].Name == name {
			return &caps[i]
		}
	}
	return nil
}

// TestCaptureFieldPaths verifies the field-access-sensitive usage info recorded
// per capture: a pure `.field` selector chain is a read PATH (not Whole), while a
// bare reference, a call, a method call, an index, a `match`, or a use as a
// function argument marks the capture Whole.
func TestCaptureFieldPaths(t *testing.T) {
	tests := []struct {
		name      string
		lambda    string
		capture   string   // the capture to inspect
		wantPaths []string // expected field-read paths (order-insensitive)
		wantWhole bool
	}{
		{
			name:      "single field read is a path, not whole",
			lambda:    "() => m.a",
			capture:   "m",
			wantPaths: []string{"a"},
			wantWhole: false,
		},
		{
			name:      "nested field read is a dotted path",
			lambda:    "() => m.a.b",
			capture:   "m",
			wantPaths: []string{"a.b"},
			wantWhole: false,
		},
		{
			name:      "distinct field reads accumulate as separate paths",
			lambda:    "() => g(m.a, m.b)",
			capture:   "m",
			wantPaths: []string{"a", "b"},
			wantWhole: false,
		},
		{
			name:      "method call on the capture is a whole use",
			lambda:    "() => m.foo()",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			// A method call on a field reads the receiver field path; the method
			// runs on that (to-be-verified-shareable) field value, so the capture
			// is a read of the receiver path "a", not a whole use.
			name:      "field read then method call records the receiver field path",
			lambda:    "() => m.a.foo()",
			capture:   "m",
			wantPaths: []string{"a"},
			wantWhole: false,
		},
		{
			name:      "nested field read then method call records the receiver path",
			lambda:    "() => m.a.b.foo()",
			capture:   "m",
			wantPaths: []string{"a.b"},
			wantWhole: false,
		},
		{
			// A call that is not the final suffix (a further access on its result)
			// cannot be reduced to a receiver field path — stays a whole use.
			name:      "field method call with a trailing index is a whole use",
			lambda:    "() => m.a.foo()[0]",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			name:      "index on a field is a whole use",
			lambda:    "() => m.a[0]",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			name:      "bare reference is a whole use",
			lambda:    "() => m",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			name:      "passing the capture as an argument is a whole use",
			lambda:    "() => foo(m)",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			name:      "indexing the capture is a whole use",
			lambda:    "() => m[i]",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			name:      "matching on the capture is a whole use",
			lambda:    "() => m match {\ncase _ => 1\n}",
			capture:   "m",
			wantPaths: nil,
			wantWhole: true,
		},
		{
			// Used both as a field read and whole: Whole dominates (the safe
			// direction), and the path is still recorded.
			name:      "mixed field-read and whole use is whole",
			lambda:    "() => m.a + foo(m)",
			capture:   "m",
			wantPaths: []string{"a"},
			wantWhole: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lambda := firstLambda(t, wrapBody("val g = "+tc.lambda))
			caps := FreeVariablesInLambda(lambda)
			c := findCapture(caps, tc.capture)
			require.NotNil(t, c, "expected a capture named %q in %v", tc.capture, names(caps))
			assert.Equal(t, tc.wantWhole, c.Whole, "Whole mismatch")
			assert.ElementsMatch(t, tc.wantPaths, c.Paths, "Paths mismatch")
		})
	}
}

// TestCapturePosition verifies the reported position is the first value-position
// reference to the captured name (the caret PR3's diagnostic points at).
func TestCapturePosition(t *testing.T) {
	lambda := firstLambda(t, wrapBody("val g = () => counter + 1"))
	caps := FreeVariablesInLambda(lambda)
	require.Len(t, caps, 1)
	assert.Equal(t, "counter", caps[0].Name)
	// `counter` is on line 4 (package, blank, func, body) of wrapBody's output.
	assert.Equal(t, 4, caps[0].Pos.Line)
	assert.Greater(t, caps[0].Pos.Column, 0)
}
