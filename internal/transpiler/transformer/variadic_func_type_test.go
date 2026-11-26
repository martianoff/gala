package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVariadicFuncTypeInference: a variadic function's type keeps its `...`
// wherever it is inferred and then spelled: a type argument, a lambda's
// result, a struct field, a closure's signature, and a lambda filling a
// variadic slot. It used to lose it (`func(int) int`, or `func(any) int`
// from a written type), and the generated Go failed to build (#1411).
func TestVariadicFuncTypeInference(t *testing.T) {
	const decls = `package main

import "fmt"
import "log"
import . "martianoff/gala/collection_immutable"

func sum(xs ...int) int = ArrayOf(xs...).FoldLeft(0, (a, x) => a + x)

func twice(f func(...int) int) int = f(1, 2) * 2

func sprint() Option[func(...any) string] = Some(fmt.Sprint)

func logger() *log.Logger = log.Default()

struct Box[T any](V T)

func (b Box[T]) Run[U any](g func(...int) int, u U) Tuple[int, U] = (g(1, 2, 3), u)

`
	tests := []struct {
		name string
		main string
		want string
	}{
		{
			name: "type argument of a constructor",
			main: "val o = Some(sum)\n    Println(o.Get()(1, 1))",
			want: "std.Some[func(...int) int]",
		},
		{
			name: "lambda result",
			main: "val h = () => sum\n    Println(h()(4, 5))",
			want: "func() func(...int) int",
		},
		{
			name: "struct field",
			main: "val b = Box(sum)\n    Println(b.V(3, 4))",
			want: "Box[func(...int) int]",
		},
		{
			name: "method value with a variadic function parameter",
			main: "val r = Box(1).Run[string]\n    Println(r(sum, \"z\"))",
			want: "__arg_",
		},
		{
			name: "lambda in a variadic slot",
			main: "Println(twice((xs) => ArrayOf(xs...).Size()))",
			want: "func(xs ...int) int",
		},
		{
			name: "Go variadic function value",
			main: "val sp = () => sprint().Get()\n    Println(sp()(1, \"a\"))",
			want: "func() func(...any) string",
		},
		{
			name: "element of a collection",
			main: "Println(ListOf(sum).Map((f) => f(1, 2, 3)))",
			want: "func(f func(...int) int) int",
		},
		{
			name: "placeholder lambda in a variadic slot",
			main: "Println(twice(_.Size()))",
			want: "func(__p0 ...int) int",
		},
		{
			name: "Go variadic method value",
			main: "val l = logger()\n    val w = () => l.Print\n    Println(w() != nil)",
			want: "func() func(...any)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			got, err := trans.Transpile(decls+"func main() {\n    "+tt.main+"\n}\n", "variadic_func_type.gala")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
			assert.NotContains(t, got, "func(int) int")
			assert.NotContains(t, got, "func(any) int")
		})
	}
}

// TestVariadicFuncTypeInterfaceMismatch: a method whose function-typed
// parameter differs from the interface's only in `...` does not implement it,
// as in Go; it used to pass GALA's check and fail go build.
func TestVariadicFuncTypeInterfaceMismatch(t *testing.T) {
	src := `package main

type Applier interface {
    Apply(f func(...int) int) int
}

struct Impl(N int)

func (i Impl) Apply(f func(int) int) int = f(i.N)

func runIt(a Applier) int = a.Apply((xs) => 0)

func main() {
    Println(runIt(Impl(1)))
}
`
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
	_, err := trans.Transpile(src, "variadic_iface.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Impl does not implement Applier")
}

// TestGoVariadicFuncType: a Go function type with a variadic parameter keeps
// its `...` when it types a lambda, as a defined type, a generic one, and a
// Go function's parameter (#1411, #1456).
func TestGoVariadicFuncType(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gvarmod\n\ngo 1.23\n"), 0o600))
	libDir := filepath.Join(dir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "lib.go"), []byte(`package lib

type Box[T any] struct{ V T }

type V func(...int) int

type VarFn[T any] func(*Box[T], ...int) error

func Take(f func(string, ...any) string) string { return f("%d", 1) }
`), 0o600))
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(dir))

	tests := []struct {
		name, body, want string
	}{
		{"defined variadic function type", "val v lib.V = (bs) => 1\n    Println(v(1, 2))", "func(bs ...int) int"},
		// Only the `...` is pinned: the lossy spelling of the *Box[int]
		// instance is #1456's.
		{"generic defined variadic function type", "val v lib.VarFn[int] = (b, xs) => nil\n    Println(v(nil, 1))", "xs ...int) error"},
		{"Go function's variadic function parameter", "Println(lib.Take((f, args) => f))", "func(f string, args ...any) string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			src := "package main\n\nimport \"gvarmod/lib\"\n\nfunc main() {\n    " + tt.body + "\n}\n"
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
