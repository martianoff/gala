package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoneQualifiedDownwardInference verifies that a bare `None()` (a std.Option
// case, whose call target lowers to the package-qualified `std.None`) has its
// vestigial type parameter inferred from the surrounding context, so callers
// never need to write `None[Array[JField]]()` when the type is predictable.
//
// The motivating shape is a method whose declared return type is `Option[X]`
// with a match body — `Some(...)` pins the element type and the `None()` arm
// inherits it via the match's externally-pinned result type:
//
//	func (v JsonValue) AsObject() Option[Array[JField]] = v match {
//	    case JObj(fields) => Some(fields)
//	    case _            => None()
//	}
//
// Before the fix this already worked for `None()` via `inferZeroArgTypeParams`
// (match subject + enclosing return). The companion negative case
// (`TestErrorPathAssertions/GALA-E0018 qualified None() uninferred in lambda`)
// covers the qualified-name guard that previously misfired.
func TestNoneQualifiedDownwardInference(t *testing.T) {
	newTranspiler := func() *transpiler.GalaToGoTranspiler {
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
	}

	cases := []struct {
		name string
		// expectInjected is the Go fragment proving the type arg was injected
		// onto the zero-arg variant (so Go can instantiate it).
		expectInjected string
		input          string
	}{
		{
			name:           "method match return over generic element",
			expectInjected: "std.None[Array[JField]]{}.Apply()",
			input: `package main

import . "martianoff/gala/collection_immutable"

sealed type JField {
    case JF(Name string)
}

sealed type JsonValue {
    case JObj(Fields Array[JField])
    case JNull()
}

func (v JsonValue) AsObject() Option[Array[JField]] = v match {
    case JObj(fields) => Some(fields)
    case _ => None()
}

func main() {
    val v = JObj(ArrayOf[JField]())
    Println(v.AsObject().IsDefined())
}`,
		},
		{
			name:           "plain function return type",
			expectInjected: "std.None[int]{}.Apply()",
			input: `package main

func f() Option[int] = None()

func main() {
    Println(f().IsDefined())
}`,
		},
		{
			name:           "val with explicit option type",
			expectInjected: "std.None[string]{}.Apply()",
			input: `package main

func f() bool {
    val x Option[string] = None()
    return x.IsDefined()
}

func main() {
    Println(f())
}`,
		},
		{
			name:           "if-expression sibling pins the type",
			expectInjected: "std.None[int]{}.Apply()",
			input: `package main

func pick(b bool) Option[int] = if (b) Some(1) else None()

func main() {
    Println(pick(true).IsDefined())
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newTranspiler().Transpile(tc.input, "none_inference_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.expectInjected,
				"expected the zero-arg None() to be instantiated with the inferred type arg")
			// Guard against the pre-fix regression: an uninstantiated
			// `std.None{}` (no type args) is invalid Go.
			assert.False(t, strings.Contains(out, "std.None{}"),
				"must not emit an uninstantiated std.None{}; got:\n%s", out)
		})
	}
}
