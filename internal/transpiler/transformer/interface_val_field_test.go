package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInterfaceValFieldSpellsSlotType covers a value of a concrete type going
// into an immutable field of an interface type. NewImmutable infers its type
// argument from the value, and Immutable[Square] is not an Immutable[Shape],
// so the wrapper must spell the field's interface type wherever a struct is
// built: positionally, by name, through Copy, and from a field default.
func TestInterfaceValFieldSpellsSlotType(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
type Shape interface {
    Area() float64
}

struct Square(Side float64)

func (s Square) Area() float64 = s.Side * s.Side

struct NotFound(Key string)

func (e NotFound) Error() string = e.Key

struct Holder(S Shape)
struct Boxed(V any)

type Marker interface {}

struct Tagged(M Marker)
struct Report(Code int, Cause error)
struct Framed(Label string, S Shape = Square(1.0))
`

	cases := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "declared interface, positional",
			body:     `Println(Holder(Square(2.0)).S.Area())`,
			contains: []string{"S: std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "any, positional",
			body:     `Println(Boxed(Square(2.0)).V)`,
			contains: []string{"V: std.NewImmutable[any](Square{"},
		},
		{
			name:     "declared interface with no methods",
			body:     `Println(Tagged(Square(2.0)).M)`,
			contains: []string{"M: std.NewImmutable[Marker](Square{"},
		},
		{
			name: "error, named, from a variable",
			body: `val nf = NotFound(Key = "k")
    Println(Report(Code = 1, Cause = nf).Cause)`,
			contains: []string{"Cause: std.NewImmutable[error](nf.Get())"},
		},
		{
			name: "Copy override",
			body: `val h = Holder(Square(2.0))
    Println(h.Copy(S = Square(3.0)).S.Area())`,
			contains: []string{"std.NewImmutable[Shape](Square{"},
		},
		{
			name:     "field default",
			body:     `Println(Framed("f").S.Area())`,
			contains: []string{"S: std.NewImmutable[Shape](Square{"},
		},
		{
			// A concrete field keeps the inferred form.
			name:     "concrete slot keeps the inferred form",
			body:     `Println(Square(2.0).Side)`,
			contains: []string{"Side: std.NewImmutable(2.0)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\nfunc main() {\n    " + tc.body + "\n}\n"
			out, err := trans.Transpile(src, "interface_val_field_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}
}
