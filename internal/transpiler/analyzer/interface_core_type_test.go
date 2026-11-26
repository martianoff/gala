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
		// Embedded interfaces intersect to int64 in Go; their terms are
		// pooled here, so the core type is missed (known limit), not wrong.
		{"Narrowed", ""},
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
