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

// transpileBareVariant compiles src through the full pipeline and returns the
// generated Go, or the error.
func transpileBareVariant(t *testing.T, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).Transpile(src, "main.gala")
}

// TestBareVariantPatternIsNotABinding pins the lowering of a bare identifier in
// pattern position.
//
// A bare identifier is ordinarily a variable binding, and a binding matches
// EVERY value. When the identifier names a zero-field variant of the type being
// matched, treating it as a binding turns the arm into a catch-all: the program
// compiles, exits 0, and answers with the wrong arm. `case None` over an
// `Option[int]` used to match a `Some`.
//
// The runtime consequence is pinned by examples/bare_variant_pattern (single
// file) and examples/multifile_lib_regress (batch analysis). These cases pin the
// generated shape, which is where a regression would first appear: the arm must
// call the variant's `Unapply`, and must NOT assign the subject to a variable
// named after the variant.
func TestBareVariantPatternIsNotABinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantContains is the extractor call the arm must lower to.
		wantContains string
		// wantAbsent is the binding assignment that must not appear.
		wantAbsent string
	}{
		{
			// The canonical case. std.Option is generic, so `None`'s companion
			// carries a type parameter — the reason the bare form used to fall
			// through to a binding while non-generic variants worked.
			name: "generic std variant",
			src: `package main

func main() {
    val o = Some(42)
    val r = o match {
        case None    => "miss"
        case Some(v) => s"hit $v"
    }
    Println(r)
}`,
			wantContains: "std.None[int]{}.Unapply(obj)",
			wantAbsent:   "None := obj",
		},
		{
			// Same shape, declared by the user rather than by std. The
			// bare form must work identically — no std-specific handling.
			name: "generic user-declared variant",
			src: `package main

sealed type Maybe[T any] {
    case Just(V T)
    case Nothing
}

func describe(m Maybe[int]) string = m match {
    case Nothing => "nothing"
    case Just(v) => s"just $v"
}

func main() {
    Println(describe(Just(42)))
}`,
			wantContains: "Nothing[int]{}.Unapply(obj)",
			wantAbsent:   "Nothing := obj",
		},
		{
			name: "non-generic user-declared variant",
			src: `package main

sealed type Shape {
    case Circle(R float64)
    case Empty
}

func describe(s Shape) string = s match {
    case Empty     => "empty"
    case Circle(r) => s"circle $r"
}

func main() {
    Println(describe(Circle(1.0)))
}`,
			wantContains: "Empty{}.Unapply(obj)",
			wantAbsent:   "Empty := obj",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			require.Contains(t, out, tc.wantContains,
				"bare variant arm must call the variant's Unapply")
			require.NotContains(t, out, tc.wantAbsent,
				"bare variant arm must not bind the subject to a variable named after the variant")
		})
	}
}

// TestBareVariantNameWithFieldsIsRejected covers the other half: a bare
// identifier naming a FIELD-BEARING variant of the matched type. There is no
// sensible lowering — the variant needs its fields bound — and leaving it as a
// binding is exactly the silent catch-all this check exists to stop.
func TestBareVariantNameWithFieldsIsRejected(t *testing.T) {
	cases := []struct {
		name           string
		src            string
		expectContains string
	}{
		{
			name: "user sealed type",
			src: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val s = Square(2.0)
    val r = s match {
        case Circle    => "circle"
        case Square(x) => s"square $x"
    }
    Println(r)
}`,
			expectContains: "`Circle` is a variant of sealed type \"Shape\"",
		},
		{
			name: "std Option",
			src: `package main

func main() {
    val o = None[int]()
    val r = o match {
        case Some   => "hit"
        case None() => "miss"
    }
    Println(r)
}`,
			expectContains: "`Some` is a variant of sealed type \"Option\"",
		},
		{
			// The arm body does not reference the bound name. This used to be
			// reported as `unused variable 'None' in match branch — use '_' to
			// discard this value`, whose own advice (`use '_'`) produces a real
			// catch-all that compiles and is still wrong. It must reach the
			// variant-collision error instead.
			name: "body does not reference the name",
			src: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val s = Square(2.0)
    val r = s match {
        case Square(x) => s"square $x"
        case Circle    => "circle"
    }
    Println(r)
}`,
			expectContains: "`Circle` is a variant of sealed type \"Shape\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, tc.src)
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			require.Equal(t, galaerr.CodeBareVariantBinding, se.Code)
			require.Contains(t, se.Msg, tc.expectContains)
			require.Contains(t, se.Hint, "to match the variant")
		})
	}
}

// TestBareBindingUnrelatedToMatchedTypeStillBinds pins the boundary of the
// check. The rule is scoped to variants of the type being matched: a bare
// identifier that collides with a variant of some UNRELATED type is an ordinary
// binding and must keep working, because such a binding was never misleading.
func TestBareBindingUnrelatedToMatchedTypeStillBinds(t *testing.T) {
	out, err := transpileBareVariant(t, `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val n = 42
    val r = n match {
        case Circle => s"bound $Circle"
        case _      => "other"
    }
    Println(r)
    Println(Circle(1.0))
}`)
	require.NoError(t, err)
	require.Contains(t, out, "Circle := obj",
		"a bare name that is not a variant of the matched type must still bind")
}
