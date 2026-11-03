package transformer

import (
	"errors"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// TestParamDefaultClauseWithoutValue: a parameter's default clause can be
// there without its value, as an error-recovered `x int = ` may leave it and
// galop's converter carries it to the native tree. Only a clean parse is
// transformed, and on its tree a default clause always has its value, so
// reading such a clause is a missing required node: readParamsNative, and the
// nested-function hint's respelling of the signature (lambdaSignatureOf),
// fail rather than read the parameter as one without a default, and the
// transform reports the failure as an internal error (GALA-E0017), not a
// parameter lowered as if it had no default. The tree is a clean parse's,
// cut before its native tree converts.
func TestParamDefaultClauseWithoutValue(t *testing.T) {
	const src = "package main\n\nfunc f(x int = 1) int = x\n"
	parsed := transpiler.NewAntlrGalaParser().ParseFile(src)
	tree, _, err := parsed.Strict()
	require.NoError(t, err)
	clause := firstParamDefault(tree)
	require.NotNil(t, clause, "no default clause")
	// Drop the clause's value, keeping its `=`.
	clause.RemoveLastChild()
	require.Nil(t, clause.Expression(), "the default clause keeps its value")
	require.False(t, parsed.Native.Converted(), "the native tree converted before the cut")
	ntree, err := parsed.Native.Tree()
	require.NoError(t, err)
	var sig native.SignatureNode
	for i := range ntree.Nodes {
		if n := ntree.Node(i); n.Kind() == native.GalaKIND_signature {
			sig = native.SignatureNode{GalaNodeRef: n}
			break
		}
	}
	require.True(t, sig.Ok(), "no signature in the native tree")

	assert.Panics(t, func() { readParamsNative(sig.Parameters()) },
		"readParamsNative: a default clause without its value read as a parameter")
	assert.Panics(t, func() { lambdaSignatureOf(nativeSig(sig)) },
		"lambdaSignatureOf: a default clause without its value respelled")

	_, _, err = NewGalaASTTransformer().Transform(&transpiler.RichAST{
		Native:           parsed.Native,
		PackageName:      "main",
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		ImportAliases:    make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
		GoExports:        make(map[string][]string),
		TypeAliases:      make(map[string]transpiler.Type),
		PackageVals:      make(map[string]*transpiler.PackageValMetadata),
		FilePath:         "param_default.gala",
		SourceContent:    src,
	})
	var semErr *galaerr.SemanticError
	require.True(t, errors.As(err, &semErr), "the transform gave %v, not a semantic error", err)
	assert.Equal(t, galaerr.CodeInternalTransformerPanic, semErr.Code)
}

// firstParamDefault is the first parameter default clause of tree, nil when
// it has none.
func firstParamDefault(tree antlr.Tree) *grammar.ParamDefaultContext {
	if clause, ok := tree.(*grammar.ParamDefaultContext); ok {
		return clause
	}
	for _, kid := range tree.GetChildren() {
		if clause := firstParamDefault(kid); clause != nil {
			return clause
		}
	}
	return nil
}
