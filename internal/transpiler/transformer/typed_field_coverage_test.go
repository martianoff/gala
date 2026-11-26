package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestTypedFieldPatternCoverage pins that a typed sub-pattern covers its field
// only when the field's type holds no value of another type (#1351). A typed
// pattern tests the dynamic type, so over an interface field (`error`, `any`)
// or a type parameter it does not match nil, and a match relying on it is not
// exhaustive (GALA-E0002) rather than panicking at runtime.
func TestTypedFieldPatternCoverage(t *testing.T) {
	wrap := func(src string) string {
		return "package main\n\n" + src + "\nfunc main() {\n    Println(0)\n}\n"
	}
	rejected := []struct{ name, src string }{
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
		},
		{
			name: "an any field",
			src: `func f(o Option[any]) int = o match {
    case Some(x: any) => s"$x".Size()
    case None() => 0
}
`,
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
		},
		{
			name: "a type-parameter field",
			src: `func f[T any](o Option[T]) int = o match {
    case Some(x: T) => s"$x".Size()
    case None() => 0
}
`,
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(tc.src), "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeNonExhaustiveMatch))
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
			name: "a concrete Go field",
			src: `import "time"

func f(o Option[time.Duration]) int = o match {
    case Some(d: time.Duration) => int(d)
    case None() => 0
}
`,
		},
		{
			name: "a Go struct field",
			src: `import "time"

func f(o Option[time.Time]) int = o match {
    case Some(t: time.Time) => t.Year()
    case None() => 0
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
