package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copyCrossPkgFixture is a module whose library package declares the structs
// the tests below copy with overrides from another package.
func copyCrossPkgFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/copyx\n\ngala dev\n")
	write("lib/lib.gala", `package lib

struct Point(X int, Y int)

struct Pair[A any, B any](First A, Second B)

struct Holder(P Point, Name string)

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}

func Origin() Point = Point(0, 0)

struct Account(Name string, balance int)

func NewAccount(name string, balance int) Account = Account(name, balance)
`)
	return root
}

// TestCopyOverridesCrossPackage: `.Copy(field = value)` on another package's
// struct, for every way the file can name that package and every receiver
// shape. A constructor call through an import alias lowers to a literal
// `l.Point{...}` whose type named the alias rather than the package, so the
// struct's fields were not found and the copy was rejected with "Copy
// overrides only supported for struct types".
func TestCopyOverridesCrossPackage(t *testing.T) {
	root := copyCrossPkgFixture(t)
	type testCase struct {
		name, src, want string
	}
	var tests []testCase
	imports := []struct{ name, imp, q string }{
		{"aliased import", `import l "example.com/copyx/lib"`, "l."},
		{"qualified import", `import "example.com/copyx/lib"`, "lib."},
		{"dot import", `import . "example.com/copyx/lib"`, ""},
	}
	for _, im := range imports {
		q := im.q
		bodies := []struct{ name, decl, body, want string }{
			{"call result", "", `Println(` + q + `Point(2, 3).Copy(Y = 9))`, q + "Point{X: std.Copy("},
			{"val", "", "val p = " + q + "Point(2, 3)\n    Println(p.Copy(X = 9))", q + "Point{X: std.NewImmutable(9), Y: std.Copy(p.Get().Y)}"},
			{"field", "", "val h = " + q + "Holder(" + q + "Point(1, 2), \"h\")\n    Println(h.P.Copy(X = 9))", q + "Point{X: std.NewImmutable(9)"},
			{"function result", "", `Println(` + q + `Origin().Copy(Y = 4))`, q + "Point{X: std.Copy("},
			{"generic struct call result", "", `Println(` + q + `Pair(1, "a").Copy(First = 2))`, q + "Pair[int, string]{First: std.NewImmutable(2)"},
			{"generic struct val", "", "val pr = " + q + "Pair(1, \"a\")\n    Println(pr.Copy(Second = \"b\"))", q + "Pair[int, string]{First: std.Copy(pr.Get().First)"},
			{"all fields overridden", "", `Println(` + q + `Point(2, 3).Copy(X = 1, Y = 2))`, q + "Point{X: std.NewImmutable(1)"},
			// Types spelled with the import qualifier in this file's own
			// declarations: a result type and a field type.
			{"local function result", "func mk() " + q + "Point = " + q + "Point(1, 2)\n\n", "Println(mk().Copy(X = 6))", q + "Point{X: std.NewImmutable(6)"},
			{"local struct field", "struct W(P " + q + "Point)\n\n", "val w = W(" + q + "Point(1, 2))\n    Println(w.P.Copy(X = 7))", q + "Point{X: std.NewImmutable(7)"},
		}
		for _, b := range bodies {
			tests = append(tests, testCase{
				name: im.name + "/" + b.name,
				src:  "package main\n\n" + im.imp + "\n\n" + b.decl + "func main() {\n    " + b.body + "\n}\n",
				want: b.want,
			})
		}
	}
	tests = append(tests, testCase{
		name: "one package imported plainly and under an alias",
		src: `package main

import l "example.com/copyx/lib"
import "example.com/copyx/lib"

func main() {
    val a = l.Point(2, 3).Copy(Y = 2)
    val p = lib.Point(2, 3)
    Println(a, p.Copy(X = 9), lib.Point(1, 1).Copy(Y = 4))
}`,
		want: "Point{X: std.NewImmutable(9), Y: std.Copy(p.Get().Y)}",
	})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want, "generated:\n%s", out)
		})
	}
}

// TestCopyOverridesCrossPackageHiddenFields: another package's struct can
// keep fields this package cannot name — a private field, or a sealed type's
// variant tag. A literal cannot read those, so the receiver is copied with its
// own Copy() and the overrides are assigned to the copy. Overriding a hidden
// field is rejected.
func TestCopyOverridesCrossPackageHiddenFields(t *testing.T) {
	root := copyCrossPkgFixture(t)
	tests := []struct {
		name, src string
		contains  []string
	}{
		{
			name: "sealed variant through an aliased import",
			src: `package main

import l "example.com/copyx/lib"

func main() {
    val s = l.Circle(1)
    Println(s.Copy(R = 2))
}`,
			contains: []string{" l.Shape) l.Shape {", ".R = std.NewImmutable(2)", "(s.Get().Copy())"},
		},
		{
			name: "sealed variant through a qualified import",
			src: `package main

import "example.com/copyx/lib"

func main() {
    Println(lib.Square(3).Copy(S = 4))
}`,
			contains: []string{" lib.Shape) lib.Shape {", ".S = std.NewImmutable(4)", ".Copy())"},
		},
		{
			name: "sealed variant through a dot import",
			src: `package main

import . "example.com/copyx/lib"

func main() {
    val s = Circle(1)
    Println(s.Copy(R = 2))
}`,
			contains: []string{" Shape) Shape {", ".R = std.NewImmutable(2)", "(s.Get().Copy())"},
		},
		{
			name: "struct with a private field",
			src: `package main

import a "example.com/copyx/lib"

func main() {
    val acc = a.NewAccount("x", 5)
    Println(acc.Copy(Name = "y").Name)
}`,
			contains: []string{" a.Account) a.Account {", `.Name = std.NewImmutable("y")`, "(acc.Get().Copy())"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			assert.NotContains(t, out, "_variant", "generated:\n%s", out)
			assert.NotContains(t, out, "balance", "generated:\n%s", out)
		})
	}

	t.Run("overriding a private field is rejected", func(t *testing.T) {
		_, err := transpileCrossPkg(t, root, `package main

import "example.com/copyx/lib"

func main() {
    Println(lib.NewAccount("x", 5).Copy(balance = 0))
}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "field balance of lib.Account is unexported, so only package lib can set it")
	})
}
