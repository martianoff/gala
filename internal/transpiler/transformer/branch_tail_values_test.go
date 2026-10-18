package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A block whose value is used may end in an if/else chain: its branches carry
// the value. Each branch's value is lowered against the type the block fills,
// so a `None()` there needs no type argument.
//
// Every successful output also goes through the package's Go type-check oracle.
func TestTrailingIfElseCarriesBlockValue(t *testing.T) {
	trans := newBindTranspiler()
	for _, tc := range []struct{ name, body string }{
		{"if-expression branch", "val k = if (n > 1) { if (n > 5) { Some(20) } else { None() } } else { Some(0) }\n    k"},
		{"if-expression branch with else-if", "val k = if (n > 1) {\n        val m = n * 2\n        if (m > 9) { None() } else if (m > 5) { Some(m) } else { Some(1) }\n    } else Some(0)\n    k"},
		{"if-expression branch of a hoisted if-expression", "val k = if (n > 1) { if (n > 5) { Some(20) } else { None() } } else { return None() }\n    k"},
		{"typed match arm", "val r Option[int] = n match {\n        case 1 => { if (n > 0) { None() } else { Some(1) } }\n        case _ => Some(2)\n    }\n    r"},
		{"untyped match arm next to a typed one", "val r = n match {\n        case 1 => { if (n > 0) { None() } else { Some(1) } }\n        case _ => Some(2)\n    }\n    r"},
		{"lambda body", "val g = (m int) Option[int] => {\n        if (m > 0) { None() } else { Some(1) }\n    }\n    g(n)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) Option[int] {\n    " + tc.body + "\n}\n"
			_, err := trans.Transpile(input, "")
			assert.NoError(t, err)
		})
	}
}

// A branch of an if-expression whose value is used must produce one. A branch
// that is empty, or ends in a declaration or a void call, is a GALA error
// rather than a `return nil` Go rejects.
func TestIfExpressionBranchWithoutValueIsRejected(t *testing.T) {
	trans := newBindTranspiler()
	const msg = "this branch of the if-expression produces no value, but the if-expression's value is used"
	for _, tc := range []struct{ name, body string }{
		{"empty branch", "val o = if (n == 1) { } else { 0 }\n    o"},
		{"branch ending in a val", "val o = if (n == 1) { val a = 1 } else { 0 }\n    o"},
		{"branch ending in a void call", "val o = if (n == 1) { Println(n) } else { 0 }\n    o"},
		{"inline void call", "val o = if (n == 1) Println(n) else 0\n    o"},
		{"hoisted if-expression", "val o = if (n == 1) { val a = 1 } else if (n == 2) { return 3 } else { 0 }\n    o"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) int {\n    " + tc.body + "\n}\n"
			_, err := trans.Transpile(input, "")
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), msg)
			}
		})
	}
}
