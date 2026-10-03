package transformer_test

import (
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValParamIsPlainParam checks that an explicit `val` on a parameter is a
// no-op marker: the parameter lowers to the same plain Go parameter as an
// unmarked one, never std.Immutable[T], and a call passes its argument as is —
// for functions, methods (value and pointer receivers), method values,
// generics, function-typed parameters, defaults, lambdas and interface specs.
// The checked transpiler type-checks every output with the Go oracle.
func TestValParamIsPlainParam(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "function called with a literal",
			input: `package main

func size(val s string) int = s.Size()

func main() {
    Println(size("five"))
}`,
			want: []string{"func size(s string) int {", `size("five")`},
		},
		{
			name: "method called with a literal",
			input: `package main

struct Parser(Base int)

func (p Parser) Parse(val s string) Try[int] = Success(s.Size() + p.Base)

func main() {
    val p = Parser(10)
    Println(p.Parse("five"))
}`,
			want: []string{"func (p Parser) Parse(s string) std.Try[int] {", `.Parse("five")`},
		},
		{
			name: "pointer-receiver method",
			input: `package main

struct Counter(var N int)

func (c *Counter) Add(val by int) int = c.N + by

func main() {
    val c = &Counter(1)
    Println(c.Add(2))
}`,
			want: []string{"func (c *Counter) Add(by int) int {", ".Add(2)"},
		},
		{
			name: "method value passed as a function",
			input: `package main

struct Parser(Base int)

func (p Parser) Parse(val s string) Try[int] = Success(s.Size() + p.Base)

func main() {
    val p = Parser(10)
    val in = Success("abc")
    Println(in.FlatMap(p.Parse))
}`,
			want: []string{"func (p Parser) Parse(s string) std.Try[int] {", ".Parse)"},
		},
		{
			name: "generic function",
			input: `package main

func first[T any](val xs T, val other T) T = xs

func main() {
    Println(first(1, 2))
}`,
			want: []string{"func first[T any](xs T, other T) T {"},
		},
		{
			name: "function-typed parameter takes a lambda",
			input: `package main

func applyPlain(val f func(int) int) int = f(1)

func main() {
    Println(applyPlain((x) => x + 2))
}`,
			want: []string{"func applyPlain(f func(int) int) int {", "return f(1)"},
		},
		{
			name: "parameter typed by a generic function alias takes a lambda",
			input: `package main

type Conv[A any, B any] func(A) B

func applyConv(val f Conv[int, int]) int = f(1)

func main() {
    Println(applyConv((x) => x + 2))
}`,
			want: []string{"func applyConv(f Conv[int, int]) int {"},
		},
		{
			name: "defaulted parameter",
			input: `package main

func greet(val name string = "world") string = "hello " + name

func main() {
    Println(greet())
    Println(greet("gala"))
}`,
			want: []string{"func greet(name string) string {", `greet("world")`, `greet("gala")`},
		},
		{
			name: "explicit val lambda parameter",
			input: `package main

func main() {
    val inc = (val x int) => x + 1
    val twice: func(int) int = (val y) => y * 2
    Println(inc(1) + twice(2))
}`,
			want: []string{"func(x int) int {", "func(y int) int {"},
		},
		{
			name: "interface method spec and implementation",
			input: `package main

type Named interface {
    Greet(val name string) string
}

struct English()

func (e English) Greet(val name string) string = "hi " + name

func say(n Named) string = n.Greet("bob")

func main() {
    Println(say(English()))
}`,
			want: []string{"Greet(name string) string", "func (e English) Greet(name string) string {", `n.Greet("bob")`},
		},
		{
			name: "val receiver",
			input: `package main

struct Box(N int)

func (val b Box) Double() int = b.N * 2

func main() {
    Println(Box(2).Double())
}`,
			want: []string{"func (b Box) Double() int {"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			assert.NotContains(t, got, "Immutable[", "a parameter must never be boxed")
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestParamReassignment checks that a parameter is immutable unless it is
// declared `var`: unmarked and explicit-`val` parameters reject reassignment
// with the same error and hint, a `var` parameter may be reassigned, and an
// unmarked lambda parameter keeps its plain binding.
func TestParamReassignment(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	rejected := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name: "unmarked function parameter",
			input: `package main

func f(s string) int {
    s = "x"
    s.Size()
}`,
			wantMsg: "cannot assign to immutable variable s",
		},
		{
			name: "explicit val function parameter",
			input: `package main

func f(val s string) int {
    s = "x"
    s.Size()
}`,
			wantMsg: "cannot assign to immutable variable s",
		},
		{
			name: "compound assignment to a method parameter",
			input: `package main

struct C(N int)

func (c C) Add(n int) int {
    n += c.N
    n
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
		{
			name: "increment of a parameter",
			input: `package main

func f(n int) int {
    n++
    n
}`,
			wantMsg: "cannot increment/decrement immutable variable n",
		},
		{
			name: "parameter reassigned inside a nested block",
			input: `package main

func f(n int) int {
    if (n < 0) {
        n = 0
    }
    n
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
		{
			name: "explicit val lambda parameter",
			input: `package main

func main() {
    val f = (val x int) => {
        x = 2
        x
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable x",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Contains(t, semErr.Msg, tt.wantMsg)
			assert.Contains(t, semErr.Hint, "declare it `var ")
		})
	}

	accepted := []struct {
		name  string
		input string
	}{
		{
			name: "var parameter",
			input: `package main

func clamp(var n int) int {
    if (n < 0) {
        n = 0
    }
    n
}`,
		},
		{
			name: "local shadowing a parameter",
			input: `package main

func f(n int) int {
    var m = n
    m = m + 1
    m
}`,
		},
		{
			name: "unmarked lambda parameter",
			input: `package main

func main() {
    val f = (x int) => {
        x = x + 1
        x
    }
    Println(f(1))
}`,
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
		})
	}
}

// TestParamAddressIsConstPtr checks that the address of a parameter not
// declared `var` is a read-only ConstPtr, as for a val, so the parameter cannot
// be changed through a pointer; a `var` parameter's address stays a plain *T.
func TestParamAddressIsConstPtr(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	for _, kw := range []string{"", "val "} {
		t.Run("write through the address of a "+kw+"parameter", func(t *testing.T) {
			_, err := trans.Transpile(`package main

func f(`+kw+`n int) int {
    val p = &n
    *p = 5
    n
}`, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Contains(t, semErr.Msg, "cannot assign through ConstPtr")
		})
	}

	t.Run("reading through the address of a parameter", func(t *testing.T) {
		got, err := trans.Transpile(`package main

func f(n int) int {
    val p = &n
    *p + 1
}

func main() {
    Println(f(1))
}`, "")
		require.NoError(t, err)
		assert.Contains(t, got, "std.NewConstPtr(&n)")
	})

	t.Run("address of a var parameter", func(t *testing.T) {
		got, err := trans.Transpile(`package main

func f(var n int) int {
    val p = &n
    *p = 5
    n
}

func main() {
    Println(f(1))
}`, "")
		require.NoError(t, err)
		assert.NotContains(t, got, "NewConstPtr")
	})
}

// TestImmutableParamLiftsBareArg checks that a parameter explicitly typed
// Immutable[T] still takes a bare T argument, lifted with NewImmutable.
func TestImmutableParamLiftsBareArg(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	got, err := trans.Transpile(`package main

func size(s Immutable[string]) int = s.Get().Size()

func main() {
    Println(size("five"))
}`, "")
	require.NoError(t, err)
	assert.Contains(t, got, `size(std.NewImmutable[string]("five"))`)
}
