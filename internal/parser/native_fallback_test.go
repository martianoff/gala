package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// fallbackSrc has a node of every kind the transformer's top-level walk
// crosses back to ANTLR's tree from.
const fallbackSrc = `package main

struct P(x int, y string)

sealed type S {
    case A(v int)
    case B()
}

opaque type O int

type Q struct {
    z int
}

val a int = 1

val b, c = 2, 3

func f(n int) int {
    return n + a
}

func (q Q) g() int = q.z + b + c

func main() = Println(P(1, "y"), A(1), f(2))
`

// misbridgingParser parses as its parser does, and, when kind is set,
// misbridges the first parse's native tree (the compiled file's) at its
// first node of kind before anything reads it.
type misbridgingParser struct {
	transpiler.GalaParser
	kind     native.GalaKind
	compiled *parser.Native
	found    bool
}

func (p *misbridgingParser) ParseFile(input string) *parser.Parsed {
	parsed := p.GalaParser.ParseFile(input)
	if p.compiled == nil {
		p.compiled = parsed.Native
		if p.kind != 0 {
			p.found = parser.Misbridge(parsed.Native, p.kind)
		}
	}
	return parsed
}

// TestNativeBridgeFallback: a file whose native tree's bridge fails on any
// kind of node the transformer's walk crosses from is compiled on ANTLR's
// tree, analysis and transform alike, to the same Go as with a sound bridge,
// not stopped by the walk's crossing (an internal error, GALA-E0017).
func TestNativeBridgeFallback(t *testing.T) {
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
	stdFile, err := bazel.Runfile("std/option.gala")
	require.NoError(t, err, "std sources not staged")
	searchPaths := []string{filepath.Dir(filepath.Dir(stdFile))}
	path := filepath.Join(t.TempDir(), "main.gala")
	require.NoError(t, os.WriteFile(path, []byte(fallbackSrc), 0o644))

	transpile := func(kind native.GalaKind) (*misbridgingParser, string, error) {
		p := &misbridgingParser{GalaParser: transpiler.NewAntlrGalaParser(), kind: kind}
		out, err := transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, searchPaths),
			transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).Transpile(fallbackSrc, path)
		return p, out, err
	}
	sound, want, err := transpile(0)
	require.NoError(t, err)
	require.True(t, sound.compiled.Bridged(), "a sound parse is not bridged")

	for name, kind := range map[string]native.GalaKind{
		"type":                  native.GalaKIND_type,
		"signature":             native.GalaKIND_signature,
		"parameter":             native.GalaKIND_parameter,
		"block":                 native.GalaKIND_block,
		"expression":            native.GalaKIND_expression,
		"expressionList":        native.GalaKIND_expressionList,
		"sealedTypeDeclaration": native.GalaKIND_sealedTypeDeclaration,
		"opaqueTypeDeclaration": native.GalaKIND_opaqueTypeDeclaration,
		"functionDeclaration":   native.GalaKIND_functionDeclaration,
	} {
		t.Run(name, func(t *testing.T) {
			p, got, err := transpile(kind)
			require.True(t, p.found, "no node of the kind to misbridge")
			assert.False(t, p.compiled.Bridged(), "a misbridged tree is bridged")
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}
