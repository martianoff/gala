package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoMultiValueInSingleValueSlot covers GALA-E0049: a Go call returning two
// or more values, standing where GALA needs one value. Each of these used to
// transpile cleanly and fail in `go build` — the match subject was typed as the
// call's first result, so `case (data, nil)` read `obj.V1` off a []byte.
//
// Hermetic: `os`, `strconv` and `strings` resolve through the Go SDK, which
// CI provides via .bazelrc --action_env=GOROOT.
func TestGoMultiValueInSingleValueSlot(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	rejected := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name: "match subject, (T, error) call",
			body: `    val m = os.ReadFile("f") match {
        case (_, nil) => "read"
        case _ => "failed"
    }
    Println(m)`,
			contains: "os.ReadFile returns 2 values, but a match subject takes a single value",
		},
		{
			name: "match subject, call without an error result",
			body: `    val m = strings.Cut("a=b", "=") match {
        case (k, _, true) => k
        case _ => ""
    }
    Println(m)`,
			contains: "strings.Cut returns 3 values, but a match subject takes a single value",
		},
		{
			name:     "tuple destructuring",
			body:     "    val (n, err) = strconv.Atoi(\"1\")\n    Println(s\"$n $err\")",
			contains: "cannot be destructured with `val (...)`",
		},
		{
			name:     "val binding one name to a call without an error result",
			body:     "    val c = strings.Cut(\"a=b\", \"=\")\n    Println(c)",
			contains: "strings.Cut returns 3 values, but a binding of one name takes a single value",
		},
		{
			name:     "var binding one name",
			body:     "    var v = strconv.Atoi(\"1\")\n    Println(v)",
			contains: "a binding of one name takes a single value",
		},
		{
			name:     "if-expression branch",
			body:     "    val r = if (true) strconv.Atoi(\"1\") else strconv.Atoi(\"2\")\n    Println(r)",
			contains: "an if-expression branch takes a single value",
		},
		{
			name:     "if-expression block branch",
			body:     "    val r = if (true) { strconv.Atoi(\"1\") } else { strconv.Atoi(\"2\") }\n    Println(r)",
			contains: "an if-expression branch takes a single value",
		},
		{
			name:     "expression-lambda body without an error result",
			body:     "    val f = () => strings.Cut(\"a=b\", \"=\")\n    Println(f())",
			contains: "a lambda body takes a single value",
		},
		{
			name:     "argument to a Tuple parameter",
			body:     "    Println(takesPair(strings.Cut(\"a=b\", \"=\")))",
			contains: "this argument takes a single value",
		},
		{
			name:     "one argument of several",
			body:     "    Println(twoArgs(\"a\", strconv.Atoi(\"1\")))",
			contains: "strconv.Atoi returns 2 values, but this argument takes a single value",
		},
	}
	for _, tc := range rejected {
		t.Run("rejects/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goMultiValueProgram(tc.body), "go_multi_value_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeGoMultiValueInSingleValueSlot))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}

	// The documented ways to reach a Go call's results must keep compiling.
	accepted := []struct {
		name string
		body string
	}{
		{"Try over a (T, error) call", `    val r = Try(strconv.Atoi("1")) match {
        case Success(n) => n
        case Failure(_) => 0
    }
    Println(r)`},
		{"multi-name binding, then a tuple match", `    val k, v, found = strings.Cut("a=b", "=")
    val r = (k, v, found) match {
        case (a, b, true) => a + b
        case _ => ""
    }
    Println(r)`},
		{"single name over a (T, error) call panics on the error", "    val n = strconv.Atoi(\"1\")\n    Println(n)"},
		{"expression lambda over a (T, error) call", "    val f = () => strconv.Atoi(\"1\")\n    Println(f())"},
		{"sole argument of a Go function", "    fmt.Println(strconv.Atoi(\"1\"))"},
		{"if statement whose branches call a multi-value Go function", "    if (strings.HasPrefix(\"ab\", \"a\")) { fmt.Println(\"a\") } else { fmt.Println(\"b\") }"},
	}
	for _, tc := range accepted {
		t.Run("accepts/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(goMultiValueProgram(tc.body), "go_multi_value_test.gala")
			require.NoError(t, err)
		})
	}
}

func goMultiValueProgram(body string) string {
	return `package main

import (
    "fmt"
    "os"
    "strconv"
    "strings"
)

func takesPair(p Tuple[string, bool]) string = p.V1

func twoArgs(a string, b int) string = a

func main() {
    Println(os.Getenv("HOME") + fmt.Sprint(1) + strconv.Itoa(1) + strings.ToUpper("a") + takesPair(("a", true)) + twoArgs("a", 1))
` + body + `
}`
}
