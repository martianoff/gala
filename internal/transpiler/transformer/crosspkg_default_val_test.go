package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crossPkgDefaultValFixture is a module whose library declares defaults that
// read the library's own package-level vals and vars, for lowering at a call
// or construction site in another package.
func crossPkgDefaultValFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/dval\n\ngala dev\n")
	write("lib/lib.gala", `package lib

import "strings"

val MaxK = 3
var Level = 1
val secret = 9

struct Opts(K int = MaxK, Lvl int = Level)

func Analyze(n int, maxK int = MaxK) int = n * maxK

func Leveled(n int, lvl int = Level) int = n + lvl

func Scaled(n int, f func(int) int = (a) => a * MaxK) int = f(n)

func Joined(s string, sep string = strings.Repeat("=", MaxK)) string = s + sep

func Hidden(n int, k int = secret) int = n + k

func Base(n int) int = n * 7

func Based(n int, k int = Base(2)) int = n + k

struct Box(N int)

func (b Box) Times(k int = MaxK) int = b.N * k

struct Pos(Line int, Col int)

val NoPos = Pos(0, 0)

struct Item(Name string, At Pos = NoPos)

func Origin(p Pos = Pos(1, 2)) int = p.Line + p.Col
`)
	// A sibling file whose defaults name a dot-imported package's function
	// and type, and the package's own type.
	write("lib/bag.gala", `package lib

import . "martianoff/gala/collection_immutable"

struct Bag(Items Array[Pos] = EmptyArray[Pos](), Pick func(Array[Pos]) int = (a) => a.Size())
`)
	// Hands out a lib value to a package that does not import lib itself.
	write("mk/mk.gala", `package mk

import "example.com/dval/lib"

func Make() lib.Box = lib.Box(2)
`)
	return root
}

// TestCrossPackageDefaultReadsDeclaringPackageVal: a default that reads a
// package-level val or var of the package declaring it is lowered at a use
// site in another package, so the name must be qualified to the declaring
// package — and a val read through its std.Immutable box — exactly as if the
// caller had written `lib.MaxK` itself. It used to be emitted bare, and the
// generated Go failed with `undefined: MaxK`.
func TestCrossPackageDefaultReadsDeclaringPackageVal(t *testing.T) {
	root := crossPkgDefaultValFixture(t)
	tests := []struct {
		name     string
		src      string
		contains []string
		absent   []string
	}{
		{
			name: "function parameter default",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Analyze(2))
}`,
			contains: []string{"lib.Analyze(2, lib.MaxK.Get())"},
		},
		{
			name: "var default stays a plain read",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Leveled(2))
}`,
			contains: []string{"lib.Leveled(2, lib.Level)"},
			absent:   []string{"lib.Level.Get()"},
		},
		{
			name: "inside a lambda default",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Scaled(2))
}`,
			contains: []string{"return a * lib.MaxK.Get()"},
		},
		{
			name: "argument to a call in the default",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Joined("a"))
}`,
			contains: []string{`strings.Repeat("=", lib.MaxK.Get())`},
		},
		{
			name: "method parameter default",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Box(4).Times())
}`,
			contains: []string{".Times(lib.MaxK.Get())"},
		},
		{
			name: "struct field defaults",
			src: `package main

import "example.com/dval/lib"

func main() {
    val o = lib.Opts()
    Println(o.K, o.Lvl)
}`,
			contains: []string{"lib.MaxK.Get()", "lib.Level"},
			absent:   []string{"lib.Level.Get()"},
		},
		{
			name: "a struct-typed val as a field default, positional and named",
			src: `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Item("x").At.Line, lib.Item(Name = "y").At.Col)
}`,
			contains: []string{
				`lib.Item{Name: std.NewImmutable("x"), At: std.NewImmutable(lib.NoPos.Get())}`,
				`lib.Item{Name: std.NewImmutable("y"), At: std.NewImmutable(lib.NoPos.Get())}`,
			},
		},
		{
			name: "aliased import",
			src: `package main

import l "example.com/dval/lib"

