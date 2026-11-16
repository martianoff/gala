package transformer_test

import (
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goResultsReturnProgram is a program whose declarations hand GALA values to
// Go as several results.
func goResultsReturnProgram(body string) string {
	return `package main

import (
    "errors"
    "strconv"
    "sync"
)

struct Counter(var Lines int)

type Parser interface {
    Parse(s string) (int, error)
}

struct Hex(Prefix string)

func (h Hex) Parse(s string) (int, error) = strconv.Atoi(s)

func run(f func() (int, error)) int = f().GetOrElse(-1)

func failWith(msg string) error = errors.New(msg)

func lockOn(m *sync.Mutex) = m.Lock()

` + body + `

func main() {}
`
}

// TestDeclaredGoResults covers a GALA function or method declaring a Go
// result list: Go sees the results, the body computes their one GALA value
// (`(T, error)` is a Try[T], `(A, B)` a Tuple[A, B]), and a GALA call of it is
// lifted to that value like a call of a Go function.
func TestDeclaredGoResults(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "method (T, error) with a block body",
			body: `func (c *Counter) Write(p []byte) (int, error) {
    c.Lines = c.Lines + 1
    Success(p.Size())
}`,
			contains: []string{
				"func (c *Counter) Write(p []byte) (int, error) {",
				"var _goResult std.Try[int] = func() std.Try[int] {",
				"return *new(int), _goResult.GetError()",
				"return _goResult.Get(), nil",
			},
		},
		{
			name: "a returning branch leaves the body",
			body: `func check(s string) (int, error) {
    if (s == "") {
        return Failure(errors.New("empty"))
    }
    Success(s.Size())
}`,
			contains: []string{"func check(s string) (int, error) {", `return std.Failure[int]{}.Apply(errors.New("empty"))`},
		},
		{
			name:     "a Go call with the same results is returned as it is",
			body:     `func parse(s string) (int, error) = strconv.Atoi(s)`,
			contains: []string{"func parse(s string) (int, error) {\n//line go_results_return_test.gala:25\n\treturn strconv.Atoi(s)\n}"},
			absent:   []string{"GoTry(strconv"},
		},
		{
			name:     "(A, B) from a Tuple",
			body:     `func divmod(a int, b int) (int, int) = (a / b, a % b)`,
			contains: []string{"func divmod(a int, b int) (int, int) {", "return _goResult.V1.Get(), _goResult.V2.Get()"},
		},
		{
			name: "(A, B, error) from a Try of a Tuple",
			body: `func split(s string) (string, int, error) = strconv.Atoi(s).Map((n) => (s, n))`,
			contains: []string{
				"func split(s string) (string, int, error) {",
				"return *new(string), *new(int), _goResult.GetError()",
				"_goValue := _goResult.Get()",
				"return _goValue.V1.Get(), _goValue.V2.Get(), nil",
			},
		},
		{
			name:     "a GALA call of it is one value",
			body:     "func parse(s string) (int, error) = strconv.Atoi(s)\n\nfunc twice(s string) int = parse(s).Map((n) => n * 2).GetOrElse(0)",
			contains: []string{`std.GoTry(parse(s))`},
		},
		{
			name:     "a method call through a GALA interface declaring it is one value",
			body:     `func parseWith(p Parser) int = p.Parse("1").GetOrElse(0)`,
			contains: []string{"Parse(s string) (int, error)", `std.GoTry(p.Parse("1"))`},
		},
		{
			name:     "a call of a generic method declaring it is one value",
			body:     "struct Box[T any](Value T)\n\nfunc (b Box[T]) Pair[U any](u U) (T, U) = (b.Value, u)\n\nfunc first() int {\n    val (n, _) = Box(3).Pair(\"x\")\n    n\n}",
			contains: []string{"func Box_Pair[U any, T any](b Box[T], u U) (T, U) {", `std.GoTuple(Box_Pair(`},
		},
		{
			name:     "names bound one by one keep the raw results",
			body:     "func parse(s string) (int, error) = strconv.Atoi(s)\n\nfunc first(s string) int {\n    val n, err = parse(s)\n    if (err != nil) 0 else n\n}",
			contains: []string{`= parse(s)`},
			absent:   []string{"GoTry(parse"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsReturnProgram(tc.body), "go_results_return_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, not := range tc.absent {
				assert.NotContains(t, out, not)
			}
		})
	}
}

