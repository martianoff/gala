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

// TestGoGenericAliasSlot pins that a generic Go type alias of a function
// type (`type Handler[T any] = func(T) error`) types a lambda that fills a
// parameter of it, as the generic defined type `Pred[T]` does: the alias's
// type arguments come from the other arguments (#1362).
func TestGoGenericAliasSlot(t *testing.T) {
	dir := t.TempDir()
	// go 1.23, so any Go SDK from 1.23 on loads the module; the analyzer
	// records the generic aliases all the same. examples/go_generic_alias
	// covers them in a module at the repository's Go version.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module galiasmod\n\ngo 1.23\n"), 0o600))
	libDir := filepath.Join(dir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "lib.go"), []byte(`package lib

type Handler[T any] = func(T) error

type Seq[T any] = func(yield func(T) bool)

func Run[T any](x T, h Handler[T]) error { return h(x) }

func Collect[T any](s Seq[T]) []T {
	var out []T
	s(func(v T) bool { out = append(out, v); return true })
	return out
}

func Ints() Seq[int] { return func(yield func(int) bool) { yield(1) } }
`), 0o600))
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(dir))

	tests := []struct {
		name, body, want string
	}{
		{"lambda in a generic alias slot", `Println(lib.Run(5, (x) => nil))`, "func(x int) error"},
		{"Handler instance as a lambda slot", `Println(lib.Run("a", (s) => nil))`, "func(s string) error"},
		{"typed val of a generic alias instance", "val h lib.Handler[int] = (x) => nil\n    Println(h(1))", "func(x int) error"},
		{"conversion to a generic alias instance", "val h = lib.Handler[int]((x) => nil)\n    Println(h(1))", "func(x int) error"},
		{"GALA function taking a generic alias instance", "Println(apply((x) => nil))\n}\n\nfunc apply(h lib.Handler[int]) error {\n    h(1)", "func(x int) error"},
		{"Seq alias instance fed to a GALA lambda", "val s lib.Seq[int] = (yield) => { yield(1) }\n    Println(lib.Collect(s))", "func(yield func(int) bool)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			src := "package main\n\nimport \"galiasmod/lib\"\n\nfunc main() {\n    " + tt.body + "\n}\n"
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			require.Contains(t, got, tt.want)
		})
	}
}

