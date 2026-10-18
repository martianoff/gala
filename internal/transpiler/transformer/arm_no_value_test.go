package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An arm of a match whose value is used, or of a partial function, must end
// in a value when another arm does: an empty block, a trailing declaration,
// assignment or void call leaves the arm without one.
func TestArmWithoutValueErrors(t *testing.T) {
	const matchErr = "this arm of the `match` produces no value, but another arm does"
	const partialErr = "this case of the partial function produces no value"
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "empty block arm of a match whose value is stored",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => { }
        case _ => 5
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "arm block ending in a declaration",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => { val a = 3 }
        case _ => 5
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "arm block ending in a void call",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => { Println("x") }
        case _ => 5
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "expression arm that is a void call",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => Println("x")
        case _ => 5
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "empty default arm",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => 5
        case _ => { }
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "arm block ending in an if with no else",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => { if (n > 0) { Println("x") } }
        case _ => 5
    }
    k
}
`,
			wantErr: matchErr,
		},
		{
			name: "empty arm of a match that is a function's value",
			body: `
func f(n int) int = n match {
    case 1 => { }
    case _ => 5
}
`,
			wantErr: matchErr,
		},
		{
			name: "empty block arm of a partial function",
			body: `
func f() {
    val pf = { case 1 => { } case 2 => 5 }
    Println(pf(1))
}
`,
			wantErr: partialErr,
		},
		{
			name: "partial function arm block ending in a declaration",
			body: `
func f() {
    val pf = { case 1 => { val a = 3 } case 2 => 5 }
    Println(pf(1))
}
`,
			wantErr: partialErr,
		},
		{
			name: "partial function arm block ending in a void call",
			body: `
func f() {
    val pf = { case 1 => { Println("x") } case 2 => 5 }
    Println(pf(1))
}
`,
			wantErr: partialErr,
		},
		{
			name: "partial function expression arm that is a void call",
			body: `
func f() {
    val pf = { case 1 => Println("x") case 2 => 5 }
    Println(pf(1))
}
`,
			wantErr: partialErr,
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

// Arms with no value stay valid where no value is needed of them: in a match
// whose value is discarded, in a match none of whose arms yields one, and in
// an arm that leaves (a diverging call or a `return`).
func TestArmWithoutValueStillValid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "statement-position match with an empty arm and a value arm",
			body: `
func g(n int) int = n * 2

func f(n int) {
    n match {
        case 1 => { }
        case _ => g(n)
    }
    Println(n)
}
`,
		},
		{
			name: "statement-position match at the tail of a void function",
			body: `
func g(n int) int = n * 2

func f(n int) {
    n match {
        case 1 => { val a = 3 }
        case _ => g(n)
    }
}
`,
		},
		{
			name: "match whose arms are all void",
			body: `
func f(n int) {
    n match {
        case 1 => { }
        case 2 => { Println("two") }
        case _ => Println(n)
    }
}
`,
		},
		{
			name: "all-void match in a lambda body",
			body: `
func each(f func(int)) = f(1)

func v() {
    each((x) => x match {
        case 1 => { }
        case _ => Println(x)
    })
}
`,
		},
		{
			name: "diverging arm next to a value arm",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => Panic("one")
        case 2 => {
            Println("two")
            Panic("two")
        }
        case _ => 5
    }
    k
}
`,
		},
		{
			name: "arm that returns from the enclosing function",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => { return 0 }
        case _ => 5
    }
    k + 1
}
`,
		},
		{
			name: "arm whose trailing if/else carries the value",
			body: `
func f(n int) int {
    val k = n match {
        case 1 => {
            Println("one")
            if (n > 0) { 1 } else { 2 }
        }
        case _ => 5
    }
    k
}
`,
		},
		{
			name: "partial function arms that end in values or diverge",
			body: `
func f() {
    val pf = { case 1 => { val a = 3; a } case 2 => Panic("two") case 3 => 5 }
    Println(pf(1))
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.NoError(t, err)
		})
	}
}
