package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTupleLiteralInterfaceElement covers a tuple literal whose expected type
// has an interface element. Tuple[Square, int] is not a Tuple[Shape, int], so
// the literal must take the slot's element type, and its element wrapper must
// name it, wherever the expected type comes from.
func TestTupleLiteralInterfaceElement(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
type Shape interface {
    Area() float64
}

type Marker interface {}

struct Square(Side float64)

func (s Square) Area() float64 = s.Side * s.Side

struct NotFound(Key string)

func (e NotFound) Error() string = e.Key

struct Placed(At Tuple[Shape, int])

func describe(t Tuple[Shape, string]) string = t.V2

func failed(key string) Tuple[int, error] = (404, NotFound(key))

struct Rect(W float64, H float64)

func (r Rect) Area() float64 = r.W * r.H

func pick(b bool) Tuple[Shape, int] = if (b) (Square(1.0), 1) else (Rect(1.0, 2.0), 2)

func choose(n int) Tuple[Shape, int] = n match {
    case 0 => (Square(2.0), 0)
    case _ => (Rect(2.0, 2.0), n)
}
`

	cases := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "val annotation",
			body:     `val t Tuple[Shape, int] = (Square(1.0), 2)`,
			contains: []string{"std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "argument",
			body:     `Println(describe((Square(2.0), "sq")))`,
			contains: []string{"describe(std.Tuple[Shape, string]{V1: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "return value, error element",
			body:     `Println(failed("k").V1)`,
			contains: []string{"std.Tuple[int, error]{V1: std.NewImmutable(404), V2: std.NewImmutable[error](NotFound{"},
		},
		{
			name:     "struct field",
			body:     `Println(Placed((Square(3.0), 7)).At.V2)`,
			contains: []string{"std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "empty interface and any, Tuple3",
			body:     `val m Tuple3[Marker, any, error] = (Square(2.0), Square(1.0), NotFound("x"))`,
			contains: []string{"std.Tuple3[Marker, any, error]{V1: std.NewImmutable[Marker](Square{", "V2: std.NewImmutable[any](Square{", "V3: std.NewImmutable[error](NotFound{"},
		},
		{
			name:     "nested tuple",
			body:     `val n Tuple[Tuple[Shape, int], string] = ((Square(5.0), 1), "n")`,
			contains: []string{"std.Tuple[std.Tuple[Shape, int], string]{V1: std.NewImmutable(std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "inside a generic constructor argument",
			body:     `val o Option[Tuple[Shape, int]] = Some((Square(4.0), 3))`,
			contains: []string{"Apply(std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "if-expression branches",
			body:     `Println(pick(true).V2)`,
			contains: []string{"std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{", "std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Rect{"},
		},
		{
			name:     "match arms",
			body:     `Println(choose(1).V2)`,
			contains: []string{"std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Square{", "std.Tuple[Shape, int]{V1: std.NewImmutable[Shape](Rect{"},
		},
		{
			name:     "Go named function type slot",
			body:     `val w Tuple[fs.WalkDirFunc, int] = ((p string, d fs.DirEntry, err error) => err, 0)`,
			contains: []string{"std.Tuple[fs.WalkDirFunc, int]{V1: std.NewImmutable[fs.WalkDirFunc](func("},
		},
		{
			name:     "untyped constant into a numeric slot",
			body:     `val c Tuple[int64, Shape] = (1, Square(1.0))`,
			contains: []string{"std.Tuple[int64, Shape]{V1: std.NewImmutable[int64](1), V2: std.NewImmutable[Shape](Square{"},
		},
		{
			// A slot of the element's own concrete type keeps the inferred form.
			name:     "concrete slot keeps the inferred form",
			body:     `val q Tuple[Square, int] = (Square(1.0), 2)`,
			contains: []string{"std.Tuple[Square, int]{V1: std.NewImmutable(Square{"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport \"io/fs\"\n" + decls + "\nfunc main() {\n    " + tc.body + "\n}\n"
			out, err := trans.Transpile(src, "tuple_interface_element_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}
}
