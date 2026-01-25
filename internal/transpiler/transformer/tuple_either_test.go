package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTupleEither(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "Tuple extraction",
			input: `package main

func main() {
    val t = Tuple[int, string](V1 = 1, V2 = "hello")
    val res = t match {
        case Tuple(a, b) => a
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"obj.V1.Get()",
				"obj.V2.Get()",
			},
		},
		{
			name: "Either extraction explicit type",
			input: `package main

func main() {
    val e Either[int, string] = Left[int, string](10)
    val res = e match {
        case Left(n) => n
        case Right(s) => len(s)
        case _ => 0
    }
}`,
			expected: []string{
				"std.Left[int, string]{}.Apply",
				"std.Left[int, string]{}.Unapply(obj)",
				"std.Right[int, string]{}.Unapply(obj)",
			},
		},
		{
			name: "Either extraction implicit type",
			input: `package main

func main() {
    val e = Left[int, string](10)
    val res = e match {
        case Left(n) => n
        case Right(s) => len(s)
        case _ => 0
    }
}`,
			expected: []string{
				"std.Left[int, string]{}.Apply",
				"std.Left[int, string]{}.Unapply(obj)",
				"std.Right[int, string]{}.Unapply(obj)",
			},
		},
		{
			name: "Either generic method",
			input: `package main

func main() {
    val e = Left[int, string](10)
    val mapped = e.Map[int]((s string) => len(s))
}`,
			expected: []string{
				// Explicit type args include both method type arg and receiver type args
				"std.Either_Map[int, int, string](e.Get()",
			},
		},
		{
			name: "Either inferred type match",
			input: `package main

import "fmt"

func main() {
    val e1 = Left[int, string](10)
    val res = e1 match {
        case Left(n) => fmt.Sprintf("Left: %d", n)
        case Right(s) => fmt.Sprintf("Right: %s", s)
        case _ => "Unknown"
    }
}`,
			expected: []string{
				"std.Either[int, string]",
				"std.Left[int, string]{}.Unapply(obj)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp), "Output missing %q\nGot:\n%s", exp, got)
			}
		})
	}
}

func TestTupleSyntax(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "Tuple literal syntax pair",
			input: `package main

func main() {
    val pair = (1, "hello")
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"V1: std.NewImmutable(1)",
				"V2: std.NewImmutable(\"hello\")",
			},
		},
		{
			name: "Tuple literal syntax triple",
			input: `package main

func main() {
    val triple = (42, "world", true)
}`,
			expected: []string{
				"std.Tuple3[int, string, bool]",
				"V1: std.NewImmutable(42)",
				"V2: std.NewImmutable(\"world\")",
				"V3: std.NewImmutable(true)",
			},
		},
		{
			name: "Tuple literal syntax quad",
			input: `package main

func main() {
    val quad = (1, 2, 3, 4)
}`,
			expected: []string{
				"std.Tuple4[int, int, int, int]",
				"V1: std.NewImmutable(1)",
				"V2: std.NewImmutable(2)",
				"V3: std.NewImmutable(3)",
				"V4: std.NewImmutable(4)",
			},
		},
		{
			name: "Tuple3 pattern matching",
			input: `package main

func main() {
    val t = (42, "hello", true)
    val res = t match {
        case Tuple3(a, b, c) => a
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple3[int, string, bool]",
				"obj.V1.Get()",
				"obj.V2.Get()",
				"obj.V3.Get()",
			},
		},
		{
			name: "Tuple pattern matching with pair",
			input: `package main

func main() {
    val pair = (1, "world")
    val res = pair match {
        case Tuple(x, y) => x
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"obj.V1.Get()",
				"obj.V2.Get()",
			},
		},
		{
			name: "Tuple pattern matching with parentheses syntax",
			input: `package main

func main() {
    val pair = (1, "world")
    val res = pair match {
        case (x, y) => x
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"obj.V1.Get()",
				"obj.V2.Get()",
			},
		},
		{
			name: "Tuple3 pattern matching with parentheses syntax",
			input: `package main

func main() {
    val triple = (1, "hello", true)
    val res = triple match {
        case (a, b, c) => a
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple3[int, string, bool]",
				"obj.V1.Get()",
				"obj.V2.Get()",
				"obj.V3.Get()",
			},
		},
		{
			name: "Function returning tuple with parentheses",
			input: `package main

func getPair() Tuple[int, string] {
    return (42, "answer")
}

func main() {
    val p = getPair()
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"V1: std.NewImmutable(42)",
				"V2: std.NewImmutable(\"answer\")",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp), "Output missing %q\nGot:\n%s", exp, got)
			}
		})
	}
}
