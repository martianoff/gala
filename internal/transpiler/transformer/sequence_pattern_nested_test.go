package transformer_test

import (
	"regexp"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sequencePatternPrelude = `package main

import . "martianoff/gala/collection_immutable"

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}

struct Box[T any](V T, Tag string)
struct Pair[K any, V any](Key K, Val V)
struct Plain(N int)

`

func transpileSequencePattern(t *testing.T, body string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
	return trans.Transpile(sequencePatternPrelude+body+"\n", "")
}

// guardBlockOf returns the body of the first `if <sizeCheck> { ... }` block
// that follows the size check `<name> := obj.Size() >= N`, so a test can
// assert what runs inside the guard and what runs after it.
func guardBlockOf(t *testing.T, code string) (guard string, after string) {
	t.Helper()
	m := regexp.MustCompile(`(_tmp_\d+) := obj\.Size\(\) >= \d+`).FindStringSubmatch(code)
	require.NotNil(t, m, "size check not found in:\n%s", code)
	start := strings.Index(code, "if "+m[1]+" {")
	require.NotEqual(t, -1, start, "size guard not found in:\n%s", code)
	end := strings.Index(code[start:], "\n\t\t\t}\n")
	require.NotEqual(t, -1, end, "end of size guard not found in:\n%s", code)
	return code[start : start+end], code[start+end:]
}

func TestSequencePatternNestedSubPatterns(t *testing.T) {
	tests := []struct {
		name string
		body string
		// inGuard must appear inside the size guard; afterGuard after it.
		inGuard     []string
		afterGuard  []string
		notContains []string
	}{
		{
			name: "nested sealed variant is a pattern, its binding is visible to the arm",
			body: `func f(xs Array[Shape]) int = xs match {
    case Array(Square(s), _) => s
    case _                   => 0
}`,
			inGuard:     []string{"= obj.Get(0)"},
			afterGuard:  []string{"Square{}.Unapply(", "s := "},
			notContains: []string{"Square :=", "var Square"},
		},
		{
			name: "doubly nested extractor with a rest binding",
			body: `func f(xs Array[Option[Shape]]) int = xs match {
    case Array(Some(Square(s)), rest...) => s + rest.Size()
    case _                               => 0
}`,
			inGuard:     []string{"rest = obj.SeqDrop(1)"},
			afterGuard:  []string{"Some[Shape]{}.Unapply(", "Square{}.Unapply(", "s := "},
			notContains: []string{"Some :=", "Square :="},
		},
		{
			name: "literal element compares, plain element binds",
			body: `func f(xs List[int]) int = xs match {
    case List(0, second, _...) => second
    case _                     => 0
}`,
			afterGuard: []string{"== 0", "second := "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transpileSequencePattern(t, tt.body)
			require.NoError(t, err)
			guard, after := guardBlockOf(t, got)
			for _, want := range tt.inGuard {
				assert.Contains(t, guard, want, "expected inside the size guard")
			}
			for _, want := range tt.afterGuard {
				assert.Contains(t, after, want, "expected after the size guard")
				assert.NotContains(t, guard, want, "must not be inside the size guard")
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, got, unwanted)
			}
		})
	}
}

// A typed element's ok flag is assigned (not declared) inside the guard, so
// the arm condition that reads it compiles.
func TestSequencePatternTypedElementOkFlagDeclaredOutside(t *testing.T) {
	got, err := transpileSequencePattern(t, `func f(xs Array[any]) int = xs match {
    case Array(n: int, _) => n
    case _                => 0
}`)
	require.NoError(t, err)
	m := regexp.MustCompile(`n, (_tmp_\d+) = std\.As\[int\]\(obj\.Get\(0\)\)`).FindStringSubmatch(got)
	require.NotNil(t, m, "typed element should assign through std.As:\n%s", got)
	assert.Contains(t, got, "var "+m[1]+" bool")
	assert.Contains(t, got, "var n int")
}

func TestGenericStructPatternOnAnySubject(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "single type argument",
			body: `func f(x any) int = x match {
    case Box[int](v, _) => v + 1
    case _              => 0
}`,
			contains: []string{".(Box[int])", "v := "},
		},
		{
			name: "two type arguments type both fields",
			body: `func f(x any) string = x match {
    case Pair[string, int](k, v) => s"$k${v * 2}"
    case _                       => ""
}`,
			contains: []string{".(Pair[string, int])"},
		},
		{
			name: "non-generic struct still asserts to itself",
			body: `func f(x any) int = x match {
    case Plain(n) => n
    case _        => 0
}`,
			contains: []string{".(Plain)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transpileSequencePattern(t, tt.body)
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			assert.NotContains(t, got, ".(Box)")
			assert.NotContains(t, got, ".(Pair)")
		})
	}
}

// Without type arguments there is no instantiation to assert to, so the
// pattern is rejected instead of emitting `obj.(Box)`.
func TestGenericStructPatternOnAnySubjectNeedsTypeArgs(t *testing.T) {
	_, err := transpileSequencePattern(t, `func f(x any) string = x match {
    case Box(_, tag) => tag
    case _           => ""
}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot match generic struct 'Box' against a value of type 'any'")
	assert.Contains(t, err.Error(), "case Box[T](...)")
}
