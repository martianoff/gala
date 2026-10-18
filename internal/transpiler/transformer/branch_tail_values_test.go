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
		{"if-expression branch of a hoisted if-expression", "val k = if (n > 1) { if (n > 5) { Some(20) } else { Some(n) } } else { return None() }\n    k"},
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

// A block lambda with a known result type must end in a value on every path,
// as a function with a result type must; Go would otherwise report a missing
// return.
func TestLambdaWithResultTypeMustProduceValue(t *testing.T) {
	trans := newBindTranspiler()
	for _, tc := range []struct{ name, body string }{
		{"void call", "val g = (k int) Option[int] => {\n        Println(k)\n    }\n    g(n)"},
		{"empty body", "val g = (k int) Option[int] => {\n    }\n    g(n)"},
		{"if with a branch that does not return", "val g = (k int) Option[int] => {\n        if (k == 7) {\n        } else {\n            return Some(k)\n        }\n    }\n    g(n)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) Option[int] {\n    " + tc.body + "\n}\n"
			_, err := trans.Transpile(input, "")
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), "this lambda returns Option[int], but its body can finish without a value")
			}
		})
	}
}

// A `return` gives a lambda that declares no result type its type; its
// trailing value is then a result value too, as in a lambda that declares it.
func TestLambdaReturnMakesTrailingValueAResult(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run(n int) Option[int] {
    val g = (k int) => {
        if (k > 3) {
            Some(k)
        } else {
            return None[int]()
        }
    }
    g(n)
}
`
	got, err := trans.Transpile(input, "")
	if assert.NoError(t, err) {
		assert.Contains(t, got, "return std.Some[int]{}.Apply(k)", "the trailing branch value is returned:\n%s", got)
	}
}

// The values a trailing if/else chain ends in must have one type: they are all
// values of the block the chain ends.
func TestTrailingIfElseValuesMustAgree(t *testing.T) {
	trans := newBindTranspiler()
	for _, tc := range []struct{ name, input, want string }{
		{
			"if-expression branch",
			"package main\n\nfunc g(a bool, b bool) string {\n    val z = if (a) { 2 } else { if (b) { 3 } else { \"s\" } }\n    s\"$z\"\n}\n",
			"the branches of this `if` end in values of different types",
		},
		{
			"bind block in a lambda with no result type",
			"package main\n\nfunc g(n int) Option[int] {\n    val h = (k int) => {\n        bind a = Some(k)\n        if (a > 1) { Some(a) } else { Some(\"small\") }\n    }\n    Some(1)\n}\n",
			"the values of this `bind` block have different types",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
}

// A branch of an if-expression that is a diverging call (`Panic`) runs as a
// statement: it has no value to return or store.
func TestIfExpressionPanicBranch(t *testing.T) {
	trans := newBindTranspiler()
	for _, tc := range []struct{ name, body string }{
		{"lowered to a function literal", "val x = if (n > 0) { Panic(\"a\") } else { 5 }\n    x"},
		{"inline", "val y = if (n > 0) 5 else { Println(\"side\"); Panic(\"b\") }\n    y"},
		{"lowered as statements", "val x = if (n > 0) { Panic(\"a\") } else if (n == 0) { return 0 } else { 5 }\n    x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) int {\n    " + tc.body + "\n}\n"
			got, err := trans.Transpile(input, "")
			if assert.NoError(t, err) {
				assert.NotContains(t, got, "return panic(", "a diverging branch is a statement:\n%s", got)
			}
		})
	}
}
