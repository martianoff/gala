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

// interfaceCoreType is the one underlying type of a constraint's type set,
// which a type parameter it constrains ranges as (#1378), or nil when there
// is none.
func TestInterfaceCoreType(t *testing.T) {
	const src = `package p

type MyInt int64

type Tilde interface{ ~int64 }
type Union interface{ int64 | MyInt }
type Embeds interface{ Tilde }
type Mixed interface{ ~int | ~int64 }
type Methods interface{ String() string }
type Slice interface{ ~[]string }
type UnionOfIface interface{ Tilde | MyInt }
type Narrowed interface {
	~int | ~int64
	Tilde
}
type NarrowedSlice interface {
	~[]int
	interface{ ~[]int | ~[]string }
}
type WithAny interface{ ~int64 | any }
type WithStringer interface {
	~int64
	String() string
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	pkg, err := (&types.Config{}).Check("example.com/p", fset, []*ast.File{file}, nil)
	require.NoError(t, err)

	cases := []struct {
		name, want string // want "" is no core type
	}{
		{"Tilde", "int64"},
		{"Union", "int64"},
		{"Embeds", "int64"},
		{"Mixed", ""},
		{"Methods", ""},
		{"Slice", "[]string"},
		{"UnionOfIface", "int64"},
		// Embedded elements intersect: {int, int64} ∩ {int64}.
		{"Narrowed", "int64"},
		{"NarrowedSlice", "[]int"},
		// `any` in a union admits every type: no core type.
		{"WithAny", ""},
		{"WithStringer", "int64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iface := pkg.Scope().Lookup(c.name).Type().Underlying().(*types.Interface)
			got := ""
			if core := interfaceCoreType(iface); core != nil {
				got = core.String()
			}
			assert.Equal(t, c.want, got)
		})
	}
}

// interfaceHasNoCoreType is true only where a type set provably has no core
// type, which GALA-E0142 rejects: never for a set with an unresolved type or
// a channel, which Go may still range over.
func TestInterfaceHasNoCoreType(t *testing.T) {
	const src = `package p

import "example.test/missing"

type Any interface{}
type Methods interface{ String() string }
type Mixed interface{ ~int | ~int64 }
type Tilde interface{ ~int64 }
type Chans interface{ chan int | <-chan int }
type Unresolved interface{ ~int | missing.ID }
type NestedUnresolved interface{ ~[]missing.ID | ~[]int }
type Elems[E any] interface{ ~[]E | ~[]int }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("example.com/p", fset, []*ast.File{file}, nil)
	require.NotNil(t, pkg)
	for name, want := range map[string]bool{
		"Any": true, "Methods": true, "Mixed": true,
		"Tilde": false, "Chans": false, "Unresolved": false,
		"NestedUnresolved": false,
		// Elems[int] is {[]int}: a type parameter can make the types one.
		"Elems": false,
	} {
		iface := pkg.Scope().Lookup(name).Type().Underlying().(*types.Interface)
		assert.Equal(t, want, interfaceHasNoCoreType(iface), name)
	}
}

// An embedding cycle is a type error, but the analyzer type-checks past
// errors: interfaceCoreType must end on it, with no core type. (go/types
// currently breaks the cycle itself, giving an invalid underlying type for
// the outer interface; each interface that is still one is checked.)
func TestInterfaceCoreTypeCycle(t *testing.T) {
	const src = `package p

type A interface{ B }
type B interface{ A }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("example.com/p", fset, []*ast.File{file}, nil)
	require.NotNil(t, pkg)
	for _, name := range []string{"A", "B"} {
		if iface, ok := pkg.Scope().Lookup(name).Type().Underlying().(*types.Interface); ok {
			assert.Nil(t, interfaceCoreType(iface), name)
		}
	}
}
