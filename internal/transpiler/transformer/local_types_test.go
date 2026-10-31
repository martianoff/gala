package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// A struct type declared inside a function body is constructed with call
// syntax like a top-level one. Inside its block it answers for its name ahead
// of a package-level type or alias of the same name; after the block, and in
// other functions, the outer declaration is back.
func TestLocalTypeConstructor(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	tests := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{
			name: "positional, named and subset construction",
			src: `func f() int {
    type Pair struct {
        A int
        B int
    }
    val p = Pair(1, 2)
    val q = Pair(B = 4, A = 3)
    val r = Pair(5)
    p.A + q.B + r.A
}`,
			want: []string{
				"Pair{A: std.NewImmutable(1), B: std.NewImmutable(2)}",
				"Pair{A: std.NewImmutable(3), B: std.NewImmutable(4)}",
				"Pair{A: std.NewImmutable(5)}",
			},
			notWant: []string{"Pair(1, 2)"},
		},
		{
			name: "generic local type infers its type argument",
			src: `func f() int {
    type Box[T any] struct {
        V T
    }
    Box(42).V
}`,
			want: []string{"Box[int]{V: std.NewImmutable(42)}"},
		},
		{
			name: "shadows a package-level struct only inside its block",
			src: `type Inner struct {
    S string
}

func f() int {
    type Inner struct {
        N int
    }
    Inner(1).N
}

func g() string = Inner("top").S`,
			want:    []string{"Inner{N: std.NewImmutable(1)}", `Inner{S: std.NewImmutable("top")}`},
			notWant: []string{`Inner{N: std.NewImmutable("top")}`},
		},
		{
			name: "shadows a package-level alias only inside its block",
			src: `type Label string

func f() int {
    type Label struct {
        Len int
    }
    Label(7).Len
}

func g() Label = Label("x")`,
			want:    []string{"Label{Len: std.NewImmutable(7)}", `Label("x")`},
			notWant: []string{"Label(7)"},
		},
		{
			name: "a field may name the local type itself",
			src: `func f() int {
    type Node struct {
        V int
        Next func() Option[Node]
    }
    val n = Node(1, () => None())
    n.Next().Map((m) => m.V).GetOrElse(n.V)
}`,
			want: []string{"Next std.Immutable[func() std.Option[Node]]", "Node{V: std.NewImmutable(1)"},
		},
		{
			name: "no methods are generated for a local type",
			src: `func f() int {
    type Box[T any] struct {
        V T
    }
    Box(1).V
}`,
			notWant: []string{"func (b Box", "BoxInstance", ") Copy(", ") Equal(", ") Unapply("},
		},
		{
			name: "same name declared in two functions",
			src: `func f() int {
    type Cell struct {
        A int
    }
    Cell(1).A
}

func g() string {
    type Cell struct {
        B string
    }
    Cell("b").B
}`,
			want: []string{"Cell{A: std.NewImmutable(1)}", `Cell{B: std.NewImmutable("b")}`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile("package main\n\n"+tc.src+"\n\nfunc main() {}\n", "")
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
			for _, w := range tc.notWant {
				assert.NotContains(t, got, w)
			}
		})
	}
}
