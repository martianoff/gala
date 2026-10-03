package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

const variantTypeShapeDecl = `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

`

// TestSealedVariantAsTypeIsRejected pins GALA-E0061: a sealed variant named
// where a type is expected compiled to the variant's empty companion struct,
// so `c.R` and passing Circle(2.0) failed only in `go build`, and a typed
// pattern silently never matched. Each source marks with @ where the error
// must point.
func TestSealedVariantAsTypeIsRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		msg  string
	}{
		{"parameter", variantTypeShapeDecl + "func radius(c @Circle) float64 = c.R\n\nfunc main() {\n    Println(radius(Circle(2.0)))\n}\n",
			"Circle is a variant of sealed type Shape, not a type"},
		{"result", variantTypeShapeDecl + "func unit() @Circle = Circle(1.0)\n\nfunc main() {\n    Println(unit())\n}\n",
			"Circle is a variant of sealed type Shape, not a type"},
		{"struct field", variantTypeShapeDecl + "struct Wheel(Rim @Circle)\n\nfunc main() {\n    Println(Wheel(Circle(1.0)))\n}\n",
			"Circle is a variant"},
		{"block struct field", variantTypeShapeDecl + "type Wheel struct {\n    Rim @Circle\n}\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"sealed case field", variantTypeShapeDecl + "sealed type Scene {\n    case One(S @Square)\n}\n\nfunc main() {\n    Println(1)\n}\n",
			"Square is a variant of sealed type Shape, not a type"},
		{"val annotation", variantTypeShapeDecl + "func main() {\n    val c @Circle = Circle(1.0)\n    Println(c)\n}\n",
			"Circle is a variant"},
		{"var annotation", variantTypeShapeDecl + "func main() {\n    var c @Circle = Circle(1.0)\n    Println(c)\n}\n",
			"Circle is a variant"},
		{"package val annotation", variantTypeShapeDecl + "val unit @Circle = Circle(1.0)\n\nfunc main() {\n    Println(unit)\n}\n",
			"Circle is a variant"},
		{"type argument", variantTypeShapeDecl + "func first(o Option[@Circle]) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"nested type argument", variantTypeShapeDecl + "func f(o Option[Option[@Square]]) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Square is a variant"},
		{"lambda parameter", variantTypeShapeDecl + "func main() {\n    val f = (c @Circle) => 1\n    Println(f)\n}\n",
			"Circle is a variant"},
		{"lambda result", variantTypeShapeDecl + "func main() {\n    val f = (x float64) @Circle => Circle(x)\n    Println(f)\n}\n",
			"Circle is a variant"},
		{"function type", variantTypeShapeDecl + "func apply(f func(@Circle) int) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"typed pattern", variantTypeShapeDecl + "func main() {\n    val s Shape = Circle(1.0)\n    val r = s match {\n        case c: @Circle => 1\n        case _ => 0\n    }\n    Println(r)\n}\n",
			"Circle is a variant"},
		{"type alias", variantTypeShapeDecl + "type Round @Circle\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"receiver", variantTypeShapeDecl + "func (c @Circle) Area() float64 = 1.0\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"pointer", variantTypeShapeDecl + "func f(c *@Circle) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant"},
		{"explicit type argument of a function", variantTypeShapeDecl + "func wrap[T any](x T) Option[T] = Some(x)\n\nfunc main() {\n    Println(wrap[@Circle](Circle(1.0)))\n}\n",
			"Circle is a variant of sealed type Shape, not a type"},
		{"explicit type argument of a std constructor", variantTypeShapeDecl + "func main() {\n    Println(Some[@Circle](Circle(1.0)))\n}\n",
			"Circle is a variant"},
		{"explicit type argument nested in another", variantTypeShapeDecl + "func wrap[T any](x T) Option[T] = Some(x)\n\nfunc main() {\n    Println(wrap[Option[@Square]](None[Square]()))\n}\n",
			"Square is a variant"},
		{"explicit type argument of a method", variantTypeShapeDecl + "func main() {\n    val o = Some(1)\n    Println(o.Map[@Circle]((n) => Circle(1.0)))\n}\n",
			"Circle is a variant"},
		{"pointer explicit type argument", variantTypeShapeDecl + "func wrap[T any](x T) Option[T] = Some(x)\n\nfunc main() {\n    Println(wrap[*@Circle](nil))\n}\n",
			"Circle is a variant"},
		{"explicit type argument of a method on a package val", variantTypeShapeDecl + "val origin = Some(1)\n\nfunc main() {\n    Println(origin.Map[@Circle]((n) => Circle(1.0)))\n}\n",
			"Circle is a variant"},
		{"explicit type argument of an extractor pattern", variantTypeShapeDecl + "func main() {\n    val o = Some(Circle(1.0))\n    val r = o match {\n        case Some[@Circle](c) => 1\n        case _ => 0\n    }\n    Println(r)\n}\n",
			"Circle is a variant"},
		{"type in an interpolated expression", variantTypeShapeDecl + "func main() {\n    Println(s\"${((c @Circle) => 1)(Circle(1.0))}\")\n}\n",
			"Circle is a variant"},
		{"outside the declaration whose type parameter shadows it", variantTypeShapeDecl + "func id[Circle any](x Circle) Circle = x\n\nfunc radius(c @Circle) float64 = 1.0\n\nfunc main() {\n    Println(id(1))\n}\n",
			"Circle is a variant"},
		{"declared after its use", "package main\n\nfunc radius(c @Circle) float64 = 1.0\n\nsealed type Shape {\n    case Circle(R float64)\n}\n\nfunc main() {\n    Println(1)\n}\n",
			"Circle is a variant of sealed type Shape, not a type"},
		{"std variant with type arguments", "package main\n\nfunc f(o @Some[int]) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Some[int] is a variant of sealed type Option[int], not a type"},
		{"std zero-field variant", "package main\n\nfunc f(o @None[int]) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"None[int] is a variant of sealed type Option[int], not a type"},
		{"generic local variant", "package main\n\nsealed type Box[T any] {\n    case Full(V T)\n    case Empty()\n}\n\nfunc f(b @Full[string]) int = 1\n\nfunc main() {\n    Println(1)\n}\n",
			"Full[string] is a variant of sealed type Box[string], not a type"},
	}
	trans := newForbiddenBuiltinTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantLine, wantCol := markerPosition(t, tc.src)
			src := strings.Replace(tc.src, "@", "", 1)
			_, err := trans.Transpile(src, "variant_type_test.gala")
			requireVariantTypeError(t, err, wantLine, wantCol, tc.msg)
		})
	}
}

