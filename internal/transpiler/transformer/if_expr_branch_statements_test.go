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

// A val declared in an if-expression's block branch is local to the branch:
// after the if-expression the outer name keeps its type, so a closure over
// it returns a string. The branch's value is typed in the branch's scope,
// so a value naming the shadowing val keeps the shadowing val's type. The
// runtime example if_expr_branch_scope covers the same cases end to end.
func TestIfExpressionBranchValIsLocalToTheBranch(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"then branch", `
    val r = if (c) {
        val m = 41
        m + 1
    } else 0`, []string{"var r = func() int {"}},
		{"branch value is the shadowing val", `
    val r = if (c) {
        val m = 41
        m
    } else 0`, []string{"var r = func() int {"}},
		{"else branch of a hoisted if-expression", `
    val r = if (c) {
        if (n > 100) { return "big" }
        n
    } else {
        val m = 2
        m
    }`, []string{`"%s %d"`}},
		{"nested branches", `
    val r = if (c) {
        val m = 10
        val inner = if (n > 1) {
            val m = "inner"
            m.Size()
        } else {
            val m = true
            if (m) 1 else 0
        }
        m + inner
    } else 0`, []string{"var inner = func() int {"}},
		{"bind continuation", `
    val r = if (c) {
        val m = 5
        bind x = Some(m)
        Some(x + m)
    } else None()`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc run(c bool, n int) string {\n    val m = \"outer\"" + tc.body + `
    val f = () => m
    s"${f()} $r"
}
`
			assertTranspiled(t, src, append(tc.want, "var f = func() string {"), nil)
		})
	}
}
