package transformer_test

import (
	"testing"

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
			contains: []string{"func parse(s string) (int, error) {\n\treturn strconv.Atoi(s)\n}"},
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
				"return _goResult.Get().V1.Get(), _goResult.Get().V2.Get(), nil",
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
			contains: []string{"run(func() (int, error) {\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
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
			contains: []string{"sync.OnceValues(func() (int, error) {\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
			absent:   []string{"T1", "T2"},
		},
		{
			name:     "a call of a value with several results is one value",
			body:     "func once() int {\n    val get = sync.OnceValues(() => strconv.Atoi(\"5\"))\n    get().GetOrElse(0)\n}",
			contains: []string{"std.GoTry(get.Get()())"},
		},
		{
			name:     "a call of a value with several results is one value as an argument",
			body:     "func once() string {\n    val get = sync.OnceValues(() => strconv.Atoi(\"5\"))\n    Println(get())\n    describe(get())\n}\n\nfunc describe(t Try[int]) string = t.String()",
			contains: []string{"fmt.Println(std.GoTry(get.Get()()))", "describe(std.GoTry(get.Get()()))"},
		},
		{
			name:     "a bare Go call is lifted into a thunk returning its results",
			body:     `func five() int = run(strconv.Atoi("5"))`,
			contains: []string{"run(func() (int, error) {\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
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
			contains: []string{"sync.OnceValues(func() (int, error) {\n\t\treturn strconv.Atoi(\"5\")\n\t})"},
		},
		{
			name:     "a function value with the results is passed as it is",
			body:     "func once() int {\n    val get = sync.OnceValues(strconv.Atoi(\"5\"))\n    run(get)\n}",
			contains: []string{"run(get.Get())"},
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
			want:     "callback.RunWith(func() (int, error) {\n\t\treturn strconv.Atoi(\"5\")\n\t})",
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
			"main.gala":            "package main\n\nimport (\n    \"strconv\"\n    \"example.com/callbacks/callback\"\n)\n\n" + body + "\n\nfunc main() {}\n",
		}, "main.gala"
	})
}
