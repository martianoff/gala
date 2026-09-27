package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A struct field, parameter, or local that shares its name with a type must
// not shadow that type in TYPE position. The type name used to be resolved
// through the value scope first, so in `struct Spec(Align Option[Align])` the
// field `Align` (typed `std.Option[Align]`) answered for the type argument and
// its package, `std`, was used to qualify it: `std.Option[std.Align]`.
func TestBindingNamedLikeTypeDoesNotShadowType(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	const prelude = `package main

sealed type Align {
    case AlignLeft()
    case AlignRight()
}

struct Style(Bold bool)

struct Box[T any](Item T)
`
	tests := []struct {
		name string
		decl string
		want []string
	}{
		{
			name: "struct field named like Option type argument",
			decl: `struct Spec(Name string, Align Option[Align])`,
			want: []string{"Align std.Immutable[std.Option[Align]]"},
		},
		{
			name: "struct field named like its plain type",
			decl: `struct Theme(Style Style, Align Align)`,
			want: []string{"Style std.Immutable[Style]", "Align std.Immutable[Align]"},
		},
		{
			name: "struct field named like a user generic type argument",
			decl: `struct Holder(Box Box[Box[int]], Style Option[Style])`,
			want: []string{"Box   std.Immutable[Box[Box[int]]]", "Style std.Immutable[std.Option[Style]]"},
		},
		{
			name: "sealed variant field named like its type argument",
			decl: `sealed type Cell {
    case Text(Align Option[Align], Body string)
    case Empty()
}`,
			want: []string{"Align std.Option[Align]"},
		},
		{
			name: "function parameters named like their types",
			decl: `func describe(Align Option[Align], Style Style) string = s"${Align.IsDefined()} ${Style.Bold}"`,
			want: []string{"func describe(Align std.Option[Align], Style Style) string"},
		},
		{
			name: "method parameter named like its type argument",
			decl: `struct Spec(Align Option[Align])
func (sp Spec) With(Align Option[Align]) Spec = sp.Copy(Align = Align)`,
			want: []string{"With(Align std.Option[Align]) Spec"},
		},
		{
			name: "local named like the type argument of its initializer",
			decl: `func pick() Option[Align] {
    val Align = Some[Align](AlignRight())
    return Align
}`,
			want: []string{"std.Some[Align]{}"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile(prelude+"\n"+tc.decl+"\n\nfunc main() {}\n", "")
			require.NoError(t, err)
			assert.NotContains(t, got, "std.Align", "a local type must never be qualified with std")
			assert.NotContains(t, got, "std.Style", "a local type must never be qualified with std")
			assert.NotContains(t, got, "std.Box", "a local type must never be qualified with std")
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
		})
	}
}
