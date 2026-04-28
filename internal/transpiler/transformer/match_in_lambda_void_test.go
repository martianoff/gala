package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

// TestMatchInLambdaVoidArms verifies that a lambda body terminating in a
// match expression whose every arm is void (assignment / `{}` / void call)
// is lowered as a void-returning lambda — not as a value-returning lambda
// wrapping the void IIFE in `return`. The pre-fix lowering emitted
// `return func(obj T) { ... }(scrutinee)` and Go rejected with
// "(no value) used as value", or it emitted `func(...) void { ... }`
// using the literal `void` ident as the lambda's Go return type.
//
// Triggered when:
//   1. The lambda's expected return type is NOT propagated by the caller
//      (e.g. `return (m) => { match }` from a function-typed return).
//   2. The lambda body's last statement is a match where every arm is
//      void.
func TestMatchInLambdaVoidArms(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		mustHave []string
		mustMiss []string
	}{
		{
			name: "Lambda return from function-typed return — both arms void via assignment/call",
			input: `package main

import "errors"

struct Member(Name string)

func CloseSession(m Member) Try[bool] {
    if m.Name == "bad" {
        return Failure[bool](errors.New("oops"))
    }
    return Success[bool](true)
}

func MakeProcessor() func(Member) {
    var counter = 0
    return (m Member) => {
        CloseSession(m) match {
            case Success(_)   => counter = counter + 1
            case Failure(err) => Println(s"err: ${err.Error()}")
        }
    }
}

func main() {
    val p = MakeProcessor()
    p(Member(Name = "ok"))
}
`,
			// The lambda must have NO return type (no `void` ident, no
			// synthesized arm-union return type).
			mustMiss: []string{
				"return func(m Member) void",
				"} void {",
				// The void IIFE must NOT be wrapped in `return ... (CloseSession(m))`.
				"return func(obj std.Try[bool])",
			},
			mustHave: []string{
				// Lambda is void.
				"return func(m Member) {",
				// Match IIFE remains a bare expression statement.
				"}(CloseSession(m))",
			},
		},
		{
			name: "Empty-block arm + assignment arm — both void",
			input: `package main

sealed type Tag {
    case A()
    case B()
}

func MakeHandler() func(Tag) {
    var seen = 0
    return (t Tag) => {
        t match {
            case A() => {}
            case B() => seen = seen + 1
        }
    }
}

func main() {
    val h = MakeHandler()
    h(A())
}
`,
			mustMiss: []string{
				"} void {",
				"return func(obj Tag)",
			},
			mustHave: []string{
				"return func(t Tag) {",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation should succeed")
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
			for _, frag := range tt.mustMiss {
				assert.False(t, strings.Contains(gen, frag),
					"expected generated code to NOT contain %q\n--- generated ---\n%s", frag, gen)
			}
		})
	}
}
