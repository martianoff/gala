package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestDiscardAfterBindUnderscore pins that `_ = x` after a `bind _ = m`
// clause is a discard: the clause binds `_` as a val, but a `_` target is
// Go's blank identifier, not that val. Reading the clause's `_` is still
// GALA-E0139.
func TestDiscardAfterBindUnderscore(t *testing.T) {
	const header = "package main\n\nfunc step() Option[int] = Some(1)\n\nfunc run() Option[int] {\n    bind _ = step()\n"
	t.Run("a discard after the clause", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    _ = \"ignored\"\n    Some(2)\n}\n\nfunc main() {\n    Println(run())\n}\n", "")
		require.NoError(t, err)
	})
	t.Run("a discard among several targets after the clause", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    var n = 0\n    n, _ = 5, 6\n    Some(n)\n}\n\nfunc main() {\n    Println(run())\n}\n", "")
		require.NoError(t, err)
	})
	t.Run("an increment of the clause's `_`", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    _++\n    Some(2)\n}\n\nfunc main() {\n    Println(run())\n}\n", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
	})
	t.Run("a read of the clause's `_`", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    Some(_)\n}\n\nfunc main() {\n    Println(run())\n}\n", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
	})
}

// TestAlsoUnderscoreClause pins that the `_` of a `bind _ = m` clause that
// starts a Zip-lowered `also` group is bound as on the sequential path: a
// read is GALA-E0139, `_ = x` is a discard, and the group still runs.
func TestAlsoUnderscoreClause(t *testing.T) {
	const header = "package main\n\nimport . \"martianoff/gala/concurrent\"\n\nfunc compute(n int) Future[int] = Future[int](n * n)\n\nfunc total() Future[int] {\n    bind _ = compute(2)\n    also b = compute(3)\n"
	const footer = "    Future[int](b)\n}\n\nfunc main() {\n    Println(total().Get())\n}\n"
	t.Run("a read of the clause's `_`", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    Println(_)\n"+footer, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
		require.Contains(t, err.Error(), "line 10:12 ")
	})
	t.Run("a discard after the group", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    _ = \"ignored\"\n"+footer, "")
		require.NoError(t, err)
	})
	t.Run("no read of the clause's `_`", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+footer, "")
		require.NoError(t, err)
	})
}
