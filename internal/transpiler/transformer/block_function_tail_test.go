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
			wantErr: "function returning int ends in `Println(\"w\")`, which produces no value",
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
			wantErr: "function Pos returns int, but its body can finish without a value",
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
