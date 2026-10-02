package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A block-bodied function declared with a result type returns its trailing
// expression, lowered like `return expr`: generic functions, methods, nested
// if/else branches and zero-argument constructors typed by the result type.
func TestBlockFunctionTrailingValue(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "generic function returns its trailing parameter",
			body: `
func f[U any](o Option[U]) Option[U] {
    Println("x")
    o
}
`,
			contains: []string{"return o\n"},
		},
		{
			name: "method returns its trailing val",
			body: `
struct Box(n int)

func (b Box) Double() int {
    val d = b.n * 2
    d
}
`,
			contains: []string{"return d.Get()"},
		},
		{
			name: "trailing None() takes the result type",
			body: `
func none() Option[string] {
    Println("none")
    None()
}
`,
			contains: []string{"return std.None[string]{}"},
		},
		{
			name: "nested trailing if/else branches return",
			body: `
func sign(n int) string {
    if (n < 0) {
        "negative"
    } else {
        if (n == 0) { "zero" } else { "positive" }
    }
}
`,
			contains: []string{`return "negative"`, `return "zero"`, `return "positive"`},
		},
		{
			name: "trailing Panic stays a diverging statement",
			body: `
func must(n int) int {
    if (n > 0) {
        return n
    }
    Panic("no")
}
`,
			contains: []string{`panic("no")`},
		},
		{
			name: "qualified go_builtins Panic under an alias diverges",
			body: `
import gb "martianoff/gala/go_builtins"

func must(n int) int {
    if (n > 0) {
        return n
    }
    gb.Panic("no")
}
`,
			contains: []string{`panic("no")`},
		},
		{
			name: "a user method named Panic is an ordinary value",
			body: `
struct Engine(code int)

func (e Engine) Panic(msg string) int = e.code

func (e Engine) Fail() int {
    Println("failing")
    e.Panic("x")
}
`,
			contains: []string{`return e.Panic("x")`},
		},
		{
			name: "a user method named Panic is a match arm value",
			body: `
struct Engine(code int)

func (e Engine) Panic(msg string) int = e.code

func pick(e Engine, n int) int = n match {
    case 0 => e.Panic("zero")
    case _ => n
}
`,
			contains: []string{`return e.Panic("zero")`},
		},
		{
			name: "a match at the tail of a lambda's if branch is the branch value",
			body: `
func apply[U any](x int, f func(int) U) U = f(x)

func g() string = apply(2, (x) => {
    if (x > 1) {
        x match {
            case 2 => "two"
            case _ => "many"
        }
    } else {
        "one"
    }
})
`,
			contains: []string{`return "one"`, `return "two"`},
		},
		{
			name: "a trailing explicit return is unchanged",
			body: `
func twice(n int) int {
    val d = n * 2
    return d
}
`,
			contains: []string{"return d.Get()"},
		},
		{
			name: "early returns work alongside an implicit trailing value",
			body: `
func classify(n int) string {
    if (n < 0) {
        return "negative"
    }
    var i = 1
    for i < 4 {
        if (i == n) {
            return "member"
        }
        i = i + 1
    }
    n match {
        case 0 => { return "zero" }
        case _ => { Println("other") }
    }
    "positive"
}
`,
			contains: []string{`return "negative"`, `return "member"`, `return "zero"`, `return "positive"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// An expression-bodied function with no result type is void: GALA does not
// infer a result type, so `func f() = <expr>` lowers as `func f() { <expr> }`.
// The generated-Go oracle type-checks each output, which rejects a `return`
// of a value from a void Go function.
func TestVoidExpressionFunction(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
		excludes []string
	}{
		{
			name: "call returning a value is a call statement",
			body: `
func fortyTwo() int = 42

func r() = fortyTwo()
`,
			contains: []string{"func r() {\n\tfortyTwo()\n}"},
		},
		{
			name: "Go call with several results is a call statement",
			body: `
func greet(name string) = Println(s"hello $name")
`,
			contains: []string{"func greet(name string) {\n\tfmt.Println("},
			excludes: []string{"return"},
		},
		{
			name: "void call",
			body: `
func each(f func(int)) = f(1)
`,
			contains: []string{"func each(f func(int)) {\n\tf(1)\n}"},
		},
		{
			name: "method",
			body: `
struct Box(n int)

func (b Box) Show() = Println(b.n)
`,
			contains: []string{"func (b Box) Show() {\n\tfmt.Println(b.n.Get())\n}"},
		},
		{
			name: "match runs its arms as statements",
			body: `
func describe(n int) = n match {
    case 0 => Println("zero")
    case _ => Println(n)
}
`,
			contains: []string{`fmt.Println("zero")`, "fmt.Println(n)"},
			excludes: []string{"return fmt.Println", "return func"},
		},
		{
			name: "if-expression is an if statement",
			body: `
func sign(n int) = if (n < 0) Println("negative") else Println("non-negative")
`,
			contains: []string{"if n < 0 {"},
			excludes: []string{"return"},
		},
		{
			name: "self-recursive if-expression is ordinary recursion",
			body: `
func countdown(n int) = if (n == 0) Println("liftoff") else countdown(n - 1)
`,
			contains: []string{"countdown(n - 1)"},
			excludes: []string{"return"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, unwanted := range tt.excludes {
				assert.NotContains(t, got, unwanted)
			}
		})
	}
}

// A body that cannot produce its function's value, and a value computed only
// to be discarded, are GALA errors rather than Go "missing return" or
// "is not used" errors.
func TestBlockFunctionTailErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "trailing void call in a value-returning function",
			body: `
func w() int {
    Println("w")
}
`,
			wantErr: "function w returns int, but ends in `Println(\"w\")`, which produces no value",
		},
		{
			name: "trailing assignment in a value-returning function",
			body: `
func w() int {
    var x = 1
    x = 2
}
`,
			wantErr: "function w returns int, but its body can finish without a value",
		},
		{
			name: "if without else at the tail of a value-returning method",
			body: `
struct Box(n int)

func (b Box) Pos() int {
    if (b.n > 0) {
        b.n
    }
}
`,
			wantErr: "function Box.Pos returns int, but its body can finish without a value",
		},
		{
			name: "a generic method is named as written, not by its lowered name",
			body: `
struct Box(n int)

func (b Box) Pick[T any](x T) T {
    if (b.n > 0) {
        x
    }
}
`,
			wantErr: "function Box.Pick returns T, but its body can finish without a value",
		},
		{
			name: "if with no else at the tail of a value-returning lambda",
			body: `
func each(f func(int) int) int = f(1)

func v() int = each((x) => {
    if (x > 0) {
        x
    }
})
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "if/else at the tail of a lambda with no value expected",
			body: `
func v() {
    val f = (x int) => {
        if (x > 0) { x } else { 0 }
    }
    f(1)
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "bare value at the tail of a lambda with no value expected",
			body: `
func v() {
    val f = (x int) => {
        Println(x)
        x
    }
    f(1)
}
`,
			wantErr: "`x` is evaluated but not used; remove it, or make it the lambda's result",
		},
		{
			name: "if/else with a non-value branch at the tail of a match arm",
			body: `
func v(n int) int = n match {
    case 0 => 1
    case _ => {
        var y = 0
        if (n > 1) { n } else { y = 2 }
    }
}
`,
			wantErr: "this branch of the `if` produces no value, but another branch does",
		},
		{
			name: "if/else with a void-call branch at the tail of a value lambda",
			body: `
func apply[U any](x int, f func(int) U) U = f(x)

func g() int = apply(2, (x) => {
    if (x > 1) { x * 10 } else { Println("a") }
})
`,
			wantErr: "this branch of the `if` produces no value, but another branch does",
		},
		{
			name: "trailing void call in a method names the method",
			body: `
struct Box(n int)

func (b Box) Pick() int {
    Println("w")
}
`,
			wantErr: "function Box.Pick returns int, but ends in `Println(\"w\")`, which produces no value",
		},
		{
			name: "empty body of a value-returning function",
			body: `
func e() string {
}
`,
			wantErr: "function e returns string, but its body can finish without a value",
		},
		{
			name: "trailing bare parameter in a void function",
			body: `
func v(x int) {
    Println("v")
    x
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "trailing bare val in a void function",
			body: `
func v() {
    val x = 1
    x
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "bare value before the tail of a value-returning function",
			body: `
func g(x int) int {
    x
    x + 1
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "bare value at the tail of a nested if body",
			body: `
func v(x int) {
    if (x > 0) {
        x
    }
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name: "bare value at the tail of a lambda passed where no value is expected",
			body: `
func each(f func(int)) = f(1)

func v() {
    each((x) => {
        Println(x)
        x
    })
}
`,
			wantErr: "`x` is evaluated but not used",
		},
		{
			name:    "literal body of a function with no result type",
			body:    "\nfunc s() = 42\n",
			wantErr: "`42` is evaluated but not used; declare the function's result type to return it, as in `func f() int = 42`",
		},
		{
			name:    "operator body of a function with no result type",
			body:    "\nfunc s(x int) = x + 1\n",
			wantErr: "`x+1` is evaluated but not used; declare the function's result type",
		},
		{
			name:    "parameter body of a function with no result type",
			body:    "\nfunc s(x int) = x\n",
			wantErr: "`x` is evaluated but not used; declare the function's result type",
		},
		{
			name:    "lambda body of a function with no result type",
			body:    "\nfunc s() = (x int) => x + 1\n",
			wantErr: "is evaluated but not used; declare the function's result type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
