package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoAliasChainFieldAndMethod covers a field read and a method call on a
// value whose type is a Go alias of a Go alias. The analyzer records an alias
// whose target go/types cannot load as written, so `Outer = Mid` and
// `Mid = other.Config` are two separate steps; both the field and the method
// lookup follow every step, as the interface check does (#1085).
func TestGoAliasChainFieldAndMethod(t *testing.T) {
	dir := t.TempDir()
	const bridge = `package main

import "example.test/nonexistent/other"

type Mid = other.Config
type Outer = Mid
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bridge.go"), []byte(bridge), 0o600))
	const src = `package main

func name(c Outer) int = c.Name.Size()

func label(c Outer) int = c.Label().Size()

func main() {
}
`
	mainPath := filepath.Join(dir, "main.gala")
	require.NoError(t, os.WriteFile(mainPath, []byte(src), 0o600))

	p := transpiler.NewAntlrGalaParser()
	parsed := p.ParseFile(src)
	require.NoError(t, parsed.Err())
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	richAST, err := a.AnalyzeParsed(parsed, mainPath)
	require.NoError(t, err)
	withNative(richAST, parsed)
	require.NotNil(t, richAST.GoTypeInfo)

	// The chain as the analyzer records it: each alias names the next.
	mid := richAST.GoTypeInfo.ResolveTypeAlias("main.Outer")
	require.NotNil(t, mid, "Outer must be recorded as an alias")
	assert.Equal(t, "Mid", mid.(transpiler.NamedType).Name, "Outer names Mid, not Mid's target")

	// The target's Go type information, as an import of it would supply.
	config := &transpiler.GoTypeData{
		Kind:       "struct",
		Fields:     map[string]transpiler.Type{"Name": transpiler.BasicType{Name: "string"}},
		FieldOrder: []string{"Name"},
		Methods: map[string]*transpiler.GoFuncSignature{
			"Label": {Returns: []transpiler.Type{transpiler.BasicType{Name: "string"}}},
		},
	}
	richAST.GoTypeInfo.Types["other.Config"] = config
	richAST.GoTypeInfo.Types["example.test/nonexistent/other.Config"] = config

	tr := transformer.NewGalaASTTransformer()
	t.Setenv("GALA_WARN_TYPES", "1")
	_, _, err = tr.Transform(richAST)
	require.NoError(t, err)
	// `.Size()` on a string lowers by the receiver's type, so each read
	// must be typed.
	for _, u := range transformer.UnresolvedTypes(tr) {
		t.Errorf("unresolved at line %d: %s", u.Line, u.Expr)
	}
}
