package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestGoDefinedFuncTypeExactSpelling pins that a lambda typed by a Go defined
// function type is spelled with the instances and array lengths its
// signature names: `func(*Box[int]) error`, not `func(*lib.Box) error`,
// which Go rejects (#1431).
func TestGoDefinedFuncTypeExactSpelling(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gexactmod\n\ngo 1.23\n"), 0o600))
	libDir := filepath.Join(dir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "lib.go"), []byte(`package lib

type Box[T any] struct{ V T }

type WithBox[T any] func(*Box[T]) error

type IntBox func(*Box[int]) error

type WithPair[T any] func([2]T) T
`), 0o600))
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(dir))

	tests := []struct {
		name, body, want string
	}{
		{"generic defined type over a struct instance", "val h lib.WithBox[int] = (b) => nil\n    Println(h(nil))", "func(b *lib.Box[int]) error"},
		{"defined type over a struct instance", "val h lib.IntBox = (b) => nil\n    Println(h(nil))", "func(b *lib.Box[int]) error"},
		{"generic defined type over a fixed-length array", "val h lib.WithPair[int] = (xs) => xs[0]\n    Println(h != nil)", "func(xs [2]int) int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			src := "package main\n\nimport \"gexactmod/lib\"\n\nfunc main() {\n    " + tt.body + "\n}\n"
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			require.Contains(t, got, tt.want)
		})
	}
}
