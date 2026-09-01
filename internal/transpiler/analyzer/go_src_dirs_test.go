package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSyntheticGoModule writes a tiny, import-free Go package to a fresh
// directory and returns it. Import-free is deliberate: AnalyzeGoFiles
// type-checks the package with the (possibly unavailable) go/importer, but a
// package that imports nothing never invokes the importer, so the fixture
// resolves identically inside and outside the Bazel sandbox — no Go SDK
// needed, fully hermetic.
func writeSyntheticGoModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package widget

type Widget struct {
	Name string
}

func New(name string) Widget { return Widget{Name: name} }

func Parse(s string) (Widget, error) { return Widget{Name: s}, nil }

func (w Widget) Label() string { return w.Name }
`
	if err := os.WriteFile(filepath.Join(dir, "widget.go"), []byte(src), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return dir
}

// TestGoSrcDirs_ThirdPartyModuleResolvesConcreteTypes is the regression test
// for third-party Go MODULE interop. go/importer's "source" mode (used by
// AnalyzeGoPackage) is GOPATH/GOROOT-based and cannot resolve a versioned
// module from the module cache or Bazel's external tree, so a direct import
// of e.g. github.com/google/uuid used to collapse to `any` and produce
// uncompilable Go. With the package's real .go source directory wired in via
// SetGoSrcDirs, the analyzer parses it directly and recovers concrete types.
func TestGoSrcDirs_ThirdPartyModuleResolvesConcreteTypes(t *testing.T) {
	dir := writeSyntheticGoModule(t)

	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, getStdSearchPath(), t.TempDir())
	batch.SetGoSrcDirs(map[string]string{"example.test/widget": dir})

	input := `package main

import "example.test/widget"

func main() {
}
`
	tree, _, err := p.Parse(input)
	require.NoError(t, err)

	richAST, err := batch.Analyze(tree, nil, "")
	require.NoError(t, err)
	require.NotNil(t, richAST)
	require.NotNil(t, richAST.GoTypeInfo, "Go type info should be populated from the wired source dir")

	// widget.New returns a concrete widget.Widget — not `any`.
	retType := richAST.GoTypeInfo.GetFuncReturnType("widget.New")
	require.NotNil(t, retType, "widget.New should resolve")
	assert.Equal(t, "widget.Widget", retType.String())

	// widget.Parse returns (widget.Widget, error) — the (T, error) shape that
	// the transpiler auto-wraps into Try[widget.Widget].
	sig := richAST.GoTypeInfo.GetFuncSignature("widget.Parse")
	require.NotNil(t, sig, "widget.Parse should resolve")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "widget.Widget", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	// The Widget type and its method are visible, so w.Label() resolves to a
	// concrete string instead of failing on `any`.
	retType = richAST.GoTypeInfo.GetMethodReturnType("widget.Widget", "Label")
	require.NotNil(t, retType, "Widget.Label should resolve")
	assert.Equal(t, "string", retType.String())
}

// TestGoSrcDirs_LongestPrefixSubpackage verifies that a registered module-path
// prefix resolves a subpackage import by appending the remaining path segment,
// mirroring how the CLI builder registers one entry per module root while user
// code may import a nested package.
func TestGoSrcDirs_LongestPrefixSubpackage(t *testing.T) {
	moduleRoot := t.TempDir()
	subDir := filepath.Join(moduleRoot, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	src := `package sub

func Tag() string { return "tag" }
`
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "sub.go"), []byte(src), 0644))

	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, getStdSearchPath(), t.TempDir())
	// Register only the module root; the import is a subpackage of it.
	batch.SetGoSrcDirs(map[string]string{"example.test/mod": moduleRoot})

	input := `package main

import "example.test/mod/sub"

func main() {
}
`
	tree, _, err := p.Parse(input)
	require.NoError(t, err)

	richAST, err := batch.Analyze(tree, nil, "")
	require.NoError(t, err)
	require.NotNil(t, richAST.GoTypeInfo)

	retType := richAST.GoTypeInfo.GetFuncReturnType("sub.Tag")
	require.NotNil(t, retType, "sub.Tag should resolve via longest-prefix module root match")
	assert.Equal(t, "string", retType.String())
}