// TestLambdaReturnsGoResults covers a lambda passed where a function with
// several results is expected: it computes their one GALA value, returned to
// Go as the results, and a Go call with those results is returned as it is.
// A generic Go callee's type parameters are then inferred by Go from the
// lambda's results.
func TestLambdaReturnsGoResults(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name:     "a Go call body is returned as it is",
			body:     `func five() int = run(() => strconv.Atoi("5"))`,
			contains: []string{"run(func() (int, error) {\n//line go_results_return_test.gala:25\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
			absent:   []string{"GoTry(strconv"},
		},
		{
			name:     "a Try body is spread",
			body:     `func seven() int = run(() => Success(7))`,
			contains: []string{"run(func() (int, error) {", "var _goResult std.Try[int] = std.Success[int]{}.Apply(7)", "return _goResult.Get(), nil"},
		},
		{
			name: "a block body is spread",
			body: `func doubled() int = run(() => {
    val n = strconv.Atoi("4")
    n.Map((k) => k * 2)
})`,
			contains: []string{"run(func() (int, error) {", "var _goResult std.Try[int] = func() std.Try[int] {"},
		},
		{
			name:     "a generic Go callee's results are the lambda's",
			body:     "func once() int {\n    val get = sync.OnceValues(() => strconv.Atoi(\"5\"))\n    val n, _ = get()\n    n\n}",
			contains: []string{"sync.OnceValues(func() (int, error) {\n//line go_results_return_test.gala:26\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
			absent:   []string{"T1", "T2"},
		},
		{
			name:     "a call of a value with several results is one value",
			body:     "func once() int {\n    val get = sync.OnceValues(() => strconv.Atoi(\"5\"))\n    get().GetOrElse(0)\n}",
			contains: []string{"std.GoTry(get())"},
		},
		{
			name:     "a call of a value with several results is one value as an argument",
			body:     "func once() string {\n    val get = sync.OnceValues(() => strconv.Atoi(\"5\"))\n    Println(get())\n    describe(get())\n}\n\nfunc describe(t Try[int]) string = t.String()",
			contains: []string{"fmt.Println(std.GoTry(get()))", "describe(std.GoTry(get()))"},
		},
		{
			name:     "a bare Go call is lifted into a thunk returning its results",
			body:     `func five() int = run(strconv.Atoi("5"))`,
			contains: []string{"run(func() (int, error) {\n//line go_results_return_test.gala:25\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
			absent:   []string{"GoTry(strconv"},
		},
		{
			name:     "a bare Try is lifted into a thunk spreading it",
			body:     `func seven() int = run(Success(7))`,
			contains: []string{"run(func() (int, error) {", "return _goResult.Get(), nil"},
		},
		{
			name:     "a bare Go call fills a generic Go callee's thunk",
			body:     "func once() int {\n    val get = sync.OnceValues(strconv.Atoi(\"5\"))\n    get().GetOrElse(0)\n}",
			contains: []string{"sync.OnceValues(func() (int, error) {\n//line go_results_return_test.gala:26\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
		},
		{
			name:     "a function value with the results is passed as it is",
			body:     "func once() int {\n    val get = sync.OnceValues(strconv.Atoi(\"5\"))\n    run(get)\n}",
			contains: []string{"run(get)"},
		},
		{
			name:     "a call of a struct field with several results is one value",
			body:     "struct Box(cb func(string) (int, error))\n\nfunc viaField(b Box) Try[int] = b.cb(\"4\")",
			contains: []string{`std.GoTry(b.cb.Get()("4"))`},
		},
		{
			name:     "a call of a call's result with several results is one value",
			body:     "func mk() func(string) (int, error) = (s) => strconv.Atoi(s)\n\nfunc viaCall() Try[int] = mk()(\"5\")",
			contains: []string{`std.GoTry(mk()("5"))`},
		},
		{
			name:     "a placeholder lambda over a Go call returns its results",
			body:     "func atoiWith(f func(string) (int, error)) Try[int] = f(\"7\")\n\nfunc seven() Try[int] = atoiWith(strconv.Atoi(_))",
			contains: []string{"atoiWith(func(__p0 string) (int, error) {\n//line go_results_return_test.gala:27\n\t\treturn strconv.Atoi(__p0)"},
		},
		{
			name:     "a placeholder lambda's Tuple is spread",
			body:     "func pair(n string) Tuple[string, bool] = (n, true)\n\nfunc look(f func(string) (string, bool)) Try[string] = Success(f(\"x\").V1)\n\nfunc found() Try[string] = look(pair(_))",
			contains: []string{"look(func(__p0 string) (string, bool) {", "var _goResult std.Tuple[string, bool] = pair(__p0)"},
		},
		{
			name:     "a generic Go callee's single result is the lambda's",
			body:     "func once() int {\n    val get = sync.OnceValue(() => 5)\n    get()\n}",
			contains: []string{"sync.OnceValue(func() int {"},
			absent:   []string{"func() T"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsReturnProgram(tc.body), "go_results_return_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, not := range tc.absent {
				assert.NotContains(t, out, not)
			}
		})
	}
}

// TestGoResultsRejected covers what cannot be returned as Go's results.
func TestGoResultsRejected(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "more results than a Tuple holds",
			body: `func many() (int, int, int, int, int, int, int, int, int, int, int) = (1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)`,
			want: "a Go result list of 11 values is more than the 10 a Tuple holds",
		},
		{
			name: "a lambda whose value cannot make the results",
			body: "func bad() int {\n    val get = sync.OnceValues(() => Some(1))\n    0\n}",
			want: "this lambda is passed where Go expects a function with 2 results, so it must give a Try for `(T, error)`, a Tuple for `(A, B)`, but it gives a value of type std.Option[int]",
		},
		{
			name: "a nested function declaring a Go result list",
			body: "func outer() int {\n    func inner() (int, error) = Success(1)\n    1\n}",
			want: "a lambda cannot declare a Go result list",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goResultsReturnProgram(tc.body), "go_results_return_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// callbackGo is a Go package of the module whose functions take callbacks
// returning several results.
const callbackGo = `package callback

func RunWith(f func() (int, error)) int {
	n, err := f()
	if err != nil {
		return -1
	}
	return n
}

func Label(f func(n int) (string, bool)) string {
	s, ok := f(1)
	if !ok {
		return "none"
	}
	return s
}
`

// TestGoCallbackReturnsGoResults covers a lambda passed to a non-generic Go
// function whose parameter is a function with several results.
func TestGoCallbackReturnsGoResults(t *testing.T) {
	cases := []liftCase{
		{
			name:     "value-and-error callback with a Go call body",
			body:     `func five() int = callback.RunWith(() => strconv.Atoi("5"))`,
			want:     "callback.RunWith(func() (int, error) {\n//line main.gala:10\n\t\treturn strconv.Atoi(\"5\")\n\t})",
			unlifted: true,
		},
		{
			name: "two-value callback with a Tuple body",
			body: `func label() string = callback.Label((n) => (s"n=$n", n > 0))`,
			want: "callback.Label(func(n int) (string, bool) {",
		},
	}
	runLiftCases(t, cases, func(body string) (map[string]string, string) {
		return map[string]string{
			"go.mod":               "module example.com/callbacks\n\ngo 1.25\n",
			"gala.mod":             "module example.com/callbacks\n",
			"callback/callback.go": callbackGo,
			"main.gala":            "package main\n\nimport (\n    \"strconv\"\n    \"example.com/callbacks/callback\"\n)\n\nfunc itoa(n int) string = strconv.Itoa(n)\n\n" + body + "\n\nfunc main() {}\n",
		}, "main.gala"
	})
}

// assertGoResultsLowering checks how out gives Go a body's `(int, error)`
// results: spread, its Try declared and spread (a failure gives the zero
// value), or not, the Go call `return strconv.Atoi(` passed through.
func assertGoResultsLowering(t *testing.T, out string, spread bool) {
	t.Helper()
	if spread {
		assert.Contains(t, out, "var _goResult std.Try[int] = ", "generated:\n%s", out)
		return
	}
	assert.NotContains(t, out, "_goResult", "generated:\n%s", out)
	assert.Contains(t, out, "return strconv.Atoi(", "generated:\n%s", out)
}

// TestGoResultsByBodyKind covers how a body lowered to `(int, error)` gives
// Go its results: a block body always spreads its Try, an expression body
// that is a Go call with those results returns them as they are. Neither may
// depend on the statements the body happens to lower to: line markers (left
// out without a file path) and a lambda's Ptr nil checks add some.
func TestGoResultsByBodyKind(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	ptrSlot := "struct In(text string)\n\nfunc each(f func(*In) (int, error), in *In) Try[int] = f(in)\n\n"
	cases := []struct {
		name   string
		body   string
		spread bool
	}{
		{name: "a function with a block body", body: "func parse(s string) (int, error) {\n    strconv.Atoi(s)\n}", spread: true},
		{name: "a function with an expression body", body: "func parse(s string) (int, error) = strconv.Atoi(s)"},
		{name: "a block lambda", body: "func five() int = run(() => {\n    strconv.Atoi(\"5\")\n})", spread: true},
		{name: "an expression lambda", body: `func five() int = run(() => strconv.Atoi("5"))`},
		{name: "a block lambda with a Ptr parameter", body: ptrSlot + "func five() Try[int] {\n    var in = In(\"5\")\n    each((p Ptr[In]) => {\n        strconv.Atoi(p.text)\n    }, &in)\n}", spread: true},
		{name: "an expression lambda with a Ptr parameter", body: ptrSlot + "func five() Try[int] {\n    var in = In(\"5\")\n    each((p Ptr[In]) => strconv.Atoi(p.text), &in)\n}"},
	}
	for _, path := range []string{"go_results_return_test.gala", ""} {
		shown := path
		if shown == "" {
			shown = "<none>"
		}
		for _, tc := range cases {
			t.Run(tc.name+", file path "+shown, func(t *testing.T) {
				out, err := trans.Transpile(goResultsReturnProgram(tc.body), path)
				require.NoError(t, err)
				assertGoResultsLowering(t, out, tc.spread)
			})
		}
	}
}

// TestGoResultsByBodyKindInForeignDefault covers a lambda default declared in
// another file of the package: it is lowered in the calling file without line
// markers, and still gives Go its results by what its body is.
func TestGoResultsByBodyKindInForeignDefault(t *testing.T) {
	cases := []struct {
		name   string
		lambda string
		spread bool
	}{
		{name: "a block lambda", lambda: "() => {\n    strconv.Atoi(\"5\")\n}", spread: true},
		{name: "an expression lambda", lambda: `() => strconv.Atoi("5")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeGalaModule(t, map[string]string{
				"gala.mod":     "module example.com/defaults\n\ngala dev\n",
				"lib/own.gala": "package lib\n\nimport \"strconv\"\n\nfunc RunWith(f func() (int, error) = " + tc.lambda + ") int = f().GetOrElse(-1)\n",
			})
			out, err := transpileCrossPkgFile(t, root, "package lib\n\nimport \"strconv\"\n\nfunc Use() int = RunWith() + strconv.IntSize\n", filepath.Join(root, "lib", "use.gala"))
			require.NoError(t, err)
			assertGoResultsLowering(t, out, tc.spread)
		})
	}
}

// goResultsWholeProgram is goResultsReturnProgram with go_interop
// dot-imported, for ResultsOf.
func goResultsWholeProgram(body string) string {
	return `package main

import (
    "bufio"
    "errors"
    "io"
    "strconv"
    . "martianoff/gala/go_interop"
)

func run(f func() (int, error)) int = f().GetOrElse(-1)

func reader(r io.Reader) *bufio.Reader = bufio.NewReader(r)

func failWith(msg string) error = errors.New(msg)

func keepAll(s string) Tuple[int, error] = ResultsOf(strconv.Atoi(s))

` + body + `

func main() {}
`
}

// TestGoResultsKeptWhole covers a body Go calls with `(T, error)` results
// that gives the Tuple of every result, Tuple[T, error], instead of the
// default Try: Go receives both components as they are, a value returned with
// an error included (io.Reader's n with io.EOF). go_interop's ResultsOf makes
// that Tuple from a Go call, which Go spreads over its parameters as it is.
func TestGoResultsKeptWhole(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	whole := []string{"var _goResult std.Tuple[int, error] = ", "return _goResult.V1.Get(), _goResult.V2.Get()"}
	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name:     "a block body giving ResultsOf",
			body:     "struct Wrap(inner io.Reader)\n\nfunc (w Wrap) Read(p []byte) (int, error) {\n    ResultsOf(w.inner.Read(p))\n}",
			contains: append([]string{"func (w Wrap) Read(p []byte) (int, error) {", "ResultsOf(w.inner.Get().Read(p))"}, whole...),
			absent:   []string{"IsFailure", "GoTry(w.inner"},
		},
		{
			name:     "an expression body giving ResultsOf",
			body:     "func parse(s string) (int, error) = ResultsOf(strconv.Atoi(s))",
			contains: append([]string{"ResultsOf(strconv.Atoi(s))"}, whole...),
			absent:   []string{"IsFailure", "GoTry(strconv"},
		},
		{
			name:     "a tuple of the results",
			body:     "func parse(s string) (int, error) {\n    val (n, err) = ResultsOf(strconv.Atoi(s))\n    (n, err)\n}",
			contains: whole,
			absent:   []string{"IsFailure"},
		},
		{
			name:     "an early return of a tuple",
			body:     "func parse(s string) (int, error) {\n    if (s == \"\") {\n        return (0, io.EOF)\n    }\n    ResultsOf(strconv.Atoi(s))\n}",
			contains: []string{"func() std.Tuple[int, error] {", "return _goResult.V1.Get(), _goResult.V2.Get()"},
			absent:   []string{"IsFailure"},
		},
		{
			name:     "(A, B, error) from ResultsOf2",
			body:     "func line(r *bufio.Reader) ([]byte, bool, error) = ResultsOf2(r.ReadLine())",
			contains: []string{"ResultsOf2(r.ReadLine())", "var _goResult std.Tuple3[[]byte, bool, error] = ", "return _goResult.V1.Get(), _goResult.V2.Get(), _goResult.V3.Get()"},
			absent:   []string{"IsFailure"},
		},
		{
			name:     "a block lambda giving ResultsOf",
			body:     "func five() int = run(() => {\n    ResultsOf(strconv.Atoi(\"5\"))\n})",
			contains: []string{"run(func() (int, error) {", "var _goResult std.Tuple[int, error] = ", "return _goResult.V1.Get(), _goResult.V2.Get()"},
			absent:   []string{"IsFailure"},
		},
		{
			name:     "an expression lambda giving ResultsOf",
			body:     `func five() int = run(() => ResultsOf(strconv.Atoi("5")))`,
			contains: []string{"run(func() (int, error) {", "var _goResult std.Tuple[int, error] = ", "return _goResult.V1.Get(), _goResult.V2.Get()"},
			absent:   []string{"IsFailure"},
		},
		{
			name:     "the default stays the Try",
			body:     "struct Wrap(inner io.Reader)\n\nfunc (w Wrap) Read(p []byte) (int, error) {\n    w.inner.Read(p)\n}",
			contains: []string{"var _goResult std.Try[int] = ", "return *new(int), _goResult.GetError()"},
		},
		{
			name:     "a Go call's results are passed to a Go function taking them",
			body:     "func first(s string) int = keepAll(s).V1",
			contains: []string{"func keepAll(s string) std.Tuple[int, error] {", "return ResultsOf(strconv.Atoi(s))"},
			absent:   []string{"GoTry(strconv"},
		},
		{
			name:     "every result binds a type parameter of the Go function taking them",
			body:     "func flag(r *bufio.Reader) bool = ResultsOf2(r.ReadLine()).V2",
			contains: []string{"ResultsOf2(r.ReadLine()).V2.Get()"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsWholeProgram(tc.body), "go_results_whole_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			for _, not := range tc.absent {
				assert.NotContains(t, out, not, "generated:\n%s", out)
			}
		})
	}
}

// TestGoResultsBodyValueRejected pins GALA-E0134: a body Go calls with
// `(T, error)` results gives the Try or the Tuple of the results; any other
// value, or both kinds in one body, is reported at the value rather than left
// to go build against the generated return.
func TestGoResultsBodyValueRejected(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a function giving a plain value",
			body: "func parse(s string) (int, error) {\n    s.Size()\n}",
			want: "function parse gives Go the results (int, error), so its body gives a Try[int] (on a failure Go receives the zero value with the error) or a Tuple[int, error] of the results as they are; this value is of type int",
		},
		{
			name: "an expression body giving an Option",
			body: "func parse(s string) (int, error) = Some(s.Size())",
			want: "this value is of type Option[int]",
		},
		{
			name: "a Tuple of other types",
			body: "func parse(s string) (int, error) = (\"x\", errors.New(s))",
			want: "this value is of type Tuple[string, error]",
		},
		{
			name: "a Try and a Tuple in one body",
			body: "func parse(s string) (int, error) {\n    if (s == \"\") {\n        return Success(0)\n    }\n    ResultsOf(strconv.Atoi(s))\n}",
			want: "function parse gives a Tuple[int, error] here but a Try[int] at line 21",
		},
		{
			name: "a lambda giving a plain value",
			body: "func five() int = run(() => {\n    5\n})",
			want: "this lambda gives Go the results (int, error)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goResultsWholeProgram(tc.body), "go_results_whole_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeGoResultsBodyValue))
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
