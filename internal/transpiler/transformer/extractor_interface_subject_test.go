package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractorPatternAssertsInterfaceSubject covers an extractor pattern — a
// sealed case, or a guard extractor — whose Unapply takes a concrete type, on
// a subject of an interface or type-parameter type. Unapply only takes the
// subject once it is asserted to that type, and the arm is gated on the
// assertion so a value of another type falls through to later arms.
func TestExtractorPatternAssertsInterfaceSubject(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
import "fmt"

sealed type Shape {
    case Circle(R int)
    case Square()
}

sealed type Wrapped {
    case Wrap(Inner any)
    case Empty()
}

type Even struct {}

func (_ Even) Unapply(i int) bool = i % 2 == 0
`

	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "sealed case on an any subject",
			body: `func describe(v any) string = v match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](obj)"},
			absent:   []string{"Unapply(obj)"},
		},
		{
			name: "cases of several sealed types, a guard and a type pattern",
			body: `func describe(v any) string = v match {
    case Circle(r) if r > 10 => "big"
    case Square() => "square"
    case Empty() => "empty"
    case i: int => fmt.Sprint(i)
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](obj)", "std.As[Wrapped](obj)", "std.As[int](obj)"},
			absent:   []string{"Unapply(obj)"},
		},
		{
			name: "sealed case nested in a field of type any",
			body: `func describe(w Wrapped) string = w match {
    case Wrap(Circle(r)) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Shape]("},
		},
		{
			name: "guard extractor on an any subject",
			body: `func describe(v any) string = v match {
    case Even() => "even"
    case _ => "other"
}`,
			contains: []string{"std.As[int](obj)"},
			absent:   []string{"Unapply(obj)"},
		},
		{
			name: "bare guard extractor on an any subject",
			body: `func describe(v any) string = v match {
    case Even => "even"
    case _ => "other"
}`,
			contains: []string{"std.As[int](obj)"},
			absent:   []string{"Unapply(obj)"},
		},
		{
			name: "instance extractor on an any subject",
			body: `val even = Even{}

func describe(v any) string = v match {
    case even() => "even"
    case _ => "other"
}`,
			contains: []string{"std.As[int](obj)"},
			absent:   []string{"Unapply(obj)"},
		},
		{
			name: "sealed case on a type parameter subject",
			body: `func describe[T any](x T) string = x match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](any(obj))"},
		},
		{
			// A subject of the sealed type itself is passed to Unapply as it is.
			name: "sealed subject is not asserted",
			body: `func describe(s Shape) string = s match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"Unapply(obj)"},
			absent:   []string{"std.As[Shape]"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\n" + tc.body + "\n\nfunc main() {}\n"
			out, err := trans.Transpile(src, "extractor_interface_subject_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, bad := range tc.absent {
				assert.NotContains(t, out, bad)
			}
		})
	}
}

// TestExtractorPatternRejectsTypeOutsideInterface covers an extractor whose
// Unapply takes a type an interface subject can never hold — a sealed parent
// missing the interface's methods, or a primitive, which has none: the arm
// could never match, so it is an error, as it is for a struct pattern.
func TestExtractorPatternRejectsTypeOutsideInterface(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name    string
		arm     string
		wantErr []string
	}{
		{
			name:    "sealed parent",
			arm:     `case Circle(r) => "circle"`,
			wantErr: []string{"Shape does not implement error (missing Error)", "the pattern Circle(...) never matches"},
		},
		{
			name:    "primitive",
			arm:     `case Even() => "even"`,
			wantErr: []string{"int does not implement error (missing Error)", "the pattern Even(...) never matches"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `package main

sealed type Shape {
    case Circle(R int)
    case Square()
}

type Even struct {}

func (_ Even) Unapply(i int) bool = i % 2 == 0

func describe(err error) string = err match {
    ` + tc.arm + `
    case _ => "other"
}

func main() {}
`
			_, err := trans.Transpile(src, "extractor_interface_subject_test.gala")
			require.Error(t, err)
			for _, want := range tc.wantErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
