package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestTypedFieldPatternCoverage pins that a typed sub-pattern covers its field
// only when the field's type is known, from GALA source alone, to hold no value
// of another type (#1351): a primitive other than `error` and `any`, or a GALA
// type that is not an interface. A typed pattern tests the dynamic type, so over
// an interface field or a type parameter it does not match nil. A Go-typed field
// never counts, so the result does not depend on whether the Go SDK is found.
// A match relying on such a pattern is not exhaustive (GALA-E0002).
func TestTypedFieldPatternCoverage(t *testing.T) {
	wrap := func(src string) string {
		return "package main\n\n" + src + "\nfunc main() {\n    Println(0)\n}\n"
	}
	rejected := []struct{ name, src, missing string }{
		{
			name: "an error field",
			src: `sealed type R {
    case Ok(Err error)
    case Other()
}

func f(r R) string = r match {
    case Ok(e: error) => e.Error()
    case Other() => "other"
}
`,
			missing: "Ok(...)",
		},
		{
			name: "an any field",
			src: `func f(o Option[any]) int = o match {
    case Some(x: any) => s"$x".Size()
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a GALA interface field",
			src: `type Named interface {
    Name() string
}

sealed type H {
    case Holder(N Named)
    case Empty()
}

func f(h H) string = h match {
    case Holder(n: Named) => n.Name()
    case Empty() => ""
}
`,
			missing: "Holder(...)",
		},
		{
			name: "a Go interface field",
			src: `import "fmt"

sealed type H {
    case Holder(S fmt.Stringer)
    case Empty()
}

func f(h H) string = h match {
    case Holder(s: fmt.Stringer) => s.String()
    case Empty() => ""
}
`,
			missing: "Holder(...)",
		},
		{
			name: "a concrete Go field",
			src: `import "time"

func f(o Option[time.Duration]) int = o match {
    case Some(d: time.Duration) => int(d)
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a Go struct field",
			src: `import "time"

func f(o Option[time.Time]) int = o match {
    case Some(t: time.Time) => t.Year()
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a function field",
			src: `func f(o Option[func(int) int]) int = o match {
    case Some(g: func(int) int) => g(1)
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a slice field",
			src: `func f(o Option[[]int]) int = o match {
    case Some(xs: []int) => xs[0]
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a map field",
			src: `func f(o Option[map[string]int]) int = o match {
    case Some(m: map[string]int) => m.Size()
    case None() => 0
}
`,
			missing: "Some(...)",
		},
		{
			name: "a type-parameter field",
			src: `func f[T any](o Option[T]) int = o match {
    case Some(x: T) => s"$x".Size()
    case None() => 0
}
`,
			missing: "Some(...)",
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(tc.src), "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeNonExhaustiveMatch))
			require.Contains(t, err.Error(), "missing cases: "+tc.missing)
		})
	}

	accepted := []struct{ name, src string }{
		{
			name: "a concrete field",
			src: `func f(o Option[int]) int = o match {
    case Some(n: int) => n
    case None() => 0
}
`,
		},
		{
			name: "a GALA struct field",
			src: `struct Point(X int, Y int)

func f(o Option[Point]) int = o match {
    case Some(p: Point) => p.X
    case None() => 0
}
`,
		},
		{
			name: "a generic GALA field",
			src: `func f(o Option[Option[int]]) int = o match {
    case Some(i: Option[int]) => i.GetOrElse(0)
    case None() => 0
}
`,
		},
		{
			name: "a GALA collection field",
			src: `import . "martianoff/gala/collection_immutable"

func f(o Option[Array[int]]) int = o match {
    case Some(a: Array[int]) => a.Size()
    case None() => 0
}
`,
		},
		{
			name: "a Go field, matched untyped",
			src: `import "time"

func f(o Option[time.Duration]) int = o match {
    case Some(d) => int(d)
    case None() => 0
}
`,
		},
		{
			name: "a Go field with a default",
			src: `import "time"

func f(o Option[time.Time]) int = o match {
    case Some(t: time.Time) => t.Year()
    case _ => 0
}
`,
		},
		{
			name: "an error field with a nil-safe default",
			src: `sealed type R {
    case Ok(Err error)
    case Other()
}

func f(r R) string = r match {
    case Ok(e: error) => e.Error()
    case Ok(_) => "nil error"
    case Other() => "other"
}
`,
		},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(tc.src), "")
			require.NoError(t, err)
		})
	}
}
