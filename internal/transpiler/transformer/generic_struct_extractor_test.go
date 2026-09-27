package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genericStructExtractorPrelude declares the generic structs the cases below
// destructure. A struct extractor on a generic struct must read the struct's
// own fields, typed by their declarations with the subject's type arguments
// substituted — never tuple accessors typed by the type arguments themselves.
const genericStructExtractorPrelude = `package main

sealed type Mode[T any] {
    case A(Fn func(int) T)
    case B(Fn func(string) T)
}

struct Inner[T any](X T)
struct Box[T any](Md Mode[T], In Inner[T])
struct Pair[K any, V any](Key K, Val V)

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}

struct Holder(Sh Shape, N int)

`

func TestGenericStructExtractor(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contains    []string
		notContains []string
	}{
		{
			// The binding takes the substituted field type Mode[int], so it
			// agrees with the default arm's b.Md.
			name: "binding takes the substituted field type",
			body: `func f(b Box[int]) Mode[int] = b match {
    case Box(mm, _) => mm
    case _          => b.Md
}`,
			contains:    []string{"mm := obj.Md.Get()"},
			notContains: []string{".V1"},
		},
		{
			name: "single-field generic struct on a type-parameter receiver",
			body: `func (b Box[T]) F() T = b.In match {
    case Inner(x) => x
    case _        => b.In.X
}`,
			contains:    []string{"x := obj.X.Get()"},
			notContains: []string{".V1"},
		},
		{
			name: "single-field generic struct with concrete type arguments",
			body: `func f(i Inner[int]) int = i match {
    case Inner(x) => x + 1
    case _        => 0
}`,
			contains:    []string{"x := obj.X.Get()"},
			notContains: []string{".V1"},
		},
		{
			name: "nested sealed variant inside a generic struct extractor",
			body: `func f(b Box[int]) string = b match {
    case Box(A(_), _) => "a"
    case _            => "o"
}`,
			contains:    []string{"A[int]{}.Unapply(obj.Md.Get())"},
			notContains: []string{"A :="},
		},
		{
			name: "nested variant binding its field inside a generic struct",
			body: `func f(b Box[int]) int = b match {
    case Box(A(g), _) => g(1)
    case Box(B(g), _) => g("s")
    case _            => 0
}`,
			notContains: []string{"A :=", "B :="},
		},
		{
			name: "multi-type-param generic struct with a literal sub-pattern",
			body: `func f(p Pair[string, int]) string = p match {
    case Pair("k", v) => s"${v + 1}"
    case Pair(k, v)   => s"$k${v + 2}"
    case _            => ""
}`,
			contains:    []string{`obj.Key.Get() == "k"`, "v := obj.Val.Get()", "k := obj.Key.Get()"},
			notContains: []string{".V1", ".V2"},
		},
		{
			name: "nested generic struct with a guard",
			body: `func f(b Box[int]) int = b match {
    case Box(_, Inner(x)) if x > 5 => x
    case _                         => 0
}`,
			contains:    []string{"x := obj.In.Get().X.Get()"},
			notContains: []string{".V1", ".V2"},
		},
		{
			name: "nested variant inside a non-generic struct extractor",
			body: `func f(h Holder) int = h match {
    case Holder(Circle(r), n) => r * n
    case _                    => 0
}`,
			contains:    []string{"Circle{}.Unapply(obj.Sh.Get())", "n := obj.N.Get()"},
			notContains: []string{"Circle :="},
		},
		{
			name: "nested variant inside a tuple pattern",
			body: `func f(t Tuple[Shape, int]) int = t match {
    case (Circle(r), n) => r * n
    case _              => 0
}`,
			contains:    []string{"Circle{}.Unapply(obj.V1.Get())"},
			notContains: []string{"Circle :="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

			got, err := trans.Transpile(genericStructExtractorPrelude+tt.body+"\n", "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, got, unwanted)
			}
		})
	}
}

// The extracted binding's type is the substituted field type, so a mismatch
// with another arm names Mode[int] — not the type argument int.
func TestGenericStructExtractorBindingTypeMismatch(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	_, err := trans.Transpile(genericStructExtractorPrelude+`func main() {
    val b = Box[int](Md = A[int]((x) => x), In = Inner[int](X = 7))
    val m = b match {
        case Box(mm, _) => mm
        case _          => 1
    }
    Println(m)
}
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returns 'Mode[int]'")
}
