package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoCodeGenerator_Generate(t *testing.T) {
	g := NewGoCodeGenerator()

	tests := []struct {
		name     string
		source   string
		expected string
		wantErr  bool
	}{
		{
			name: "Simple package and function",
			source: `package main
func main() {
	println("hello")
}
`,
			expected: generatedHeader + `package main

func main() {
	println("hello")
}
`,
			wantErr: false,
		},
		{
			name: "Struct and method",
			source: `package test
type Point struct {
	X, Y int
}
func (p Point) Sum() int { return p.X + p.Y }
`,
			expected: generatedHeader + `package test

type Point struct {
	X, Y int
}

func (p Point) Sum() int { return p.X + p.Y }
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "test.go", tt.source, parser.ParseComments)
			assert.NoError(t, err)

			got, err := g.Generate(fset, file)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestGoCodeGenerator_SyntheticDocComments covers doc comments on a
// position-less AST, the shape the transformer builds. go/printer cannot place
// a package doc or a field doc without positions, so Generate splices those
// itself; each doc must come out directly above its declaration.
func TestGoCodeGenerator_SyntheticDocComments(t *testing.T) {
	doc := func(lines ...string) *ast.CommentGroup {
		g := &ast.CommentGroup{}
		for _, l := range lines {
			g.List = append(g.List, &ast.Comment{Text: l})
		}
		return g
	}
	field := func(d *ast.CommentGroup, name string) *ast.Field {
		return &ast.Field{Doc: d, Names: []*ast.Ident{ast.NewIdent(name)}, Type: ast.NewIdent("int")}
	}
	file := &ast.File{
		Doc:  doc("// Package p is documented.", "//", "// Second paragraph."),
		Name: ast.NewIdent("p"),
		Decls: []ast.Decl{
			&ast.FuncDecl{
				Doc:  doc("// F does f."),
				Name: ast.NewIdent("F"),
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{},
			},
			&ast.GenDecl{Tok: token.TYPE, Doc: doc("// T is t."), Specs: []ast.Spec{&ast.TypeSpec{
				Name: ast.NewIdent("T"),
				Type: &ast.StructType{Fields: &ast.FieldList{List: []*ast.Field{
					field(doc("// A is a."), "A"),
					field(nil, "B"),
					field(doc("// C is c.", "// More on c."), "C"),
				}}},
			}}},
			&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
				Name: ast.NewIdent("I"),
				Type: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{
					Doc:   doc("// M is m."),
					Names: []*ast.Ident{ast.NewIdent("M")},
					Type:  &ast.FuncType{Params: &ast.FieldList{}},
				}}}},
			}}},
		},
	}

	got, err := NewGoCodeGenerator().Generate(token.NewFileSet(), file)
	require.NoError(t, err)
	assert.Equal(t, generatedHeader+`// Package p is documented.
//
// Second paragraph.
package p

// F does f.
func F() {
}

// T is t.
type T struct {
	// A is a.
	A int
	B int
	// C is c.
	// More on c.
	C int
}
type I interface {
	// M is m.
	M()
}
`, got)

	// The caller's AST keeps its docs.
	assert.NotNil(t, file.Doc)
	assert.NotNil(t, file.Decls[1].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type.(*ast.StructType).Fields.List[0].Doc)
}
