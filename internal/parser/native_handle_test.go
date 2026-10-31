package parser

import (
	"testing"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// TestNilNative: a nil *Native, what every parse without a native tree
// carries, reports that it has none and finds nothing, without panicking.
func TestNilNative(t *testing.T) {
	var n *Native
	if tree, err := n.Tree(); tree != nil || err == nil {
		t.Errorf("Tree() = %v, %v; want no tree and an error", tree, err)
	}
	if ctx := n.Ctx(native.GalaNodeRef{}); ctx != nil {
		t.Errorf("Ctx() = %v, want nil", ctx)
	}
	if node := n.Node(nil); node.Ok() {
		t.Error("Node() found a node")
	}
	if n.WellFormed() {
		t.Error("WellFormed() = true")
	}
}

// TestNewNativeNoTree: no handle for a parse without a source-file tree,
// including an interface holding a nil *SourceFileContext.
func TestNewNativeNoTree(t *testing.T) {
	stream := antlr.NewCommonTokenStream(grammar.NewgalaLexer(antlr.NewInputStream("")), antlr.TokenDefaultChannel)
	var typedNil *grammar.SourceFileContext
	for name, root := range map[string]antlr.Tree{"nil": nil, "typed nil": typedNil} {
		if n := newNative(root, stream, ""); n != nil {
			t.Errorf("%s root: got a handle", name)
		}
	}
}
