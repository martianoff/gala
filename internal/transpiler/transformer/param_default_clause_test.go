package transformer

import (
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/grammar"
)

// TestParamDefaultClauseWithoutValue: a parameter's default clause on ANTLR's
// tree can be there without its value, as an error-recovered `x int = ` may
// leave it. The parameter still has a default, as the clause says: readParams
// reports one, and so does the signature as the nested-function hint respells
// it (lambdaSignatureOf), which then says a lambda cannot take one.
func TestParamDefaultClauseWithoutValue(t *testing.T) {
	lexer := grammar.NewgalaLexer(antlr.NewInputStream("(x int = 1) int"))
	lexer.RemoveErrorListeners()
	p := grammar.NewgalaParser(antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel))
	p.RemoveErrorListeners()
	sig := p.Signature()
	list := sig.Parameters().(*grammar.ParametersContext).ParameterList().(*grammar.ParameterListContext)
	clause := list.AllParameter()[0].(*grammar.ParameterContext).ParamDefault().(*grammar.ParamDefaultContext)
	// Drop the clause's value, keeping its `=`.
	clause.RemoveLastChild()
	require.Nil(t, clause.Expression(), "the default clause keeps its value")

	params := readParams(sig.Parameters().(*grammar.ParametersContext))
	require.Len(t, params, 1)
	assert.False(t, params[0].def.ok(), "readParams: a value for a clause without one")
	assert.True(t, params[0].hasDefault, "readParams: no default")
	assert.True(t, lambdaSignatureOf(antlrSig(sig)).hasDefault, "lambdaSignatureOf: no default")
}
