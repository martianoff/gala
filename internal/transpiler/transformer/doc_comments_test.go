package transformer_test

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docCommentsSource documents one declaration of every kind GALA has. Line
// numbers are load-bearing: docCommentDecls names the GALA line each Go
// declaration must map back to through its //line directive.
const docCommentsSource = `// Package docs is documented.
//
// Second paragraph.
package docs

// Add returns the sum of a and b.
// @Summary Adds two numbers
// @Router /add [get]
func Add(a int, b int) int = a + b

/* Config is a block-commented struct. */
struct Config(
	// Name is a documented shorthand field.
	var Name string,
	Count int
)

// Describe is a documented method.
func (c Config) Describe() string = c.Name

// Shape is a documented sealed type.
sealed type Shape {
	// Circle is a documented case.
	case Circle(R float64)
	case Square(S float64)
}

// Greeter is a documented interface.
type Greeter interface {
	// Greet is a documented interface method.
	Greet() string
}

// Point is a documented struct.
type Point struct {
	// X is a documented field.
	X int
	Y int
}

// Millis is a documented type.
type Millis int64

// Limit is a documented val.
val Limit = 10

// Counter is a documented var.
var Counter = 0

func undocumented() int {
	// BODY_COMMENT inside a function body is not carried.
	return 1 // TRAILING_COMMENT is not carried.
}
`

// docCommentDecls maps each documented Go declaration to its doc text (as
// go/doc reports it) and the GALA line its declaration keyword is on.
var docCommentDecls = []struct {
	name     string
	doc      string
	galaLine int
}{
	{"Add", "Add returns the sum of a and b. @Summary Adds two numbers @Router /add [get]", 9},
	{"Config", "Config is a block-commented struct.", 12},
	{"Describe", "Describe is a documented method.", 19},
	{"Shape", "Shape is a documented sealed type.", 22},
	{"Circle", "Circle is a documented case.", 0}, // a companion type, not line-mapped
	{"Greeter", "Greeter is a documented interface.", 29},
	{"Point", "Point is a documented struct.", 35},
	{"Millis", "Millis is a documented type.", 42},
	{"Limit", "Limit is a documented val.", 45},
	{"Counter", "Counter is a documented var.", 48},
}

// TestDocComments_GeneratedGo checks that every documented GALA declaration's
// doc comment reaches the generated Go, where go/doc — what `go doc`, gopls and
// pkg.go.dev read — attaches it to the right declaration, and that the
// //line directive still maps each declaration to its GALA line. It runs with a
// source file (line directives on) and without one (no directives).
func TestDocComments_GeneratedGo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		filePath string
	}{
		{"with line directives", "docs_demo.gala"},
		{"without line directives", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			goCode, err := newLineDirectiveTranspiler().Transpile(docCommentsSource, tc.filePath)
			require.NoError(t, err)

			// Exact text for the swag-style annotation block: every line, in
			// order, directly above the function.
			assert.Contains(t, goCode,
				"// Add returns the sum of a and b.\n// @Summary Adds two numbers\n// @Router /add [get]\nfunc Add(",
				"annotation lines must sit directly above the declaration\n%s", goCode)
			assert.NotContains(t, goCode, "BODY_COMMENT", "body comments are out of scope")
			assert.NotContains(t, goCode, "TRAILING_COMMENT", "trailing comments are out of scope")

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "docs.gen.go", goCode, parser.ParseComments)
			require.NoError(t, err)

			// Line mapping first: go/doc.New trims the AST it is handed.
			if tc.filePath != "" {
				for _, want := range docCommentDecls {
					if want.galaLine == 0 {
						continue
					}
					pos, ok := declPosition(fset, file, want.name)
					require.True(t, ok, "declaration %s not found", want.name)
					assert.Equal(t, tc.filePath, pos.Filename, "%s: //line file", want.name)
					assert.Equal(t, want.galaLine, pos.Line, "%s: //line must map the declaration to its GALA line", want.name)
				}
			}

			assertFieldDoc(t, file, "Config", "Name", "Name is a documented shorthand field.")
			assertFieldDoc(t, file, "Point", "X", "X is a documented field.")
			assertFieldDoc(t, file, "Greeter", "Greet", "Greet is a documented interface method.")
			assertFieldDoc(t, file, "Point", "Y", "")

			pkg, err := doc.NewFromFiles(fset, []*ast.File{file}, "example.com/docs", doc.PreserveAST)
			require.NoError(t, err)
			assert.Equal(t, "Package docs is documented.\n\nSecond paragraph.\n", pkg.Doc)

			got := make(map[string]string)
			for _, f := range pkg.Funcs {
				got[f.Name] = f.Doc
			}
			for _, v := range pkg.Vars {
				got[v.Names[0]] = v.Doc
			}
			for _, typ := range pkg.Types {
				got[typ.Name] = typ.Doc
				for _, m := range typ.Methods {
					got[m.Name] = m.Doc
				}
				for _, f := range typ.Funcs {
					got[f.Name] = f.Doc
				}
			}
			for _, want := range docCommentDecls {
				assert.Equal(t, want.doc, strings.Join(strings.Fields(got[want.name]), " "),
					"go/doc must attach the doc comment of %s", want.name)
			}
			assert.Empty(t, got["Square"], "an undocumented case gets no doc")
			assert.Empty(t, got["Copy"], "generated helpers get no doc")
		})
	}
}

// declPosition returns the //line-adjusted position of the top-level
// declaration of name (a function, method, type or variable).
func declPosition(fset *token.FileSet, file *ast.File, name string) (token.Position, bool) {
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == name {
				return fset.Position(d.Pos()), true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == name {
						return fset.Position(d.Pos()), true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == name {
							return fset.Position(d.Pos()), true
						}
					}
				}
			}
		}
	}
	return token.Position{}, false
}

// assertFieldDoc checks the doc comment of a struct field or interface method;
// want "" asserts it has none.
func assertFieldDoc(t *testing.T, file *ast.File, typeName, fieldName, want string) {
	t.Helper()
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		ts := gd.Specs[0].(*ast.TypeSpec)
		if ts.Name.Name != typeName {
			continue
		}
		var fields *ast.FieldList
		switch typ := ts.Type.(type) {
		case *ast.StructType:
			fields = typ.Fields
		case *ast.InterfaceType:
			fields = typ.Methods
		}
		require.NotNil(t, fields, "%s has no field list", typeName)
		for _, f := range fields.List {
			if len(f.Names) > 0 && f.Names[0].Name == fieldName {
				assert.Equal(t, want, strings.TrimSpace(f.Doc.Text()), "doc of %s.%s", typeName, fieldName)
				return
			}
		}
		t.Fatalf("%s.%s not found", typeName, fieldName)
	}
	t.Fatalf("type %s not found", typeName)
}
