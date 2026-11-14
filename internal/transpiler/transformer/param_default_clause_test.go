package transformer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// TestParamDefaultClauseWithoutValue: a parameter's default clause can be
// there without its value, as an error-recovered `x int = ` may leave it.
// Only a clean parse is transformed, and on its tree a default clause always
// has its value, so reading such a clause is a missing required node:
// readParamsNative, and the nested-function hint's respelling of the
// signature (lambdaSignatureOf), fail rather than read the parameter as one
// without a default, and the transform reports the failure as an internal
// error (GALA-E0017), not a parameter lowered as if it had no default. The
// tree is a clean parse's, its clause cut.
func TestParamDefaultClauseWithoutValue(t *testing.T) {
	const src = "package main\n\nfunc f(x int = 1) int = x\n"
	parsed := transpiler.NewAntlrGalaParser().ParseFile(src)
	require.NoError(t, parsed.Err())
	ntree, err := parsed.Native.Tree()
	require.NoError(t, err)
	var clause native.ParamDefaultNode
	for i := range ntree.Nodes {
		if n := ntree.Node(i); n.Kind() == native.GalaKIND_paramDefault {
			clause = native.ParamDefaultNode{GalaNodeRef: n}
			break
		}
	}
	require.True(t, clause.Ok(), "no default clause")
	// Drop the clause's value, its last child, keeping its `=`.
	ntree.Nodes[clause.Index()].NKids--
	require.False(t, clause.Expression().Ok(), "the default clause keeps its value")
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
