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
struct Node(V int)
struct AnyHolder(V any)

var probes = 0

struct Probe()

func (p Probe) Unapply(n int) Option[int] {
    probes = probes + 1
    return Some(n)
}

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
			inGuard:     []string{"= obj.Get(0)", "Square{}.Unapply(", "s = "},
			notContains: []string{"Square :=", "var Square", "s := "},
		},
		{
			name: "doubly nested extractor with a rest binding",
			body: `func f(xs Array[Option[Shape]]) int = xs match {
    case Array(Some(Square(s)), rest...) => s + rest.Size()
    case _                               => 0
}`,
			inGuard:     []string{"rest = obj.SeqDrop(1)", "Some[Shape]{}.Unapply(", "Square{}.Unapply(", "s = "},
			notContains: []string{"Some :=", "Square :=", "s := "},
		},
		{
			name: "plain element binds inside the guard, the literal compares in the arm condition",
			body: `func f(xs List[int]) int = xs match {
    case List(0, second, _...) => second
    case _                     => 0
}`,
			inGuard:    []string{"second = "},
			afterGuard: []string{"== 0"},
		},
		{
			// A struct pattern on a pointer element reads a field through the
			// pointer; on an empty array that pointer is nil.
			name: "struct pattern on a pointer element reads fields only inside the guard",
			body: `func f(xs Array[*Node]) int = xs match {
    case Array(Node(v), rest...) => v + rest.Size()
    case _                       => 0
}`,
			inGuard:     []string{".V.Get()", "v = "},
			notContains: []string{"v := "},
		},
		{
			// A user extractor must not run (or count) for an arm whose
			// sequence is too short.
			name: "user extractor runs only inside the guard",
			body: `func f(xs Array[int]) int = xs match {
    case Array(Probe(x), y) => x + y
    case _                  => 0
}`,
			inGuard: []string{"Probe{}.Unapply("},
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

// A sub-pattern of an Option-returning extractor reads the payload, which is
// only set when the extractor matched: `Some(Node(v))` against None must not
// read a field through the nil payload. The sub-pattern runs inside the
// `if ok { payload = result.Get() ... }` guard; `v` is declared before it.
func TestExtractorSubPatternRunsInsideGuard(t *testing.T) {
	got, err := transpileSequencePattern(t, `func f(o Option[*Node]) int = o match {
    case Some(Node(v)) => v
    case _             => 0
}`)
	require.NoError(t, err)
	m := regexp.MustCompile(`if (_tmp_\d+) \{\n\s*(_tmp_\d+) = (_tmp_\d+)\.Get\(\)`).FindStringSubmatch(got)
	require.NotNil(t, m, "extractor guard not found in:\n%s", got)
	start := strings.Index(got, m[0])
	end := strings.Index(got[start:], "\n\t\t\t}\n")
	require.NotEqual(t, -1, end)
	guard := got[start : start+end]
	assert.Contains(t, guard, m[2]+".V.Get()", "the field read must be inside the guard")
	assert.Contains(t, guard, "v = ")
	assert.Contains(t, got[:start], "var v int", "v must be declared before the guard")
	assert.NotContains(t, got, "v := ")
}

// A binding whose declared type is `any` (a field declared `any`, a tuple
// element typed `any`) is hoisted as `any`: that is its type, not an erasure.
func TestHoistedBindingOfDeclaredAnyType(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "struct field declared any inside an extractor payload",
			body: `func f(o Option[AnyHolder]) string = o match {
    case Some(AnyHolder(v)) => s"$v"
    case _                  => ""
}`,
			contains: []string{"var v any", "v = "},
		},
		{
			name: "tuple element typed any inside an extractor payload",
			body: `func f(o Option[Tuple[any, int]]) string = o match {
    case Some(Tuple(a, n)) => s"$a${n + 1}"
    case _                 => ""
}`,
			contains: []string{"var a any", "var n int"},
		},
		{
			name: "struct field declared any inside a sequence element",
			body: `func f(xs Array[AnyHolder]) string = xs match {
    case Array(AnyHolder(v), _) => s"$v"
    case _                      => ""
}`,
			contains: []string{"var v any", "v = "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transpileSequencePattern(t, tt.body)
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// A binding hoisted out of a guard is a `var`, not a `:=`; the GALA
// unused-binding check must still see it.
func TestHoistedBindingUnusedIsReported(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "extractor payload",
			body: `func f(o Option[AnyHolder]) string = o match {
    case Some(AnyHolder(v)) => "yes"
    case _                  => "no"
}`,
			want: "unused variable 'v' in match branch",
		},
		{
			name: "nested sequence element",
			body: `func f(xs Array[Shape]) string = xs match {
    case Array(Square(s), _) => "square"
    case _                   => "other"
}`,
			want: "unused variable 's' in match branch",
		},
		{
			name: "plain sequence element",
			body: `func f(xs Array[int]) string = xs match {
    case Array(first, _) => "some"
    case _               => "none"
}`,
			want: "unused variable 'first' in match branch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileSequencePattern(t, tt.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
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
