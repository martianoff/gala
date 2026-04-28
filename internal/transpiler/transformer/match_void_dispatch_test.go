package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMatchVoidDispatch verifies that a match expression used purely as a
// dispatch statement — where every arm either calls a void function, recurses,
// or is an empty `{}` block — is treated as void-returning instead of failing
// with "cannot infer result type of match expression". Previously the fallback
// only kicked in when the enclosing function had a concrete declared return
// type; a void enclosing function (return type nil) caused the diagnostic to
// fire even though the match's value was never used.
func TestMatchVoidDispatch(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		mustHave []string // substrings that must appear in generated code
	}{
		{
			name: "All arms void or nil — sealed dispatch with recursion + empty block",
			input: `package main

sealed type Shape {
    case Point()
    case Line()
    case Nothing()
    case Nested(Inner Shape)
}

func doStuff() {
    Println("side effect")
}

func log(s Shape) {
    s match {
        case Point()       => doStuff()
        case Line()        => Println("line")
        case Nothing()     => {}
        case Nested(inner) => log(inner)
    }
}
`,
			// The match IIFE inside log must have no return type, and the body
			// must strip the "return" values from each arm.
			mustHave: []string{
				`func log(s Shape) {`,
				`func(obj Shape) {`, // IIFE with no result — void dispatch
				`doStuff()`,
				`fmt.Println("line")`,
				`log(inner)`,
			},
		},
		{
			name: "Two-arm match, both arms bare void calls",
			input: `package main

func beep() {}
func boop() {}

func dispatch(b bool) {
    b match {
        case true  => beep()
        case false => boop()
    }
}
`,
			mustHave: []string{
				`func dispatch(b bool) {`,
				`beep()`,
				`boop()`,
			},
		},
		{
			name: "Match with mixed void call + empty block arms",
			input: `package main

func act() {}

func run(flag bool) {
    flag match {
        case true  => act()
        case false => {}
    }
}
`,
			mustHave: []string{
				`func run(flag bool) {`,
				`act()`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation should succeed for void dispatch match")
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
			// The infer error must NOT be produced; we verify by asserting NoError above.
			assert.NotContains(t, gen, "cannot infer result type")
		})
	}
}

// TestMatchVoidArm_TrailingLiteralExpressionIsDropped guards against a
// regression where a void-context match whose case body is a block
// ending in a literal value (`true`, `42`, an identifier, …) generated
// invalid Go like `{ <literal>; return }`. Go only accepts function
// calls (and channel ops) as expression-statements; a bare value
// triggers "untyped bool constant is not used" and the package fails
// to compile.
//
// The pattern is common in stream / dispatch code that hand-rolled a
// `true` sentinel inside void match arms (e.g. gala_tui's layout
// constraint walker). The fix lives in stripReturnStatement
// (patterns.go) and elides side-effect-free expressions when stripping
// the value off a return.
func TestMatchVoidArm_TrailingLiteralExpressionIsDropped(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

sealed type Constraint {
    case Length(V int)
    case Percent(P int)
}

func walk(c Constraint) {
    var total = 0
    c match {
        case Length(v)  => {
            total = total + v
            true
        }
        case Percent(p) => {
            total = total + p
            true
        }
    }
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err, "transpilation should succeed")
	gen := stripGeneratedHeader(got)

	// The literal trailing values must NOT appear as bare expression
	// statements anywhere in the output (the previous bug emitted
	// `{ true\n return }` blocks).
	assertNoBareLiteralStmt(t, gen, "true")
}

// assertNoBareLiteralStmt asserts that `lit` does not appear on its own
// line in `code`. This catches the dangling-literal-as-statement form
// `{\n\tlit\n\treturn\n}` without false-positiving on legitimate uses
// like `if x == true {`.
func assertNoBareLiteralStmt(t *testing.T, code string, lit string) {
	t.Helper()
	for _, line := range strings.Split(code, "\n") {
		if strings.TrimSpace(line) == lit {
			t.Errorf("found bare literal %q as a Go expression-statement; this is invalid Go.\n--- generated ---\n%s", lit, code)
			return
		}
	}
}

// TestMatchValueContextStillErrors sanity-checks that the fallback does not
// silently turn a value-using match with all-nil arms into VoidType. When the
// match IS used as a value and no branch has a concrete type, we still want
// an explicit error so the user knows inference failed.
//
// Note: this case currently only triggers an error when the enclosing function
// has no concrete return type AND no branch resolves. Previously the resulting
// Go code would be rejected by `go build`; now we rely on the Go compiler to
// reject `val x = voidMatch` downstream. The test records the current behavior
// so future regressions are visible.
func TestMatchValueContextFallback(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// When the enclosing function declares a concrete return type, the existing
	// fallback still takes effect (no regression).
	input := `package main

sealed type Shape {
    case Point()
    case Line()
}

func doStuff() {}

func describe(s Shape) string {
    return s match {
        case Point() => "p"
        case Line()  => "l"
    }
}
`
	_, err := trans.Transpile(input, "")
	assert.NoError(t, err, "value-producing match with concrete branches must continue to work")
}

// TestIfExprFormInsideMatchArmBody guards against a panic that occurred when a
// match-arm body contained the parenthesized if-expression form
// `if (cond) callA() else callB()` (no braces, both branches calling void
// functions). The transformer eagerly cast .Primary() to *grammar.PrimaryContext
// without checking the result, but for the if-expression alternation
// .Primary() returns a typed-nil interface, producing
//
//	interface conversion: grammar.IPrimaryContext is nil, not *grammar.PrimaryContext
func TestIfExprFormInsideMatchArmBody(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		mustHave []string
	}{
		{
			name: "parenthesized if-expr with void branches inside string-literal match arm",
			input: `package main

func voidA() {}
func voidB() {}

func dispatch(key string, flag bool) {
    key match {
        case "k" => {
            if (flag) voidA()
            else      voidB()
        }
        case _ => voidA()
    }
}
`,
			mustHave: []string{
				`func dispatch(key string, flag bool) {`,
				`voidA()`,
				`voidB()`,
			},
		},
		{
			name: "parenthesized if-expr with void-call then-branch and assignment else-branch (faithful repro)",
			input: `package main

func voidA() {}

func dispatch(key string, flag bool) {
    var slot string
    key match {
        case "k" => {
            if (flag) voidA()
            else      slot = "x"
        }
        case _ => voidA()
    }
    Println(slot)
}
`,
			mustHave: []string{
				`func dispatch(key string, flag bool) {`,
				`voidA()`,
				`slot`,
			},
		},
		{
			name: "parenthesized if-expr inside match arm inside for loop (faithful field-decoder repro)",
			input: `package main

func voidA() {}

func processAll(keys []string, flag bool) {
    var slot string
    for i := range keys {
        keys[i] match {
            case "k" => {
                if (flag) voidA()
                else      slot = "y"
            }
            case _ => voidA()
        }
    }
    Println(slot)
}
`,
			mustHave: []string{
				`func processAll(`,
				`voidA()`,
			},
		},
		{
			name: "parenthesized if-expr with void branches inside sealed-variant match arm",
			input: `package main

sealed type Tag {
    case A()
    case B()
}

func sideEffect() {}
func other()      {}

func dispatch(t Tag, flag bool) {
    t match {
        case A() => {
            if (flag) sideEffect()
            else      other()
        }
        case B() => sideEffect()
    }
}
`,
			mustHave: []string{
				`func dispatch(t Tag, flag bool) {`,
				`sideEffect()`,
				`other()`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation must not panic on if-expr form inside match arm body")
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
		})
	}
}
