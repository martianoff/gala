package transformer

import (
	"bytes"
	"go/printer"
	"go/token"
	"testing"

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