// TestSealedVariantAsTypeHint pins the hint: it names the sealed type to use
// instead and the pattern that reaches the variant's fields.
func TestSealedVariantAsTypeHint(t *testing.T) {
	src := variantTypeShapeDecl + "func radius(c Circle) float64 = c.R\n\nfunc main() {\n    Println(radius(Circle(2.0)))\n}\n"
	_, err := newForbiddenBuiltinTranspiler().Transpile(src, "variant_type_test.gala")
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a semantic error, got %T: %v", err, err)
	require.Equal(t, "use the sealed type Shape here; Circle(...) builds one, "+
		"and `case Circle(...)` in a match reaches the variant's fields", se.Hint)
}

// TestSealedVariantOfImportedPackageAsType covers a variant reached through
// an import qualifier, which needs a real package on disk.
func TestSealedVariantOfImportedPackageAsType(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "shapes")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shapes.gala"), []byte(`package shapes

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}
`), 0o600))

	for _, tc := range []struct {
		name string
		src  string
		msg  string
	}{
		{"qualified", "package main\n\nimport \"martianoff/gala/shapes\"\n\nfunc radius(c @shapes.Circle) float64 = 1.0\n\nfunc main() {\n    Println(radius(shapes.Circle(1.0)))\n}\n",
			"shapes.Circle is a variant of sealed type shapes.Shape, not a type"},
		{"aliased", "package main\n\nimport sh \"martianoff/gala/shapes\"\n\nfunc radius(c @sh.Circle) float64 = 1.0\n\nfunc main() {\n    Println(radius(sh.Circle(1.0)))\n}\n",
			"sh.Circle is a variant of sealed type sh.Shape, not a type"},
		{"dot-imported", "package main\n\nimport . \"martianoff/gala/shapes\"\n\nfunc radius(c @Circle) float64 = 1.0\n\nfunc main() {\n    Println(radius(Circle(1.0)))\n}\n",
			"Circle is a variant of sealed type Shape, not a type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantLine, wantCol := markerPosition(t, tc.src)
			src := strings.Replace(tc.src, "@", "", 1)
			mainDir := filepath.Join(root, "main_"+strings.ReplaceAll(tc.name, "-", "_"))
			require.NoError(t, os.MkdirAll(mainDir, 0o755))
			mainPath := filepath.Join(mainDir, "main.gala")
			require.NoError(t, os.WriteFile(mainPath, []byte(src), 0o600))
			_, err := newDocGuardTranspilerWithPaths(root).Transpile(src, mainPath)
			requireVariantTypeError(t, err, wantLine, wantCol, tc.msg)
		})
	}
}

// TestSealedVariantAsTypeLegalNeighbours covers what GALA-E0061 must leave
// alone: the sealed type itself, variants as constructors and patterns, and a
// local type or type parameter that shadows a dot-imported variant's name.
func TestSealedVariantAsTypeLegalNeighbours(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"sealed type and variant patterns", variantTypeShapeDecl + `func radius(s Shape) float64 = s match {
    case Circle(r) => r
    case Square(_) => 0.0
}

func main() {
    val c Shape = Circle(2.0)
    val o Option[Shape] = Some(Square(1.0))
    Println(radius(c), o.IsDefined())
}
`},
		{"type parameter shadows a variant", variantTypeShapeDecl + `func id[Circle any](x Circle) Circle = x

func main() {
    Println(id(1))
}
`},
		{"sealed type as an explicit type argument", `package main

import . "martianoff/gala/collection_immutable"

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val xs = ArrayOf[Shape](Circle(1.0), Square(2.0))
    Println(xs.Get(0))
}
`},
		{"indexing by a value named like a variant", `package main

import "os"

func pick(Left int) string = os.Args[Left]

func main() {
    Println(pick(0) != "")
}
`},
		{"a field named like a variant", variantTypeShapeDecl + `struct Holder(Circle Shape)

func main() {
    Println(Holder(Circle(1.0)).Circle)
}
`},
	}
	trans := newForbiddenBuiltinTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.src, "variant_type_test.gala")
			require.NoError(t, err)
		})
	}
}

func requireVariantTypeError(t *testing.T, err error, wantLine, wantCol int, msg string) {
	t.Helper()
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a semantic error, got %T: %v", err, err)
	require.Equal(t, galaerr.CodeSealedVariantAsType, se.Code, "got: %v", err)
	require.Contains(t, se.Msg, msg)
	require.Equal(t, wantLine, se.Line, "line; got: %v", err)
	require.Equal(t, wantCol, se.Column, "column; got: %v", err)
}
