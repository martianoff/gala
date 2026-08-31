package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

func newUnknownMethodTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestUnknownMethodOnKnownTypeIsRejected pins B3: calling a method a GALA type
// does not have must be a GALA error naming a real method, instead of reaching
// `go build` — which reported it against the GENERATED expression, including
// auto-unwrap `.Get()` calls the user never wrote:
//
//	xs.Get().Filter(func(x int) bool {…}).Sum undefined
//	  (type collection_immutable.Array[int] has no field or method Sum)
func TestUnknownMethodOnKnownTypeIsRejected(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name       string
		input      string
		wantMethod string
		wantInHint string
	}{
		{
			name: "no_such_method_on_stdlib_type",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Sum())
}`,
			wantMethod: "Sum",
		},
		{
			name: "near_miss_suggests_the_real_one",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Sise())
}`,
			wantMethod: "Sise",
			wantInHint: "Size",
		},
		{
			name: "user_defined_struct",
			input: `package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(p.Norm())
}`,
			wantMethod: "Norm",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.Error(t, err, "an unknown method must be rejected by GALA")
			require.Contains(t, err.Error(), string(galaerr.CodeUnknownMethod))
			require.Contains(t, err.Error(), tc.wantMethod)
			if tc.wantInHint != "" {
				require.Contains(t, err.Error(), tc.wantInHint,
					"the hint must suggest the nearest real method")
			}
		})
	}
}

// TestSynthesizedMethodsAreNotUnknown is the false-positive guard for the
// methods the transformer generates rather than the user declaring them. None
// of them appear in TypeMetadata.Methods, so a naive "not in Methods" check
// would reject every one.
func TestSynthesizedMethodsAreNotUnknown(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "copy_equal_on_struct",
			input: `package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    val q = p.Copy(X = 5)
    Println(p.Equal(q))
}`,
		},
		{
			name: "string_on_sealed",
			input: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val c = Circle(1.0)
    Println(c.String())
}`,
		},
		{
			name: "real_methods_still_work",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Filter((x) => x > 1).Size())
}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.NoError(t, err, "this method call is legitimate and must transpile")
		})
	}
}

// TestHintWhenTypeDeclaresNoMethods pins the review finding that the hint
// degenerated to a dangling "Point declares: " with nothing after the colon.
// A type in the package being compiled is judged even with an empty method set,
// so the empty list is reachable and has to read as a sentence.
func TestHintWhenTypeDeclaresNoMethods(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	_, err := trans.Transpile(`package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(p.Norm())
}`, "main.gala")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Point declares no methods")
	require.NotContains(t, err.Error(), "declares: ",
		"an empty method set must not render as a dangling list")
}
