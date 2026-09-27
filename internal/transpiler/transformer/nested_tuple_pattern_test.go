package transformer_test

import (
	"os"
	"path/filepath"
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

// Arity is checked for every call-shaped variant pattern, including the
// qualified form with explicit type arguments (`ev.Got[int](v)`), where the
// name sits in a selector and the type arguments come before the call.
func TestVariantArityQualifiedWithTypeArgs(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/arity\n\ngala dev\n")
	write("ev/ev.gala", `package ev

sealed type Ev[T any] {
    case Got(V T)
    case Nothing()
}
`)
	transpile := func(src string) (string, error) {
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, append([]string{root}, getStdSearchPath()...), root)
		return transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
			Transpile(src, filepath.Join(root, "main.gala"))
	}
	const header = `package main

import "example.com/arity/ev"

`
	t.Run("correct arity", func(t *testing.T) {
		out, err := transpile(header + `func f(e ev.Ev[int]) int = e match {
    case ev.Got[int](v) => v
    case _              => 0
}
`)
		require.NoError(t, err)
		assert.Contains(t, out, "ev.Got[int]{}.Unapply(")
	})
	t.Run("wrong arity is reported at the pattern", func(t *testing.T) {
		_, err := transpile(header + `func f(e ev.Ev[int]) int = e match {
    case ev.Got[int](v, w) => v + w
    case _                 => 0
}
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `sealed variant "Got" pattern binds 2 field(s) but declares 1`)
		var coded *galaerr.SemanticError
		if assert.ErrorAs(t, err, &coded) {
			assert.Equal(t, galaerr.CodeVariantArityMismatch, coded.Code)
			assert.Equal(t, 6, coded.Line, "error line")
			assert.Equal(t, 9, coded.Column, "error column")
		}
	})
}
