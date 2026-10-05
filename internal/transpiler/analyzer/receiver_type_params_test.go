package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

const renamedReceiverTypes = `package recv

struct Box[T any](V T)

struct Pair[A any, B any](L A, R B)
`

const renamedReceiverMethods = `package recv

func (b Box[U]) Twice(f func(U) U) U = f(f(b.V))
func (b *Box[Elem]) Peek(f func(Elem) string) string = f(b.V)
func (b Box[U]) Map[V any](f func(U) V) Box[V] = Box(f(b.V))
func (b Box[U]) Clash[T any](f func(U) T) T = f(b.V)
func (p Pair[X, Y]) Swap() Pair[Y, X] = Pair(p.R, p.L)
func (p Pair[A, Y]) Both(f func(A, Y) string) string = f(p.L, p.R)
`

// TestRenamedReceiverTypeParams pins that a method whose receiver names its
// type's parameters differently (`func (b Box[U])` on `Box[T]`) records its
// signature in the type's own names, which is what a call reads it against —
// whether the method is in the file analyzed or in a sibling of it. A type
// parameter of the method the type's names would capture (`Clash[T]` on a
// `Box[T]` written `Box[U]`) gets a fresh name.
func TestRenamedReceiverTypeParams(t *testing.T) {
	want := map[string]struct{ params, result string }{
		"Twice": {"func(T) T", "T"},
		"Peek":  {"func(T) string", "string"},
		"Map":   {"func(T) V", "recv.Box[V]"},
		"Clash": {"func(T) T_", "T_"},
	}
	wantPair := map[string]struct{ params, result string }{
		"Swap": {"", "recv.Pair[B, A]"},
		"Both": {"func(A, B) string", "string"},
	}
	for _, tc := range []struct {
		name     string
		analyzed string // which file's analysis is inspected
	}{
		{"methods in the analyzed file", "methods.gala"},
		{"methods in a sibling file", "types.gala"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rich := analyzeRecvPackage(t, tc.analyzed)
			check := func(typeName string, cases map[string]struct{ params, result string }) {
				meta := rich.Types[typeName]
				require.NotNil(t, meta, typeName)
				for method, w := range cases {
					m := meta.Methods[method]
					require.NotNil(t, m, method)
					var params string
					if len(m.ParamTypes) > 0 {
						params = m.ParamTypes[0].String()
					}
					assert.Equal(t, w.params, params, method)
					assert.Equal(t, w.result, m.ReturnType.String(), method)
				}
			}
			check("recv.Box", want)
			check("recv.Pair", wantPair)
		})
	}
}

func analyzeRecvPackage(t *testing.T, analyzed string) *transpiler.RichAST {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/recv\n\ngala dev\n"), 0o644))
	files := map[string]string{"types.gala": renamedReceiverTypes, "methods.gala": renamedReceiverMethods}
	var sibling string
	for name, src := range files {
		require.NoError(t, os.WriteFile(filepath.Join(tmp, name), []byte(src), 0o644))
		if name != analyzed {
			sibling = filepath.Join(tmp, name)
		}
	}
	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, append([]string{tmp}, getStdSearchPath()...), tmp)
	tree, _, err := p.Parse(files[analyzed])
	require.NoError(t, err)
	batch.SetPackageFiles([]string{sibling})
	rich, err := batch.Analyze(tree, nil, filepath.Join(tmp, analyzed))
	require.NoError(t, err)
	return rich
}

// TestRenamedGoReceiverTypeParams pins the same rule for a generic type
// declared in Go: a method whose receiver renames the type's parameters is
// recorded in the type's own names.
func TestRenamedGoReceiverTypeParams(t *testing.T) {
	skipIfNoGoSDK(t)
	dir := t.TempDir()
	src := "package sample\n\n" +
		"type Box[T any] struct{ V T }\n\n" +
		"func (b Box[U]) Twice(f func(U) U) U { return f(f(b.V)) }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sample.go"), []byte(src), 0o644))
	info := analyzer.AnalyzeGoFiles(dir, "")
	require.NotNil(t, info)
	box := info.Types["sample.Box"]
	require.NotNil(t, box)
	twice := box.Methods["Twice"]
	require.NotNil(t, twice)
	assert.Equal(t, []string{"T"}, twice.TypeParams)
	assert.Equal(t, "func(T) T", twice.Params[0].Type.String())
	assert.Equal(t, "T", twice.Returns[0].String())
}
