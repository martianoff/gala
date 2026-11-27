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

// TestGoSignatureExactSpelling pins that a lambda passed to a Go function or
// stored in a Go struct field, and a value a Go function returns, are spelled
// with the generic struct instances and array lengths their Go types name:
// `func(*Box[int]) int`, not `func(*lib.Box) int`, which Go rejects (#1456).
func TestGoSignatureExactSpelling(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gsigmod\n\ngo 1.23\n"), 0o600))
	libDir := filepath.Join(dir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "lib.go"), []byte(`package lib

import "strings"

type Box[T any] struct{ V T }

func Take(f func(*Box[int]) int) int { return f(&Box[int]{V: 1}) }

func SumPair(f func([2]int) int) int { return f([2]int{2, 3}) }

func First() *Box[int] { return &Box[int]{V: 7} }

type Handlers struct {
	OnBox func(*Box[int]) int
}

var DefaultBox = &Box[int]{V: 3}

func Get[P ~*Box[T], T any](p P) T { return p.V }

type Iter[T any] interface{ Next() T }

type IntIter struct{}

func (IntIter) Next() int { return 1 }

func Ints() Iter[int] { return IntIter{} }

type Holder struct{ It Iter[int] }

type Named[T any] struct {
	strings.Builder
	V T
}

func NewNamed() *Named[int] { return &Named[int]{} }

type Inner[U any] struct{ v U }

func (i *Inner[U]) Get() U { return i.v }

func (i *Inner[U]) Len() int { return 2 }

type Outer[A, B any] struct {
	Inner[A]
	b B
}

func NewOuter() *Outer[int, string] { return &Outer[int, string]{} }

type Src[T any] = Iter[T]

type Plain struct{ Inner[int] }

func NewPlain() *Plain { return &Plain{} }

type IntNamed = Named[int]

func NewAliased() *IntNamed { return &Named[int]{} }
`), 0o600))
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(dir))

	tests := []struct {
		name, body, want string
	}{
		{"lambda passed to a Go function over a struct instance", "Println(lib.Take((b) => b.V))", "func(b *lib.Box[int]) int"},
		{"lambda passed to a Go function over a fixed-length array", "Println(lib.SumPair((xs) => xs[0]))", "func(xs [2]int) int"},
		{"a Go function's result in a generic slot", "Println(Some(lib.First()))", "std.Some[*lib.Box[int]]"},
		{"a Go package variable in a generic slot", "Println(Some(lib.DefaultBox))", "std.Some[*lib.Box[int]]"},
		{"a constraint's core type binds through a struct instance", "val v = lib.Get(lib.First())\n    val f = () => v\n    Println(f())", "func() int"},
		{"a method of a generic Go interface a Go function returns", "val x = lib.Ints().Next()\n    val f = () => x + 1\n    Println(f())", "func() int"},
		{"a method of a generic Go interface a Go struct field holds", "val h = lib.Holder(It = lib.Ints())\n    val f = () => h.It.Next() + 1\n    Println(f())", "func() int"},
		{"a method promoted from an embedded field of a generic Go struct", "val n = lib.NewNamed()\n    val f = () => n.String() + \"!\"\n    Println(f())", "func() string"},
		{"a promoted method value through an alias of a generic Go struct", "val n = lib.NewAliased()\n    val m = n.String\n    val f = () => m() + \"!\"\n    Println(f())", "func() string"},
		{"a method promoted from an embedded generic field, naming its parameter", "val x = lib.NewOuter().Get()\n    val f = () => x + 1\n    Println(f())", "func() int"},
		{"a method promoted from an embedded generic field, naming none", "val n = lib.NewOuter().Len()\n    val f = () => n + 1\n    Println(f())", "func() int"},
		{"a type pattern on a generic Go interface a Go function returns", "val n = lib.Ints() match {\n        case it: lib.IntIter => it.Next()\n        case _ => 0\n    }\n    Println(n)", "any(_subj).(type)"},
		{"a method of an instance of a generic alias of a generic Go interface", "val s lib.Src[int] = lib.Ints()\n    val x = s.Next()\n    val f = () => x + 1\n    Println(f())", "func() int"},
		{"a method value promoted from a generic field into a non-generic Go struct", "val g = lib.NewPlain().Get\n    val f = () => g() + 1\n    Println(f())", "func() int"},
		{"lambda in a Go struct field", "Println(lib.Handlers(OnBox = (b) => b.V))", "func(b *lib.Box[int]) int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			src := "package main\n\nimport \"gsigmod/lib\"\n\nfunc main() {\n    " + tt.body + "\n}\n"
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			require.Contains(t, got, tt.want)
		})
	}
}
