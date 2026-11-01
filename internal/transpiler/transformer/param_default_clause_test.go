package transformer

import (
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/galop"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// TestParamDefaultClauseWithoutValue: a parameter's default clause can be
// there without its value, as an error-recovered `x int = ` may leave it and
// galop's converter carries it to the native tree. Only a clean parse is
// transformed, and on its tree a default clause always has its value, so
// reading such a clause is a missing required node: an internal error, never
// a parameter lowered as if it had no default. The tree is ANTLR's, cut and
// converted.
func TestParamDefaultClauseWithoutValue(t *testing.T) {
	const src = "(x int = 1) int"
	lexer := grammar.NewgalaLexer(antlr.NewInputStream(src))
	lexer.RemoveErrorListeners()
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	p := grammar.NewgalaParser(stream)
	p.RemoveErrorListeners()
	sig := p.Signature()
	list := sig.Parameters().(*grammar.ParametersContext).ParameterList().(*grammar.ParameterListContext)
	clause := list.AllParameter()[0].(*grammar.ParameterContext).ParamDefault().(*grammar.ParamDefaultContext)
	// Drop the clause's value, keeping its `=`.
	clause.RemoveLastChild()
	require.Nil(t, clause.Expression(), "the default clause keeps its value")
	tree, _ := galop.ToNative(sig.(antlr.ParserRuleContext), stream, src)
	require.NotNil(t, tree, "the converter gave no tree")
	n := native.SignatureNode{GalaNodeRef: tree.Root()}
	require.Equal(t, native.GalaKIND_signature, n.Kind())

	require.PanicsWithValue(t, "native tree: no node, not an expression", func() { readParamsNative(n.Parameters()) },
		"readParamsNative: a default clause without its value read as a parameter")
}
