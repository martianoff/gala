package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/transformer"
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
		{
			// The receiver used inside a lambda default is the CALL-SITE
			// receiver, with its immutable field unwrapped — not the
			// declaration's receiver name, which names nothing here.
			name: "lambda method default uses the receiver",
			input: `package main

struct Box(K int)

func (b Box) Scale(f func(int) int = (x) => x * b.K) int = f(3)

func main() {
    val box = Box(2)
    Println(box.Scale())
}`,
			contains: []string{"return x * box.Get().K.Get()"},
			absent:   []string{"x * b.K"},
		},
		{
			name: "lambda parameter shadowing the receiver name is left alone",
			input: `package main

struct Box(K int)

func (b Box) Peek(f func(Box) int = (b) => b.K + 1) int = f(b)

func main() {
    val box = Box(2)
    Println(box.Peek())
}`,
			contains: []string{"Peek(func(b Box) int {", "return b.K.Get() + 1"},
		},
		{
			// The receiver's type arguments bind `T` for a zero-argument call
			// just as they do for a call with arguments.
			name: "zero-argument call on a generic receiver",
			input: `package main

struct Holder[T any](V T)

func (h Holder[T]) Apply(f func(T) T = (a) => a) T = f(h.V)

func main() {
    val h = Holder(1)
    Println(h.Apply())
}`,
			contains: []string{"Apply(func(a int) int {"},
		},
		{
			// Only a DEFAULT `nil` is passed as is. An explicit `nil` argument to
			// a zero-argument function parameter keeps the by-name sugar's
			// meaning: a thunk returning nil.
			name: "explicit nil to a by-name parameter is still a thunk",
			input: `package main

func probe(f func() *int) bool = f() == nil

func probeDefault(f func() *int = nil) bool = f == nil

func main() {
    Println(probe(nil), probeDefault())
}`,
			contains: []string{"probe(func() *int {", "probeDefault(nil)"},
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

// transformDefaultsForLSP analyzes and transforms src the way the language
// server does, returning the variable types and lambda hints it collects.
func transformDefaultsForLSP(t *testing.T, src string) *transpiler.TransformResult {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.gala")
	require.NoError(t, os.WriteFile(path, []byte(src), 0644))
	p := transpiler.NewAntlrGalaParser()
	tree, docs, err := p.Parse(src)
	require.NoError(t, err)
	richAST, err := analyzer.NewGalaAnalyzer(p, getStdSearchPath()).Analyze(tree, docs, path)
	require.NoError(t, err)
	result, err := transformer.NewGalaASTTransformer().TransformForLSP(richAST)
	require.NoError(t, err)
	return result
}

// TestDefaultLambdaLSPHints: a default is ONE declaration however many call
// sites lower it. Its lambda parameters get one inlay hint each, from the
// declared type — not one per call site, which with a generic function also
// disagreed (`: int` at one call, `: string` at the next). And nothing a
// lowered default binds — its lambda parameters, a method's receiver — is
// recorded as a variable of the calling function, where it overwrote a real
// local of the same name.
func TestDefaultLambdaLSPHints(t *testing.T) {
	result := transformDefaultsForLSP(t, `package main

struct Gauge(K int)

func (g Gauge) Read(f func(int) int = (x) => x * g.K) int = f(3)

func greet(name string, f func(string) string = (s) => s + "!") string = f(name)

func twice[T any](x T, combine func(T, T) T = (a, b) => a) T = combine(x, x)

func main() {
    val g = 5
    val gauge = Gauge(2)
    Println(greet("a"), greet("b"), twice(1), twice("x"), gauge.Read(), g)
}
`)

	hintsOn := func(name string) []string {
		var types []string
		for _, h := range result.LambdaParamHints {
			if h.Name == name {
				types = append(types, h.Type.String())
			}
		}
		return types
	}
	assert.Equal(t, []string{"string"}, hintsOn("s"), "one hint for the default's parameter, from its declaration")
	assert.Equal(t, []string{"int"}, hintsOn("x"))
	assert.Equal(t, []string{"T"}, hintsOn("a"), "a generic default is hinted with its declared type, once")

	assert.Equal(t, "int", result.VarTypes["main.g"].String(), "the receiver bound while lowering Read's default overwrote the caller's local")
	_, leaked := result.VarTypes["main.s"]
	assert.False(t, leaked, "a default lambda's parameter was recorded as a local of the caller")
}

// funcDefaultsFixture is a module whose library declares parameter defaults
// that use the library's own functions, for lowering at a call site in another
// package.
func funcDefaultsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/fdefs\n\ngala dev\n")
	write("lib/lib.gala", `package lib

func Twice(n int) int = n * 2

func helper(n int) int = n + 1

func Wrap(n int, f func(int) int = (a) => Twice(a)) int = f(n)

func Hidden(n int, f func(int) int = (a) => helper(a)) int = f(n)

struct Gauge(K int)

func (g Gauge) Read(f func(int) int = (x) => Twice(x) + g.K) int = f(1)

func DefaultClose() int = 0

func Inc(n int) int = n + 1

struct Backend(Name string, OnClose func() int = DefaultClose, Step func(int) int = Inc)

func Run(f func() int = DefaultClose, g func(int) int = Inc) int = f() + g(1)
`)
	return root
}

// TestCrossPackageParamDefaults: a parameter default declared in another
// package is lowered in the caller's scope, so the names it borrows from its
// own package must be qualified — including inside a lambda — exactly as a
// struct field default's are.
func TestCrossPackageParamDefaults(t *testing.T) {
	root := funcDefaultsFixture(t)

	t.Run("exported helpers are qualified", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/fdefs/lib"

func main() {
    val g = lib.Gauge(5)
    Println(lib.Wrap(3), g.Read())
}`)
		require.NoError(t, err)
		body := out[strings.Index(out, "func main()"):]
		assert.Contains(t, body, "return lib.Twice(a)")
		assert.Contains(t, body, "return lib.Twice(x) + g.Get().K.Get()")
	})

	// A default that is a reference to the declaring package's function is
	// already a function value. It must not be mistaken for a plain value and
	// wrapped by the by-name sugar into `func() int { return lib.DefaultClose }`.
	t.Run("function-reference defaults stay references", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/fdefs/lib"

func main() {
    val b = lib.Backend(Name = "x")
    Println(b.OnClose(), b.Step(1), lib.Run())
}`)
		require.NoError(t, err)
		body := out[strings.Index(out, "func main()"):]
		assert.Contains(t, body, "OnClose: std.NewImmutable(lib.DefaultClose)")
		assert.Contains(t, body, "Step: std.NewImmutable(lib.Inc)")
		assert.Contains(t, body, "lib.Run(lib.DefaultClose, lib.Inc)")
		assert.NotContains(t, body, "return lib.DefaultClose")
	})

	t.Run("an unexported helper is reported at the default", func(t *testing.T) {
		_, err := transpileCrossPkg(t, root, `package main

import "example.com/fdefs/lib"

func main() {
    Println(lib.Hidden(3))
}`)
		require.Error(t, err)
		var se *galaerr.SemanticError
		require.True(t, errors.As(err, &se), "want a SemanticError, got %T: %v", err, err)
		assert.Contains(t, se.Error(), `"helper", which is unexported in package "lib"`)
		assert.Equal(t, filepath.Join(root, "lib", "lib.gala"), filepath.Clean(se.FilePath))
		assert.Equal(t, 9, se.Line)
		assert.Equal(t, 37, se.Column)
	})
}
