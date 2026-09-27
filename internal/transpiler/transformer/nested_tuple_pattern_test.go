package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nestedTuplePrelude = `package main

import . "martianoff/gala/collection_immutable"

sealed type Msg {
    case Move(Pos Tuple[int, int], Label string)
    case Quit()
}

struct Holder(Pair Tuple[string, int], Tag string)

`

func transpileNestedTuple(t *testing.T, body string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
	return trans.Transpile(nestedTuplePrelude+body+"\n", "")
}

// A parenthesized tuple pattern is ONE argument of the pattern it sits in.
func TestNestedTuplePattern(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "tuple inside Some",
			body: `func f(o Option[Tuple[int, string]]) string = o match {
    case Some((n, s)) => s"$n $s"
    case _            => "none"
}`,
			// The tuple's bindings are hoisted out of the extractor's guard.
			contains: []string{"var n int", "var s string", ".V1.Get()", ".V2.Get()"},
		},
		{
			name: "tuple inside Right",
			body: `func f(e Either[string, Tuple[int, int]]) int = e match {
    case Right((a, b)) => a + b
    case Left(_)       => 0
}`,
			contains: []string{"var a int", "var b int"},
		},
		{
			name: "tuple with a literal inside Success",
			body: `func f(t Try[Tuple[string, bool]]) string = t match {
    case Success((s, true)) => s
    case _                  => ""
}`,
			contains: []string{"var s string", "== true"},
		},
		{
			name: "tuple inside a user variant next to another field",
			body: `func f(m Msg) string = m match {
    case Move((x, y), label) => s"$label $x $y"
    case Quit()              => "quit"
}`,
			contains: []string{"var x int", "var y int", ".V1.Get().V1.Get()", ".V2.Get()"},
		},
		{
			name: "tuple inside a struct extractor",
			body: `func f(h Holder) string = h match {
    case Holder((k, v), tag) => s"$tag $k ${v + 1}"
    case _                   => ""
}`,
			contains: []string{"obj.Pair.Get().V1.Get()", "obj.Pair.Get().V2.Get()"},
		},
		{
			name: "tuple as a sequence element",
			body: `func f(xs Array[Tuple[string, int]]) string = xs match {
    case Array((k, v), _) => s"$k ${v + 1}"
    case _                => ""
}`,
			contains: []string{"var k string", "var v int"},
		},
		{
			name: "tuple inside a tuple",
			body: `func f(t Tuple[Tuple[int, int], string]) string = t match {
    case ((a, b), s) => s"$s ${a * b}"
    case _           => ""
}`,
			contains: []string{"obj.V1.Get().V1.Get()", "obj.V1.Get().V2.Get()"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transpileNestedTuple(t, tt.body)
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// A genuine arity mismatch is still GALA-E0004, now reported at the offending
// pattern rather than at the `match` keyword. Commas inside a nested tuple or
// a string literal do not count as extra fields.
func TestVariantArityMismatchPosition(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
		line    int
		col     int
	}{
		{
			name: "too few fields",
			body: `func f(m Msg) string = m match {
    case Quit()   => "quit"
    case Move(p)  => "move"
}`,
			wantMsg: `sealed variant "Move" pattern binds 1 field(s) but declares 2`,
			line:    14, col: 9,
		},
		{
			name: "tuple and string commas are not extra fields",
			body: `func f(m Msg) string = m match {
    case Move((x, y), "a,b", z) => "move"
    case _                      => "other"
}`,
			wantMsg: `sealed variant "Move" pattern binds 3 field(s) but declares 2`,
			line:    13, col: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileNestedTuple(t, tt.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
			var coded *galaerr.SemanticError
			if assert.ErrorAs(t, err, &coded) {
				assert.Equal(t, tt.line, coded.Line, "error line")
				assert.Equal(t, tt.col, coded.Column, "error column")
			}
		})
	}
}