func main() {
    Println(l.Analyze(2), l.Box(1).Times())
}`,
			contains: []string{"l.Analyze(2, l.MaxK.Get())", ".Times(l.MaxK.Get())"},
		},
		{
			name: "dot import",
			src: `package main

import . "example.com/dval/lib"

func main() {
    Println(Analyze(2))
}`,
			contains: []string{"Analyze(2, MaxK.Get())"},
		},
		{
			// The caller's own binding of the same name was not in scope
			// where the default was written.
			name: "a caller local of the same name does not capture it",
			src: `package main

import "example.com/dval/lib"

func main() {
    val MaxK = 100
    Println(lib.Analyze(2), MaxK)
}`,
			contains: []string{"lib.Analyze(2, lib.MaxK.Get())"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want, "generated:\n%s", out)
			}
			for _, bad := range tc.absent {
				assert.NotContains(t, body, bad, "generated:\n%s", out)
			}
		})
	}

	// The default names an import of its declaring file, which the calling
	// file does not have: the import is added there. One the calling file
	// binds to another package is not reused; the import gets a free name.
	t.Run("an import of the declaring file is added", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Joined("a"))
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `import "strings"`, "generated:\n%s", out)
		assert.Contains(t, out, `strings.Repeat("=", lib.MaxK.Get())`, "generated:\n%s", out)
	})

	t.Run("a calling file's same-named import is not reused", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import (
    "example.com/dval/lib"
    strings "unicode/utf8"
)

func main() {
    Println(lib.Joined("a"), strings.RuneLen('a'))
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `strings2 "strings"`, "generated:\n%s", out)
		assert.Contains(t, out, `strings2.Repeat("=", lib.MaxK.Get())`, "generated:\n%s", out)
	})

	// The calling file reaches lib's method only through a value another
	// package hands it, and imports no lib of its own.
	t.Run("the declaring package is imported when the calling file lacks it", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/mk"

func main() {
    Println(mk.Make().Times())
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `"example.com/dval/lib"`, "generated:\n%s", out)
		assert.Contains(t, out, `.Times(lib.MaxK.Get())`, "generated:\n%s", out)
	})

	// The declaring file dot-imports collection_immutable; the calling file
	// does not import it at all. The default resolves through the declaring
	// file's dot import and is qualified here.
	t.Run("a dot import of the declaring file", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func main() {
    val b = lib.Bag()
    Println(b.Pick(b.Items))
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `"martianoff/gala/collection_immutable"`, "generated:\n%s", out)
		assert.Contains(t, out, `collection_immutable.EmptyArray[lib.Pos]()`, "generated:\n%s", out)
		assert.Contains(t, out, `func(a collection_immutable.Array[lib.Pos]) int`, "generated:\n%s", out)
	})

	// A `main` use site keys its own functions by bare name; one named like a
	// function the default calls must not stand in for the declaring
	// package's.
	t.Run("a caller function of the same name does not capture a default's call", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func Base() string = "local"

func main() {
    Println(lib.Based(1), Base())
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `lib.Based(1, lib.Base(2))`, "generated:\n%s", out)
	})

	// The same holds for a name the default constructs a type of, and for a
	// name it reaches through its file's dot import.
	t.Run("a caller function named like a type the default constructs does not capture it", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func Pos() string = "local"

func main() {
    Println(lib.Origin())
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `lib.Origin(lib.Pos{Line: std.NewImmutable(1), Col: std.NewImmutable(2)})`, "generated:\n%s", out)
	})

	t.Run("a caller function named like a dot-imported function does not capture it", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func EmptyArray(n int) int = n

func main() {
    val b = lib.Bag()
    Println(b.Items.Size(), EmptyArray(1))
}`)
		require.NoError(t, err)
		assert.Contains(t, out, `collection_immutable.EmptyArray[lib.Pos]()`, "generated:\n%s", out)
	})

	t.Run("an unexported val is reported at the default", func(t *testing.T) {
		_, err := transpileCrossPkg(t, root, `package main

import "example.com/dval/lib"

func main() {
    Println(lib.Hidden(1))
}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"secret", which is unexported in package "lib"`)
	})
}
