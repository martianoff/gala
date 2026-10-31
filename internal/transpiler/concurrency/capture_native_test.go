package concurrency

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// stagedSources is the .gala files staged as the test's data, sorted.
func stagedSources(t *testing.T) []string {
	t.Helper()
	// Only galop's parses come with a native tree, whatever the build's or
	// environment's default engine.
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	var paths []string
	for _, f := range files {
		if info, err := os.Stat(f.Path); err == nil && info.Mode().IsRegular() && strings.HasSuffix(f.ShortPath, ".gala") {
			paths = append(paths, f.Path)
		}
	}
	sort.Strings(paths)
	require.Greater(t, len(paths), 300, "the corpus is not staged")
	return paths
}

// TestCapturesAgree runs the capture analysis on both trees of every clean
// file of the corpus: FreeVariablesInLambda against FreeVariablesInLambdaNative
// for every lambda, and FreeVariablesInExpression against
// FreeVariablesInExpressionNative for the expression of every argument, the shape
// of a thunk argument, with no parameters and with one. Each case calls the
// native twin on a node of the file's native tree, whose bridge gives the
// ANTLR context the other reads.
func TestCapturesAgree(t *testing.T) {
	paths := stagedSources(t)

	p := parser.NewAntlrGalaParser()
	clean, lambdas, exprs, captured := 0, 0, 0, 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		handle := p.ParseFile(string(data)).CleanNative()
		if handle == nil {
			continue
		}
		require.True(t, handle.Bridged(), path)
		tree, err := handle.Tree()
		require.NoError(t, err)
		clean++
		tree.Root().Walk(func(n native.GalaNodeRef) bool {
			switch n.Kind() {
			case native.GalaKIND_lambdaExpression:
				ctx, ok := handle.Ctx(n).(grammar.ILambdaExpressionContext)
				require.True(t, ok, "%s: node %d", path, n.Index())
				want := FreeVariablesInLambda(ctx)
				require.Equal(t, want, FreeVariablesInLambdaNative(native.LambdaExpressionNode{GalaNodeRef: n}), "%s: lambda at %d:%d", path, n.Start().Line, n.Start().Column)
				lambdas++
				captured += len(want)
			case native.GalaKIND_expression:
				// An argument's expression sits in its pattern.
				if pattern := n.Parent(); !pattern.Ok() || !pattern.Parent().Ok() || pattern.Parent().Kind() != native.GalaKIND_argument {
					break
				}
				ctx, ok := handle.Ctx(n).(grammar.IExpressionContext)
				require.True(t, ok, "%s: node %d", path, n.Index())
				for _, params := range [][]string{nil, {"step"}} {
					want := FreeVariablesInExpression(params, ctx)
					require.Equal(t, want, FreeVariablesInExpressionNative(params, native.ExpressionNode{GalaNodeRef: n}), "%s: expression at %d:%d, params %v", path, n.Start().Line, n.Start().Column, params)
					captured += len(want)
				}
				exprs++
			}
			return true
		})
	}
	t.Logf("%d clean files, %d lambdas, %d argument expressions, %d captures", clean, lambdas, exprs, captured)
	assert.Greater(t, lambdas, 1000)
	assert.Greater(t, exprs, 1000)
}
