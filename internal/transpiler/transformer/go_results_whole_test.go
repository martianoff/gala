package transformer_test

import (
	"fmt"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goResultsWholeProgram is a program with go_interop dot-imported, for
// ResultsOf, around body.
func goResultsWholeProgram(body string) string {
	return `package main

import (
    "bufio"
    "errors"
    "io"
    "os"
    "strconv"
    . "martianoff/gala/go_interop"
)

func run(f func() (int, error)) int = f().GetOrElse(-1)

func reader() io.Reader = bufio.NewReader(os.Stdin)

func failWith(msg string) error = errors.New(msg)

func keepAll(s string) Tuple[int, error] = ResultsOf(strconv.Atoi(s))

` + body + `

func main() {}
`
}

// goResultsWholeBodyLine is the line the body of goResultsWholeProgram
// starts on.
const goResultsWholeBodyLine = 20

// TestGoResultsKeptWhole covers a body Go calls with `(T, error)` results
// that gives the Tuple of every result, Tuple[T, error], instead of the
// default Try: Go receives both components as they are, a value returned with
// an error included (io.Reader's n with io.EOF). go_interop's ResultsOf makes
// that Tuple from a Go call, which Go spreads over its parameters as it is.
// Each `return` is spread where it is, from a local of the value's own type.
func TestGoResultsKeptWhole(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	whole := []string{"_goResult := ", "return _goResult.V1.Get(), _goResult.V2.Get()"}
	notTry := []string{"IsFailure", "std.Try[int]"}
	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name:     "a block body giving ResultsOf",
			body:     "struct Wrap(inner io.Reader)\n\nfunc (w Wrap) Read(p []byte) (int, error) {\n    ResultsOf(w.inner.Read(p))\n}",
			contains: append([]string{"func (w Wrap) Read(p []byte) (int, error) {", "_goResult := ResultsOf(w.inner.Get().Read(p))"}, whole...),
			absent:   []string{"IsFailure", "GoTry(w.inner"},
		},
		{
			name:     "an expression body giving ResultsOf",
			body:     "func parse(s string) (int, error) = ResultsOf(strconv.Atoi(s))",
			contains: append([]string{"_goResult := ResultsOf(strconv.Atoi(s))"}, whole...),
			absent:   append([]string{"GoTry(strconv"}, notTry...),
		},
		{
			name:     "a tuple of the results",
			body:     "func parse(s string) (int, error) {\n    val (n, err) = ResultsOf(strconv.Atoi(s))\n    (n, err)\n}",
			contains: whole,
			absent:   notTry,
		},
		{
			name:     "a tuple with nil for the error",
			body:     "func size(s string) (int, error) = (s.Size(), nil)",
			contains: append([]string{"std.Tuple[int, error]{"}, whole...),
			absent:   notTry,
		},
		{
			name:     "an early return with nil for the error",
			body:     "func parse(s string) (int, error) {\n    if (s == \"\") {\n        return (0, nil)\n    }\n    ResultsOf(strconv.Atoi(s))\n}",
			contains: []string{"std.Tuple[int, error]{", "_goResult := ResultsOf(strconv.Atoi(s))"},
			absent:   append([]string{"func() std.Tuple"}, notTry...),
		},
		{
			name:     "a component only assignable to its result",
			body:     "func open(p string) (io.Reader, error) = ResultsOf(os.Open(p))",
			contains: []string{"func open(p string) (io.Reader, error) {", "_goResult := ResultsOf(os.Open(p))"},
			absent:   []string{"std.Tuple[io.Reader, error]"},
		},
		{
			name:     "(A, B, error) from ResultsOf2",
			body:     "func line(r *bufio.Reader) ([]byte, bool, error) = ResultsOf2(r.ReadLine())",
			contains: []string{"_goResult := ResultsOf2(r.ReadLine())", "return _goResult.V1.Get(), _goResult.V2.Get(), _goResult.V3.Get()"},
			absent:   []string{"IsFailure"},
		},
		{
			name:     "a Tuple through an alias",
			body:     "type Pair Tuple[int, error]\n\nfunc mkPair(s string) Pair = (s.Size(), nil)\n\nfunc parse(s string) (int, error) = mkPair(s)",
			contains: append([]string{"_goResult := mkPair(s)"}, whole...),
			absent:   notTry,
		},
		{
			name:     "an if-expression of two ResultsOf",
			body:     "func pick(s string, hex bool) (int, error) = if (hex) ResultsOf(strconv.Atoi(\"0x\" + s)) else ResultsOf(strconv.Atoi(s))",
			contains: whole,
			absent:   notTry,
		},
		{
			name:     "a self-tail-call loop",
			body:     "func count(n int) (int, error) = if (n <= 0) ResultsOf(strconv.Atoi(\"7\")) else count(n - 1)",
			contains: append([]string{"for {"}, whole...),
			absent:   notTry,
		},
		{
			name:     "a block lambda giving ResultsOf",
			body:     "func five() int = run(() => {\n    ResultsOf(strconv.Atoi(\"5\"))\n})",
			contains: append([]string{"run(func() (int, error) {"}, whole...),
			absent:   notTry,
		},
		{
			name:     "an expression lambda giving ResultsOf",
			body:     `func five() int = run(() => ResultsOf(strconv.Atoi("5")))`,
			contains: append([]string{"run(func() (int, error) {"}, whole...),
			absent:   notTry,
		},
		{
			name:     "a by-name argument giving ResultsOf",
			body:     `func five() int = run(ResultsOf(strconv.Atoi("5")))`,
			contains: append([]string{"run(func() (int, error) {"}, whole...),
			absent:   notTry,
		},
		{
			name:     "the default stays the Try",
			body:     "struct Wrap(inner io.Reader)\n\nfunc (w Wrap) Read(p []byte) (int, error) {\n    w.inner.Read(p)\n}",
			contains: []string{"var _goResult std.Try[int] = ", "return *new(int), _goResult.GetError()"},
		},
		{
			name:     "a Try through an alias",
			body:     "type IntTry Try[int]\n\nfunc mk(s string) IntTry = Success(s.Size())\n\nfunc parse(s string) (int, error) = mk(s)\n\nfunc parseBlock(s string) (int, error) {\n    mk(s)\n}",
			contains: []string{"_goResult.IsFailure()"},
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
// value, or both kinds in one body, is reported at the value itself rather
// than left to go build against the generated return. line and col (1-based)
// are the
// value's, line counted from the body's first line; mixedAt is the body line
// of the other kind's value, 0 when the body does not mix them.
func TestGoResultsBodyValueRejected(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name    string
		body    string
		line    int
		col     int
		mixedAt int
		want    string
	}{
		{
			name: "a function giving a plain value",
			body: "func parse(s string) (int, error) {\n    s.Size()\n}",
			line: 2, col: 5,
			want: "function parse gives Go the results (int, error), so its value is a Try[int] (on a failure Go receives the zero value with the error) or a Tuple[int, error] of the results as they are; this value is of type int",
		},
		{
			name: "an expression body giving an Option",
			body: "func parse(s string) (int, error) = Some(s.Size())",
			line: 1, col: 37,
			want: "this value is of type Option[int]",
		},
		{
			name: "a Tuple of other types",
			body: "func label(s string) Tuple[string, error] = (s, nil)\n\nfunc parse(s string) (int, error) = label(s)",
			line: 3, col: 37,
			want: "this value is of type Tuple[string, error]",
		},
		{
			name: "a Try and a Tuple in one body",
			body: "func parse(s string) (int, error) {\n    if (s == \"\") {\n        return Success(0)\n    }\n    ResultsOf(strconv.Atoi(s))\n}",
			line: 5, col: 5, mixedAt: 3,
			want: "function parse gives a Tuple[int, error] here but a Try[int]",
		},
		{
			name: "an if-expression giving a Try and a Tuple",
			body: "func parse(s string) (int, error) = if (s == \"\") Success(0) else ResultsOf(strconv.Atoi(s))",
			line: 1, col: 66, mixedAt: 1,
			want: "function parse gives a Tuple[int, error] here but a Try[int]",
		},
		{
			name: "a match giving a Try and a Tuple",
			body: "func parse(s string) (int, error) = s match {\n    case \"\" => Success(0)\n    case _ => ResultsOf(strconv.Atoi(s))\n}",
			line: 3, col: 15, mixedAt: 2,
			want: "function parse gives a Tuple[int, error] here but a Try[int]",
		},
		{
			name: "a self-tail-call loop giving a Try and a Tuple",
			body: "func count(n int) (int, error) = if (n < 0) Success(0) else if (n == 0) ResultsOf(strconv.Atoi(\"7\")) else count(n - 1)",
			line: 1, col: 73, mixedAt: 1,
			want: "function count gives a Tuple[int, error] here but a Try[int]",
		},
		{
			name: "a lambda giving a plain value",
			body: "func five() int = run(() => {\n    5\n})",
			line: 2, col: 5,
			want: "this lambda gives Go the results (int, error)",
		},
		{
			name: "a lambda giving a Try and a Tuple",
			body: "func five(s string) int = run(() => {\n    if (s == \"\") {\n        return Success(0)\n    }\n    ResultsOf(strconv.Atoi(s))\n})",
			line: 5, col: 5, mixedAt: 3,
			want: "this lambda gives a Tuple[int, error] here but a Try[int]",
		},
		{
			name: "a by-name argument giving a plain value",
			body: "func five() int = run(5)",
			line: 1, col: 23,
			want: "this argument gives Go the results (int, error)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goResultsWholeProgram(tc.body), "go_results_whole_test.gala")
			require.Error(t, err)
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeGoResultsBodyValue))
			assert.Contains(t, msg, fmt.Sprintf("go_results_whole_test.gala:%d:%d ", goResultsWholeBodyLine+tc.line-1, tc.col-1))
			assert.Contains(t, msg, tc.want)
			if tc.mixedAt > 0 {
				assert.Contains(t, msg, fmt.Sprintf("at line %d:", goResultsWholeBodyLine+tc.mixedAt-1))
			}
		})
	}
}
