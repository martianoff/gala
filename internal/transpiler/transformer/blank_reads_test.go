package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestUnboundUnderscoreReadInDefault pins GALA-E0139 for an unbound `_` in a
// default lowered at a use site in another package: it is reported in the
// default's own file, where the `_` is written.
func TestUnboundUnderscoreReadInDefault(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) string {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
		return full
	}
	write("gala.mod", "module example.com/blank\n\ngala dev\n")
	libFile := write("lib/lib.gala", "package lib\n\nfunc Inc(n int) int = n + 1\n\nfunc F(n int = Inc(_)) int = n\n")
	_, err := transpileCrossPkg(t, root, "package main\n\nimport \"example.com/blank/lib\"\n\nfunc main() {\n    Println(lib.F())\n}\n")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "%v", err)
	require.Equal(t, galaerr.CodeUnderscoreBindingRead, se.Code, "%v", err)
	require.Equal(t, libFile, se.FilePath)
	require.Equal(t, 5, se.Line)
}

// TestUnboundUnderscoreRead pins GALA-E0139 at a read of a `_` that nothing
// binds, decided on the finished file: an argument lowered on trial before
// it turns out to be a placeholder lambda reports nothing.
func TestUnboundUnderscoreRead(t *testing.T) {
	const want = "`_` is read here, but nothing binds it: `_` names no value"
	rejected := []struct{ name, src, at string }{
		{"an argument of a non-function slot",
			"package main\n\nfunc inc(n int) int = n + 1\n\nfunc main() {\n    Println(inc(_))\n}\n", "line 6:16 "},
		{"an operand outside any argument",
			"package main\n\nfunc main() {\n    val y = 1\n    Println(y + _)\n}\n", "line 5:16 "},
		{"an `also` clause's receiver",
			"package main\n\nimport . \"martianoff/gala/concurrent\"\n\nfunc compute(n int) Future[int] = Future[int](n * n)\n\nfunc g(n int) Future[int] = Future[int](n + 1)\n\nfunc total() Future[int] {\n    bind a = compute(2)\n    also b = g(_)\n    Future[int](a + b)\n}\n\nfunc main() {\n    Println(total().Get())\n}\n", "line 11:15 "},
		// No placeholder lambda takes a `_` inside an if-expression argument
		// yet (#1346); its read is reported rather than left to Go.
		{"inside an if-expression argument",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    Println(ArrayOf(1, -2).Map(if (_ > 0) 1 else 2))\n}\n", "line 6:35 "},
		{"the first of two reads",
			"package main\n\nfunc add(a int, b int) int = a + b\n\nfunc main() {\n    Println(add(_, 1))\n    Println(add(2, _))\n}\n", "line 6:16 "},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
			require.Contains(t, err.Error(), tc.at+want)
		})
	}

	accepted := []struct{ name, src string }{
		{"a placeholder lambda in a function slot",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    Println(ArrayOf(1, 2).Map(_ + 1))\n}\n"},
		{"a placeholder lambda through a nested call",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc inc(n int) int = n + 1\n\nfunc main() {\n    Println(ArrayOf(1, 2).Map(inc(_)))\n}\n"},
		{"placeholder lambdas of two slots",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    Println(ArrayOf(1, 2).FoldLeft(0, _ + _))\n}\n"},
		{"a discard",
			"package main\n\nfunc main() {\n    _ = 1\n}\n"},
		{"a discard among several targets",
			"package main\n\nfunc main() {\n    var n = 0\n    n, _ = 1, 2\n    Println(n)\n}\n"},
		{"a wildcard sub-pattern",
			"package main\n\nfunc main() {\n    val o = Some(1)\n    Println(o match { case Some(_) => 1 case _ => 0 })\n}\n"},
		{"a placeholder lambda as a named argument",
			"package main\n\nfunc apply(x int, f func(int) int) int = f(x)\n\nfunc main() {\n    Println(apply(x = 1, f = _ + 1))\n}\n"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.NoError(t, err)
		})
	}
}
