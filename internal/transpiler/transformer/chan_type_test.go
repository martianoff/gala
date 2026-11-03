package transformer

import (
	"bytes"
	"go/printer"
	"go/token"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"

	"github.com/stretchr/testify/assert"
)

// A generic parameter typed as a channel binds its type parameter from a
// channel argument of either direction: a `chan T` fills a `<-chan T`.
func TestUnifyChanTypes(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	pattern := transpiler.ChanType{Elem: transpiler.BasicType{Name: "T"}, Dir: transpiler.ChanRecv}
	concrete := transpiler.ChanType{Elem: transpiler.BasicType{Name: "int"}}

	inferred := map[string]transpiler.Type{}
	assert.True(t, tr.unifyForInference(pattern, concrete, []string{"T"}, inferred))
	assert.Equal(t, transpiler.BasicType{Name: "int"}, inferred["T"])
}

// Go reads `chan <-chan T` as `chan<- (chan T)`, so a bidirectional channel of
// a receive-only channel keeps its parentheses in emitted Go.
func TestNestedChanTypeExpr(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	inner := transpiler.ChanType{Elem: transpiler.BasicType{Name: "int"}, Dir: transpiler.ChanRecv}

	for _, tt := range []struct {
		typ  transpiler.ChanType
		want string
	}{
		{transpiler.ChanType{Elem: inner}, "chan (<-chan int)"},
		{transpiler.ChanType{Elem: inner, Dir: transpiler.ChanRecv}, "<-chan <-chan int"},
		{transpiler.ChanType{Elem: inner, Dir: transpiler.ChanSend}, "chan<- <-chan int"},
	} {
		var buf bytes.Buffer
		assert.NoError(t, printer.Fprint(&buf, token.NewFileSet(), tr.typeToExpr(tt.typ)))
		assert.Equal(t, tt.want, buf.String())
		assert.Equal(t, tt.want, tt.typ.String())
	}
}

// An anonymous Go struct type is never written in the generated Go: a type
// that is or holds one is GALA-E0090, however deep the struct sits.
func TestStructTypeExprRejected(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	empty := transpiler.StructType{}
	for _, typ := range []transpiler.Type{
		empty,
		transpiler.ChanType{Elem: empty, Dir: transpiler.ChanRecv},
		transpiler.GenericType{Base: transpiler.NamedType{Package: "std", Name: "Option"}, Params: []transpiler.Type{empty}},
		transpiler.FuncType{Results: []transpiler.Type{transpiler.PointerType{Elem: empty}}},
		transpiler.MapType{Key: transpiler.BasicType{Name: "string"}, Elem: transpiler.ArrayType{Elem: empty}},
		transpiler.StructType{Fields: []transpiler.StructField{
			{Name: "X", Type: transpiler.BasicType{Name: "int"}, Tag: `json:"x"`},
		}},
	} {
		func() {
			defer func() {
				err, ok := recover().(*galaerr.SemanticError)
				if assert.True(t, ok, "%s: expected a semantic error", typ) {
					assert.Equal(t, galaerr.CodeAnonymousStructType, err.Code, typ.String())
				}
			}()
			tr.typeToExpr(typ)
		}()
	}
}

// An inline interface, and a fixed-length array (as an anonymous struct's
// field reads), are emitted as Go spells them.
func TestFieldTypeExpr(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	for _, tt := range []struct {
		typ  transpiler.Type
		want string
	}{
		{transpiler.ArrayType{Elem: transpiler.ArrayType{Elem: transpiler.BasicType{Name: "string"}, Len: 2, Fixed: true}}, "[][2]string"},
		{transpiler.InterfaceType{Methods: []transpiler.InterfaceMethod{
			{Name: "Area", Type: transpiler.FuncType{Results: []transpiler.Type{transpiler.BasicType{Name: "int"}}}},
			{Name: "Scale", Type: transpiler.FuncType{Params: []transpiler.Type{transpiler.BasicType{Name: "int"}}}},
		}}, "interface {\n\tArea() int\n\tScale(int)\n}"},
	} {
		var buf bytes.Buffer
		expr := tr.typeToExpr(tt.typ)
		assert.NoError(t, printer.Fprint(&buf, token.NewFileSet(), expr))
		assert.Equal(t, tt.want, buf.String())
		assert.Equal(t, tt.typ.String(), tr.astTypeToTranspilerType(expr).String(), "the emitted type reads back as itself")
	}
}
