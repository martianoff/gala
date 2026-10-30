package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A match over the dynamic type of a type parameter or interface value, whose
// arms are unguarded type patterns, `_` and bindings, is dispatched by one Go
// type switch in front of its arms; any other match keeps an As call per type
// pattern. The runtime behaviour of both is held by examples/match_type_switch.
func TestTypeSwitchDispatch(t *testing.T) {
	const prelude = `package main

struct Point(X int, Y int)

func (p Point) Hash() uint32 = uint32(p.X)

type Celsius int

`
	tests := []struct {
		name string
		body string
		// dispatched: the match lowers to a type switch.
		dispatched bool
		contains   []string
	}{
		{
			name: "type parameter subject",
			body: `func f[T any](v T) string = v match {
    case i: int => s"int $i"
    case s: string => s
    case _ => "other"
}`,
			dispatched: true,
			contains: []string{
				"switch _tmp_", ":= any(obj).(type)",
				"case std.ImmutableUnwrapper, std.Transparent:",
				"= std.As[int](obj)", "= std.As[string](obj)",
				"case int:", "case string:",
			},
		},
		{
			name: "any subject is switched on as it is",
			body: `func f(v any) string = v match {
    case i: int => s"int $i"
    case _ => "other"
}`,
			dispatched: true,
			contains:   []string{":= obj.(type)"},
		},
		{
			name: "interface arm boxes the subject afresh",
			body: `func f[T any](v T) string = v match {
    case h: Hashable => s"${h.Hash()}"
    case _ => "none"
}`,
			dispatched: true,
			contains:   []string{"switch any(obj).(type)", "case std.Hashable:", "= any(obj).(std.Hashable), 1"},
		},
		{
			name: "guarded wildcard and binding arms stay in the chain",
			body: `func f[T any](v T, loud bool) string = v match {
    case i: int => s"int $i"
    case _ if loud => "loud"
    case other => s"other $other"
}`,
			dispatched: true,
			contains:   []string{"case int:", "&& loud"},
		},
		{
			name: "struct type arm",
			body: `func f(v any) int = v match {
    case p: Point => p.X
    case _ => 0
}`,
			dispatched: true,
			contains:   []string{"case Point:"},
		},
		{
			name: "guarded type arm falls back",
			body: `func f[T any](v T) string = v match {
    case i: int if i > 1 => "big"
    case _ => "other"
}`,
		},
		{
			name: "extractor arm falls back",
			body: `func f(v any) int = v match {
    case i: int => i
    case Point(x, _) => x
    case _ => 0
}`,
		},
		{
			name: "value arm falls back",
			body: `func f(v any) int = v match {
    case i: int => i
    case "one" => 1
    case _ => 0
}`,
		},
		{
			name: "type parameter target falls back",
			body: `func f[T any, U any](v T) bool = v match {
    case _: U => true
    case _ => false
}`,
		},
		{
			name: "the same type twice falls back",
			body: `func f[T any](v T) string = v match {
    case _: int => "int"
    case _: Celsius => "celsius"
    case _ => "other"
}`,
		},
		{
			name: "predeclared alias of an earlier arm falls back",
			body: `func f[T any](v T) string = v match {
    case _: uint8 => "uint8"
    case _: byte => "byte"
    case _ => "other"
}`,
		},
		{
			name: "concrete subject is not switched on",
			body: `func f(p Point) int = p match {
    case q: Point => q.X
    case _ => 0
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			tr := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			got, err := tr.Transpile(prelude+tt.body+"\n", "")
			require.NoError(t, err)
			assert.Equal(t, tt.dispatched, strings.Contains(got, ".(type)"), got)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			if !tt.dispatched {
				assert.Contains(t, got, "std.As[", "a match the switch does not dispatch keeps its As calls")
			}
		})
	}
}
