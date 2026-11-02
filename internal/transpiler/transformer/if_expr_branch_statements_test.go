package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An if-expression whose value nothing reads, before an if-expression
// branch's value, is a Go if statement, not a value IIFE.
func TestDiscardedIfInIfExpressionBranchIsAnIfStatement(t *testing.T) {
	assertTranspiled(t, `package main

func run(c bool, n int) int {
    val v = if (c) {
        if (n > 1) Println("big") else Println("small")
        n * 10
    } else 0
    v
}
`, []string{"if n > 1 {"}, nil)
}

// A statement-position match lowers its subject as a value: a match in the
// condition of an if statement inside the subject is value-consumed, not a
// statement-position match of its own.
func TestStatementMatchSubjectIsNotStatementPosition(t *testing.T) {
	assertTranspiled(t, `package main

func run(c bool, n int) {
    (if (c) {
        if (n match { case 1 => true case _ => false }) Println("one") else Println("many")
        n
    } else 0) match {
        case 0 => Println("zero")
        case _ => Println("other")
    }
}
`, []string{`"one"`, `"zero"`}, nil)
}

// The statements before the value of an if-expression's block branch, and
// those of a `bind` continuation, are lowered as in any other block, so an
// unused value there is a GALA error, not a Go one. The runtime example
// if_expr_branch_statements covers the statement-position match and the
// discarded if-expression.
func TestUnusedValueInIfExpressionBranchIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"if-expression branch", `package main

func run(c bool, n int) int {
    val v = if (c) { n + 1; 5 } else 2
    v
}
`},
		{"bind continuation", `package main

func run(n int) int = Some(n).FlatMap((m) => {
    bind x = Some(m)
    n + 1
    Some(x)
}).GetOrElse(0)
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "`n+1` is evaluated but not used")
		})
	}
}

// An `also` that follows no `bind` is rejected wherever a block's statements
// are lowered: a block, an if-expression branch, before its value or at its
// end, and a `bind` continuation.
func TestAlsoWithoutBindIsRejectedInEveryBlock(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"block", `package main

func run(n int) Option[int] {
    also y = Some(n)
    Some(y)
}
`},
		{"if-expression branch", `package main

func run(c bool, n int) Option[int] {
    val v = if (c) {
        also y = Some(n)
        Some(y)
    } else Some(0)
    v
}
`},
		{"end of an if-expression branch", `package main

func run(c bool, n int) Option[int] {
    val v = if (c) {
        Println(n)
        also y = Some(n)
    } else Some(0)
    v
}
`},
		{"bind continuation", `package main

func run(n int) Option[int] {
    bind x = Some(n)
    Println(x)
    also y = Some(x)
    Some(y)
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "`also` must directly follow a `bind` or `also`")
		})
	}
}
