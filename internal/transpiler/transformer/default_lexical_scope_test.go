package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// defaultLexicalScopeFixture is a module where pa and pb declare the same
// names with different types, and lib's defaults name them through lib's own
// imports, or name pb's without importing it.
func defaultLexicalScopeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/lexical\n\ngala dev\n")
	write("pa/pa.gala", `package pa

struct Point(X string)

sealed type KindA {
    case Same()
    case OnlyA()
}
`)
	write("pb/pb.gala", `package pb

struct Point(X int)

sealed type KindB {
    case Same()
    case OnlyB()
}
`)
	// lib's own file dot-imports pa.
	write("lib/own.gala", `package lib

import . "example.com/lexical/pa"

func Matched(s string = Some(Same()) match {
    case Some(Same()) => "same"
    case _ => "other"
}) string = s

func Mapped(x string = Some(Point("a")).Map((p) => p.X).GetOrElse("")) string = x

struct Cfg(N int)

func Run(f func(Cfg) int = (c) => c.N) int = f(Cfg(1))
`)
	// pb is imported by name here: a file using the default that dot-imports
	// pb spells its declared type bare, which the default never wrote.
	write("lib/named.gala", `package lib

import "example.com/lexical/pb"

func Named(f func(pb.Point) int = (p) => p.X) int = f(pb.Point(2))
`)
	// The constructors these patterns name are pb's, which this file does
	// not import: only a file using the default does. The analyzer does not
	// check a pattern's constructor where the default is declared.
	write("lib/borrowed.gala", `package lib

val Thing any = 1

func Bare(s string = Thing match {
    case OnlyB() => "b"
    case _ => "other"
}) string = s

func Qualified(s string = Thing match {
    case pb.OnlyB() => "b"
    case _ => "other"
}) string = s
`)
	return root
}

// TestDefaultLexicalScope: a default is resolved where it is written. Lowered
// in another file — of its package or of another one — its bare names resolve
// through its own file's dot imports only, never through the using file's,
// even where the using file dot-imports a package declaring the same names.
// A name that only the using file's imports resolve is reported as undefined,
// at its own token in the default's file.
func TestDefaultLexicalScope(t *testing.T) {
	root := defaultLexicalScopeFixture(t)

	t.Run("another package, a using file that dot-imports the same names", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import (
    . "example.com/lexical/pb"
    "example.com/lexical/lib"
)

func main() {
    Println(lib.Matched(), lib.Mapped(), lib.Named(), Point(1), OnlyB())
}
`)
		require.NoError(t, err)
		assert.Contains(t, out, "func(p Point) int", "lib.Named's declared type, spelled for this file:\n%s", out)
		for _, want := range []string{"pa.Same{}", "func(p pa.Point) string"} {
			assert.Contains(t, out, want, "generated:\n%s", out)
		}
	})

	// The using file's dot import of lib is out of scope while lib's default
	// is lowered, but the generated code is still spelled with it: the
	// lambda's parameter type is a bare Cfg, not lib.Cfg (which this file
	// cannot name).
	t.Run("another package, a using file that dot-imports the declaring package", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import . "example.com/lexical/lib"

func main() {
    Println(Run())
}
`)
		require.NoError(t, err)
		assert.Contains(t, out, "func(c Cfg) int", "generated:\n%s", out)
		assert.NotContains(t, out, "lib.Cfg", "generated:\n%s", out)
	})

	t.Run("another file of the package, a using file that dot-imports the same names", func(t *testing.T) {
		out, err := transpileCrossPkgFile(t, root, `package lib

import . "example.com/lexical/pb"

func Use() string = Matched() + Mapped() + s"${Point(1).X}${OnlyB()}"
`, filepath.Join(root, "lib", "use.gala"))
		require.NoError(t, err)
		for _, want := range []string{"pa.Same{}", "func(p pa.Point) string", `"example.com/lexical/pa"`} {
			assert.Contains(t, out, want, "generated:\n%s", out)
		}
	})

	t.Run("another file of the package, a using file that imports nothing", func(t *testing.T) {
		out, err := transpileCrossPkgFile(t, root, `package lib

func Use() string = Matched() + Mapped()
`, filepath.Join(root, "lib", "use.gala"))
		require.NoError(t, err)
		assert.Contains(t, out, "pa.Same{}", "generated:\n%s", out)
	})

	rejected := []struct {
		name, call string
		using      string // the using file's import of pb
		line, col  int
		undefined  string
	}{
		{"a bare constructor only the using file's dot import declares", "lib.Bare()", `. "example.com/lexical/pb"`, 6, 9, "OnlyB"},
		{"a qualifier only the using file binds", "lib.Qualified()", `"example.com/lexical/pb"`, 11, 9, "pb"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import (
    `+tt.using+`
    "example.com/lexical/lib"
)

func main() {
    Println(`+tt.call+`)
}
`)
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "%v", err)
			assert.Equal(t, galaerr.CodeUndefinedVariable, se.Code, "%v", err)
			assert.Equal(t, "undefined: "+tt.undefined, se.Msg, "%v", err)
			assert.True(t, strings.HasSuffix(filepath.ToSlash(se.FilePath), "lib/borrowed.gala"), "reported in the default's file, got %q", se.FilePath)
			assert.Equal(t, tt.line, se.Line, "%v", err)
			assert.Equal(t, tt.col, se.Column, "%v", err)
			assert.Contains(t, se.Hint, `"example.com/lexical/pb"`)
		})
	}
}
