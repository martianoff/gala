package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoCallResultsAsOneValue covers the conversion of a Go call returning
// several results to one GALA value, in every single-value position:
//
//	(T, error)    → Try[T]            std.GoTry(call)
//	(A, B, error) → Try[Tuple[A, B]]  std.GoTry2(call)
//	(A, B)        → Tuple[A, B]       std.GoTuple(call)
//	(A, B, C)     → Tuple3[A, B, C]   std.GoTuple3(call)
//
// Hermetic: `os`, `strconv`, `strings`, `net` and friends resolve through the
// Go SDK, which CI provides via .bazelrc --action_env=GOROOT. The examples
// `go_call_results` and `try_val_destructure` run the generated Go.
func TestGoCallResultsAsOneValue(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	converted := []struct {
		name     string
		body     string
		contains []string
	}{
		{"val binding, (T, error)", "    val n = strconv.Atoi(\"1\")\n    Println(n)",
			[]string{`std.NewImmutable(std.GoTry(strconv.Atoi("1")))`}},
		{"val binding, (A, B, error)", "    val hp = net.SplitHostPort(\"h:1\")\n    Println(hp)",
			[]string{`std.GoTry2(net.SplitHostPort("h:1"))`}},
		{"val binding, (A, B, C)", "    val c = strings.Cut(\"a=b\", \"=\")\n    Println(c)",
			[]string{`std.GoTuple3(strings.Cut("a=b", "="))`}},
		{":= binding", "    n := strconv.Atoi(\"1\")\n    Println(n)",
			[]string{`std.GoTry(strconv.Atoi("1"))`}},
		{"var binding", "    var n = strconv.Atoi(\"1\")\n    Println(n)",
			[]string{`var n = std.GoTry(strconv.Atoi("1"))`}},
		{"match subject", `    val m = os.ReadFile("f") match {
        case Success(_) => "read"
        case Failure(_) => "failed"
    }
    Println(m)`, []string{`}(std.GoTry(os.ReadFile("f")))`}},
		{"tuple destructuring of a (A, B, C) call", "    val (k, v, found) = strings.Cut(\"a=b\", \"=\")\n    Println(k, v, found)",
			[]string{`__tuple_1 = std.GoTuple3(strings.Cut("a=b", "="))`}},
		{"argument of a GALA function", "    Println(describe(strconv.Atoi(\"1\")))",
			[]string{`describe(std.GoTry(strconv.Atoi("1")))`}},
		{"sole argument of a variadic Go function", "    fmt.Println(strconv.Atoi(\"1\"))",
			[]string{`fmt.Println(std.GoTry(strconv.Atoi("1")))`}},
		{"expression-lambda body", "    val f = () => strconv.Atoi(\"1\")\n    Println(f())",
			[]string{`func() std.Try[int] {`, `return std.GoTry(strconv.Atoi("1"))`}},
		{"if-expression branches", "    val r = if (true) strconv.Atoi(\"1\") else strconv.Atoi(\"2\")\n    Println(r)",
			[]string{`return std.GoTry(strconv.Atoi("1"))`, `return std.GoTry(strconv.Atoi("2"))`}},
		{"match-arm results", `    val r = 1 match {
        case 1 => strconv.Atoi("1")
        case _ => strconv.Atoi("2")
    }
    Println(r)`, []string{`return std.GoTry(strconv.Atoi("1"))`}},
		{"struct field value", "    Println(Holder(N = strconv.Atoi(\"1\")))",
			[]string{`std.GoTry(strconv.Atoi("1"))`}},
		{"method chained on the value", "    Println(strconv.Atoi(\"1\").Map((n) => n + 1))",
			[]string{`std.Try_Map(std.GoTry(strconv.Atoi("1")),`}},
		{"interpolated value", "    Println(s\"${strconv.Atoi(\"1\")}\")",
			[]string{`std.GoTry(strconv.Atoi("1"))`}},
	}
	for _, tc := range converted {
		t.Run("converts/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsProgram(tc.body), "go_results_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}

	// Positions that take a call's results one by one keep the raw call.
	raw := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{"multi-name val binding", "    val n, err = strconv.Atoi(\"1\")\n    Println(n, err)",
			[]string{`= strconv.Atoi("1")`}, []string{"GoTry"}},
		{"multi-name var binding", "    var n, err = strconv.Atoi(\"1\")\n    Println(n, err)",
			[]string{`var n, err = strconv.Atoi("1")`}, []string{"GoTry"}},
		{"multi-name :=", "    n, err := strconv.Atoi(\"1\")\n    Println(n, err)",
			[]string{`= strconv.Atoi("1")`}, []string{"GoTry"}},
		{"multi-name reassignment", "    var n = 0\n    var e error = nil\n    n, e = strconv.Atoi(\"1\")\n    Println(n, e)",
			[]string{`n, e = strconv.Atoi("1")`}, []string{"GoTry"}},
		{"statement", "    fmt.Println(\"x\")\n    fmt.Fprintf(os.Stdout, \"y\")",
			[]string{`fmt.Println("x")`, `fmt.Fprintf(os.Stdout, "y")`}, []string{"GoTry"}},
		{"sole argument a Go function spreads", "    Println(template.Must(template.New(\"x\").Parse(\"hi\")).Name())",
			[]string{`template.Must(template.New("x").Parse("hi"))`}, []string{"GoTry"}},
		{"value-free if-expression of Println calls", "    if (true) fmt.Println(\"a\") else fmt.Println(\"b\")",
			nil, nil},
		// The arms of a statement-position match discard their values, and so
		// does a match at the tail of such an arm: its Go call arm must not be
		// read as the value its void arm cannot give.
		{"match at the tail of a statement-position match arm", `    Try(strconv.Atoi("1")) match {
        case Failure(_) => twice(1)
        case Success(n) => {
            strconv.Itoa(n) match {
                case "1" => {
                    os.Stdout.WriteString("one")
                    os.Stdout.WriteString("\n")
                }
                case _ => each((_) => fmt.Println("other"))
            }
        }
    }`, []string{`os.Stdout.WriteString("\n")`}, []string{"GoTry", "return each("}},
		{"void lambda body", "    each((n) => fmt.Println(n))",
			[]string{"fmt.Println(n)"}, []string{"GoTry"}},
	}
	for _, tc := range raw {
		t.Run("keeps raw/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsProgram(tc.body), "go_results_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, not := range tc.absent {
				assert.NotContains(t, out, not)
			}
		})
	}

	// Try(...) already turns an error into a Failure: the call's error is the
	// panic Try catches, never a Try inside the Try.
	tries := []struct {
		name     string
		body     string
		contains []string
	}{
		{"Try over a (T, error) call", "    Println(Try(strconv.Atoi(\"1\")))",
			[]string{"std.Try[int]{}.Apply(func() int {", "panic(_err)"}},
		{"Try over a lambda of a (T, error) call", "    Println(Try(() => strconv.Atoi(\"1\")))",
			[]string{"std.Try[int]{}.Apply(func() int {", "panic(_err)"}},
		{"Try over a block lambda ending in a (T, error) call", "    Println(Try(() => {\n        val s = \"1\"\n        strconv.Atoi(s)\n    }))",
			[]string{"std.Try[int]{}.Apply(func() int {", "panic(_err)"}},
		{"Try over an error-only call", "    Println(Try(os.Remove(\"f\")))",
			[]string{"std.Try[std.Void]{}.Apply(func() std.Void {", `if _err := os.Remove("f"); _err != nil {`}},
		{"Try over a lambda of an error-only call", "    Println(Try(() => os.Remove(\"f\")))",
			[]string{"std.Try[std.Void]{}.Apply(func() std.Void {"}},
		{"TryApply over a (T, error) call", "    Println(TryApply(() => strconv.Atoi(\"1\")))",
			[]string{"std.TryApply(func() int {"}},
		{"Try over a (A, B, error) call", "    Println(Try(net.SplitHostPort(\"h:1\")))",
			[]string{"std.Try[std.Tuple[string, string]]{}.Apply("}},
	}
	for _, tc := range tries {
		t.Run("Try/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(goResultsProgram(tc.body), "go_results_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			assert.NotContains(t, out, "std.Try[std.Try[")
		})
	}

	// GALA-E0049: the value is used as the call's plain first result.
	rejected := []struct {
		name     string
		body     string
		contains string
	}{
		{"GALA function argument", "    Println(twice(strconv.Atoi(\"1\")))",
			"`strconv.Atoi(...)` can fail, so it produces `Try[int]`; `int` is expected here"},
		{"Go function argument, through a val", "    val data = os.ReadFile(\"f\")\n    os.WriteFile(\"g\", data, 0o644)",
			"`data` holds the result of `os.ReadFile(...)`, which can fail, so it is a `Try[[]byte]`; `[]byte` is expected here"},
		{"variadic Go function argument", "    val dir = os.MkdirTemp(\"\", \"x\")\n    Println(filepath.Join(dir, \"a\"))",
			"`string` is expected here"},
		{"declared val type", "    val data []byte = os.ReadFile(\"f\")\n    Println(data)",
			"`[]byte` is expected here"},
		{"reassignment of a typed var", "    var n = 0\n    n = strconv.Atoi(\"5\")\n    Println(n)",
			"`int` is expected here"},
		{"struct field of the plain type", "    Println(Plain(N = strconv.Atoi(\"4\")))",
			"`int` is expected here"},
		{"member of the plain value", "    val resp = http.Get(\"http://x\")\n    Println(resp.StatusCode)",
			"so it is a `Try[*http.Response]`; a Try has no member `StatusCode`"},
		{"method of the plain value", "    val f = os.Create(\"x\")\n    f.Close()",
			"a Try has no member `Close`"},
		{"operand", "    val n = strconv.Atoi(\"1\")\n    Println(n + 1)",
			"it cannot be an operand of `+`"},
		{"conversion", "    val data = os.ReadFile(\"f\")\n    Println(string(data))",
			"it cannot be converted to `string`"},
		{"index", "    val data = os.ReadFile(\"f\")\n    Println(data[0])",
			"it cannot be indexed"},
		{"tuple destructuring of a Try", "    val (data, err) = os.ReadFile(\"f\")\n    Println(data, err)",
			"a Try is not a Tuple, so it cannot be destructured with `val (...)`"},
		{"member of a Tuple", "    val c = strings.Cut(\"a=b\", \"=\")\n    Println(c.Before)",
			"`c` holds the result of `strings.Cut(...)`, which returns 3 values, so it is a `Tuple3[string, string, bool]`; a Tuple3 has no member `Before`"},
	}
	for _, tc := range rejected {
		t.Run("rejects/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goResultsProgram(tc.body), "go_results_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeGoCallResultAsValue))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}

	t.Run("hint names every way to the plain value", func(t *testing.T) {
		_, err := trans.Transpile(goResultsProgram("    Println(twice(strconv.Atoi(\"1\")))"), "go_results_test.gala")
		require.Error(t, err)
		for _, want := range []string{"`.Get()`", "`.GetOrElse(default)`", "match", "`val v, err = strconv.Atoi(...)`"} {
			assert.Contains(t, err.Error(), want)
		}
	})

	// A val field of a Go type with a multi-value Get(url) method: the
	// Immutable unwrap `.Get()` takes no arguments, so it is not that method.
	t.Run("keeps a val field whose Go type has a multi-value Get", func(t *testing.T) {
		out, err := trans.Transpile(goResultsProgram(`    val a = Api(Client = http.DefaultClient, Name = "x")
    Println(useIt(a.Client, 1))
    val c = a.Client
    Println(c == http.DefaultClient)`), "go_results_test.gala")
		require.NoError(t, err)
		assert.NotContains(t, out, "GoTry")
	})

	t.Run("converts a dot-imported (T, error) function", func(t *testing.T) {
		out, err := trans.Transpile(`package main

import . "strconv"

func main() {
    val n = Atoi("1")
    Println(n)
}`, "go_results_test.gala")
		require.NoError(t, err)
		assert.Contains(t, out, `std.GoTry(Atoi("1"))`)
	})
}

func goResultsProgram(body string) string {
	return `package main

import (
    "fmt"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "text/template"
)

struct Holder(N Try[int])

struct Plain(N int)

struct Api(Client *http.Client, Name string)

func twice(n int) int = n * 2

func describe(r Try[int]) string = if (r.IsSuccess()) "ok" else "failed"

func useIt(c *http.Client, n int) int = n

func each(f func(int)) {
    f(1)
}

func main() {
    Println(fmt.Sprint(1), net.IPv4len, http.MethodGet, os.Getenv("X"), filepath.Base("a"), strconv.Itoa(1), strings.ToUpper("a"), template.HTMLEscapeString("a"))
` + body + `
}`
}
