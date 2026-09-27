package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestFuncTypedDefaultsLowering covers defaults whose declared type is a
// function type, on struct fields, function parameters and method parameters.
// The declared type is the default's expected type, exactly as it is for an
// argument passed explicitly: an unannotated lambda takes its parameter and
// result types from it, and a bare `nil` takes the declared type instead of
// leaving Go nothing to infer from.
func TestFuncTypedDefaultsLowering(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			name: "field lambda takes its parameter types from the field type",
			input: `package main

struct Hooks(Name string, OnOne func(int) int = (a) => a + 1)

func main() {
    Println(Hooks(Name = "x").OnOne(1))
}`,
			contains: []string{"OnOne: std.NewImmutable(func(a int) int {", "return a + 1"},
		},
		{
			// The default is re-parsed from its recorded text. Text taken from
			// GetText() had lost its whitespace, turning `a int` into `aint`.
			name: "annotated lambda parameters keep their names",
			input: `package main

struct Hooks(Name string, OnOne func(int) int = (a int) => a + 1)

func main() {
    Println(Hooks(Name = "x").OnOne(1))
}`,
			contains: []string{"func(a int) int {"},
			absent:   []string{"aint"},
		},
		{
			name: "void two-parameter lambda with a block body",
			input: `package main

struct Hooks(Name string, Report func(int, int) = (r, c) => {
    Println(r, c)
})

func main() {
    Hooks(Name = "x").Report(1, 2)
}`,
			contains: []string{"Report: std.NewImmutable(func(r int, c int) {"},
		},
		{
			name: "zero-argument lambda returning a tuple",
			input: `package main

struct Hooks(Name string, Origin func() Tuple[int, int] = () => (0, 0))

func main() {
    Println(Hooks(Name = "x").Origin())
}`,
			contains: []string{"func() std.Tuple[int, int] {"},
		},
		{
			name: "nil defaults take the field type",
			input: `package main

struct Hooks(Name string, OnOne func(int) int = nil, OnDone func() int = nil, Ptr *int = nil)

func main() {
    val h = Hooks(Name = "x")
    Println(h.OnOne == nil, h.OnDone == nil, h.Ptr == nil)
}`,
			contains: []string{
				"OnOne: std.NewImmutable[func(int) int](nil)",
				"OnDone: std.NewImmutable[func() int](nil)",
				"Ptr: std.NewImmutable[*int](nil)",
			},
		},
		{
			name: "function parameter lambda default",
			input: `package main

func greet(name string, f func(string) string = (s) => s + "!") string = f(name)

func main() {
    Println(greet("hi"))
}`,
			contains: []string{`greet("hi", func(s string) string {`},
		},
		{
			name: "named-argument call fills a lambda default",
			input: `package main

func greet(name string, suffix string = "", f func(string) string = (s) => s + "!") string = f(name) + suffix

func main() {
    Println(greet(name = "hi", suffix = "?"))
}`,
			contains: []string{`greet("hi", "?", func(s string) string {`},
		},
		{
			// `nil` is a value of every function type: it must not be turned
			// into a thunk `func() int { return nil }` by the by-name sugar
			// that zero-argument function parameters get.
			name: "nil default for a zero-argument function parameter",
			input: `package main

func run(f func() int = nil) bool = f == nil

func main() {
    Println(run())
}`,
			contains: []string{"run(nil)"},
		},
		{
			name: "method parameter lambda default",
			input: `package main

struct Box(N int)

func (b Box) Apply(f func(int) int = (x) => x * 3) int = f(b.N)

func main() {
    Println(Box(2).Apply())
}`,
			contains: []string{"Apply(func(x int) int {"},
		},
		{
			// A call with no argument list never reached the dispatcher that
			// fills defaults, so it was emitted as `.Scale()` and failed Go's
			// "not enough arguments".
			name: "zero-argument method call fills every default",
			input: `package main

struct Box(N int)

func (b Box) Scale(k int = 3) int = b.N * k

func main() {
    val b = Box(2)
    Println(b.Scale())
}`,
			contains: []string{"Scale(3)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "func_typed_defaults_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			for _, notWant := range tc.absent {
				assert.NotContains(t, out, notWant)
			}
		})
	}
}

// TestUninferableDefaultPointsAtTheDefault pins the position of a diagnostic
// raised while lowering a default: it names the offending token inside the
// default expression, not the start of the file.
func TestUninferableDefaultPointsAtTheDefault(t *testing.T) {
	trans := newDefaultsTranspiler()

	_, err := trans.Transpile(`package main

struct Hooks(
    Name string,
    Bad  func() int = (x) => 1,
)

func main() {
    Println(Hooks(Name = "x").Name)
}`, "func_typed_defaults_test.gala")

	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %T: %v", err, err)
	assert.Equal(t, galaerr.CodeUntypedLambdaParam, se.Code)
	assert.Equal(t, 5, se.Line)
	assert.Equal(t, 23, se.Column)
}
