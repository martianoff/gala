package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An anonymous Go struct is typed concretely, its fields' types exact: a
// fixed-length array keeps its length, a generic type instance its type
// arguments and an inline interface its methods. One that could not be
// written outside its package (an unexported field, method or type) or with
// a field the type model does not spell (a variadic function) is typed the
// same way, not erased to any: GALA never writes the type (GALA-E0090).
func TestAnonymousStructFieldTypes(t *testing.T) {
	const src = `package p

type Box[T any] struct{ Value T }

type Reader interface{ Read() int }

type cell int

type Alias = Reader

func Grid() struct{ Cells [3]int; Rows [][2]string }
func Boxed() struct{ Inner Box[int]; Boxes []Box[[2]int] }
func Shaped() struct{ Shape interface{ Scale(int); Area() int } }
func Embedded() struct{ R interface{ Reader; Close() error } }
func Named() struct{ F func(x int) string }
func Nested() struct{ A struct{ B [1]Box[string] } }

func Hidden() struct{ x int }
func HiddenMethod() struct{ R interface{ read() } }
func Variadic() struct{ F func(...int) }
func HiddenType() struct{ C [3]cell }
func Aliased() struct{ R Alias; A any }

func AnyField() struct{ A any; E error }
func TopLevel() [3]int
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	pkg, err := (&types.Config{}).Check("example.com/p", fset, []*ast.File{file}, nil)
	require.NoError(t, err)

	result := func(name string) types.Type {
		return pkg.Scope().Lookup(name).Type().(*types.Signature).Results().At(0).Type()
	}
	for _, tt := range []struct {
		fn, want string
	}{
		{"Grid", "struct{Cells [3]int; Rows [][2]string}"},
		{"Boxed", "struct{Inner p.Box[int]; Boxes []p.Box[[2]int]}"},
		{"Shaped", "struct{Shape interface{Area() int; Scale(int)}}"},
		{"Embedded", "struct{R interface{Close() error; Read() int}}"},
		{"Named", "struct{F func(int) string}"},
		{"Nested", "struct{A struct{B [1]p.Box[string]}}"},
		{"Hidden", "struct{x int}"},
		{"HiddenMethod", "struct{R interface{read()}}"},
		{"Variadic", "struct{F func([]int)}"},
		{"HiddenType", "struct{C [3]p.cell}"},
		{"Aliased", "struct{R p.Reader; A any}"},
		// Outside an anonymous struct an array stays the slice it has been.
		{"AnyField", "struct{A any; E error}"},
		{"TopLevel", "[]int"},
	} {
		got := goTypeToTranspilerType(result(tt.fn))
		assert.Equal(t, tt.want, got.String(), tt.fn)
	}
}
