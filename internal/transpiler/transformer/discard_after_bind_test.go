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
	t.Run("a read of the clause's `_`", func(t *testing.T) {
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    Some(_)\n}\n\nfunc main() {\n    Println(run())\n}\n", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
	})
}
