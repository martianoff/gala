package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyOverrides(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Struct Copy with one override",
			input: `package main

struct Person(val name string, age int)
val p = Person("Alice", 30)
val p2 = p.Copy(age = 31)`,
			expected: `package main

import "martianoff/gala/std"

type Person struct {
	name std.Immutable[string]
	age  std.Immutable[int]
}

func (s Person) Copy() Person {
	return Person{name: std.Copy(s.name), age: std.Copy(s.age)}
}
func (s Person) Equal(other Person) bool {
	return std.Equal(s.name, other.name) && std.Equal(s.age, other.age)
}

var p = std.NewImmutable(Person{name: std.NewImmutable("Alice"), age: std.NewImmutable(30)})
var p2 = std.NewImmutable(Person{name: std.Copy(p.Get().name), age: std.NewImmutable(31)})
`,
		},
		{
			name: "Struct Copy with multiple overrides",
			input: `package main

struct Person(name string, age int)
val p = Person("Alice", 30)
val p2 = p.Copy(age = 31, name = "Bob")`,
			expected: `package main

import "martianoff/gala/std"

type Person struct {
	name std.Immutable[string]
	age  std.Immutable[int]
}

func (s Person) Copy() Person {
	return Person{name: std.Copy(s.name), age: std.Copy(s.age)}
}
func (s Person) Equal(other Person) bool {
	return std.Equal(s.name, other.name) && std.Equal(s.age, other.age)
}

var p = std.NewImmutable(Person{name: std.NewImmutable("Alice"), age: std.NewImmutable(30)})
var p2 = std.NewImmutable(func(_ Person) Person {
	return Person{name: std.NewImmutable("Bob"), age: std.NewImmutable(31)}
}(p.Get()))
`,
		},
		{
			name: "Copy without overrides",
			input: `package main

struct Person(name string)
val p = Person("Alice")
val p2 = p.Copy()`,
			expected: `package main

import "martianoff/gala/std"

type Person struct {
	name std.Immutable[string]
}

func (s Person) Copy() Person {
	return Person{name: std.Copy(s.name)}
}
func (s Person) Equal(other Person) bool {
	return std.Equal(s.name, other.name)
}

var p = std.NewImmutable(Person{name: std.NewImmutable("Alice")})
var p2 = std.NewImmutable(p.Get().Copy())
`,
		},
		{
			// Regression: a chained struct-field-access receiver
			// (`o.Tab.Copy(...)`) used to fail with
			// "cannot use Copy overrides: type of receiver unknown"
			// because the receiver-type inferencer only handled bare
			// identifiers and `.Get()` chains. The fix routes through
			// the general expression-type inferencer so any receiver
			// shape that resolves to a struct type works.
			name: "Copy on chained struct-field access",
			input: `package main

struct Inner(Selected int)
struct Outer(Tab Inner)

func selectChained(o Outer, idx int) Inner = o.Tab.Copy(Selected = idx)`,
			expected: `package main

import "martianoff/gala/std"

type Inner struct {
	Selected std.Immutable[int]
}

func (s Inner) Copy() Inner {
	return Inner{Selected: std.Copy(s.Selected)}
}
func (s Inner) Equal(other Inner) bool {
	return std.Equal(s.Selected, other.Selected)
}
func (s Inner) Unapply(v any) (std.Immutable[int], bool) {
	if p, ok := v.(Inner); ok {
		return p.Selected, true
	}
	if p, ok := v.(*Inner); ok && p != nil {
		return p.Selected, true
	}
	return *new(std.Immutable[int]), false
}

type Outer struct {
	Tab std.Immutable[Inner]
}

func (s Outer) Copy() Outer {
	return Outer{Tab: std.Copy(s.Tab)}
}
func (s Outer) Equal(other Outer) bool {
	return std.Equal(s.Tab, other.Tab)
}
func (s Outer) Unapply(v any) (std.Immutable[Inner], bool) {
	if p, ok := v.(Outer); ok {
		return p.Tab, true
	}
	if p, ok := v.(*Outer); ok && p != nil {
		return p.Tab, true
	}
	return *new(std.Immutable[Inner]), false
}
func selectChained(o Outer, idx int) Inner {
	return func(_ Inner) Inner {
		return Inner{Selected: std.NewImmutable(idx)}
	}(o.Tab.Get())
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(stripGeneratedHeader(got)))
		})
	}
}

func TestCopyOverridesErrors(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name          string
		input         string
		expectedError string
	}{
		{
			name: "Override on non-struct type",
			input: `package main

val x = 1
val y = x.Copy(value = 2)`,
			expectedError: "Copy overrides only supported for struct types",
		},
		{
			name: "Override non-existent field",
			input: `package main

struct Person(name string)
val p = Person("Alice")
val p2 = p.Copy(age = 30)`,
			expectedError: "struct Person has no field age",
		},
		{
			name: "Unnamed override",
			input: `package main

struct Person(name string)
val p = Person("Alice")
val p2 = p.Copy("Bob")`,
			expectedError: "Copy overrides must be named: Copy(field = value)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

// TestPackageQualifiedCopyIsNotStructCopy guards against a regression
// where `io.Copy(dst, src)` was misrouted into the struct-Copy
// short-circuit, which then errored with "cannot use Copy overrides:
// type of receiver unknown" because the receiver "io" is a package,
// not a struct value. The dispatcher must skip the short-circuit when
// the receiver is a known imported package and let regular
// package-qualified function dispatch handle the call.
func TestPackageQualifiedCopyIsNotStructCopy(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

import (
    "io"
    "os"
)

func copyStream(src string, dst string) {
    var srcF, _ = os.Open(src)
    var dstF, _ = os.Create(dst)
    val _, _ = io.Copy(dstF, srcF)
}`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "io.Copy(dstF, srcF)",
		"expected io.Copy(...) to transpile as a regular package call, not a struct-Copy override")
}

// TestCopyOverridesGenericReceiver guards the result type of an inlined
// `recv.Copy(field = value)` on a generic struct. Copy cannot change field
// types, so the composite literal must carry the receiver's type arguments;
// a bare `Box{...}` is "cannot use generic type Box[T any] without
// instantiation" in Go, and it also made if/match arms disagree ('Box' vs
// 'Box[T]').
func TestCopyOverridesGenericReceiver(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	const decls = `package main

struct Box[T any](Item T, N int)
struct Entry[K comparable, V any](Key K, Value V, Hits int)
`
	tests := []struct {
		name  string
		body  string
		wants []string
	}{
		{
			name:  "concrete receiver bound to a val",
			body:  `val c = Box[string](Item = "x", N = 1).Copy(N = 5)`,
			wants: []string{"Box[string]{Item: std.Copy(", "N: std.NewImmutable(5)}"},
		},
		{
			name:  "type-parameter receiver in a generic function",
			body:  `func bump[T any](b Box[T]) Box[T] = b.Copy(N = b.N + 1)`,
			wants: []string{"return Box[T]{Item: std.Copy(b.Item)"},
		},
		{
			name:  "if-expression arm",
			body:  `func pick[T any](b Box[T], up bool) Box[T] = if (up) b.Copy(N = b.N + 1) else b`,
			wants: []string{"return Box[T]{Item: std.Copy(b.Item)"},
		},
		{
			name: "match arm",
			body: `sealed type Ev {
    case Inc()
    case Keep()
}
func step[T any](b Box[T], ev Ev) Box[T] = ev match {
    case Inc()  => b.Copy(N = b.N + 1)
    case Keep() => b
}`,
			wants: []string{"return Box[T]{Item: std.Copy(b.Item)"},
		},
		{
			name:  "multi-parameter generic",
			body:  `func hit[K comparable, V any](e Entry[K, V]) Entry[K, V] = e.Copy(Hits = e.Hits + 1)`,
			wants: []string{"return Entry[K, V]{Key: std.Copy(e.Key)"},
		},
		{
			name:  "pointer receiver yields the value type",
			body:  `func viaPtr[T any](b *Box[T]) Box[T] = b.Copy(N = 0)`,
			wants: []string{"return Box[T]{Item: std.Copy(b.Item)"},
		},
		{
			name: "ConstPtr receiver copies through Deref",
			body: `func main() {
    val b = Box[int](Item = 1, N = 1)
    val p = &b
    val c = p.Copy(N = 2)
}`,
			wants: []string{"Box[int]{Item: std.Copy(p.Get().Deref().Item)"},
		},
		{
			// The no-override form calls the generated Copy(); its result
			// must still type as the receiver so the Immutable field unwraps.
			name: "field access on a no-override Copy",
			body: `func main() {
    val b = Box[string](Item = "x", N = 1)
    val n = b.Copy().N
}`,
			wants: []string{"b.Get().Copy().N.Get()"},
		},
		{
			// `&v` on a val types as ConstPtr[Box[int]] (not ConstPtr[T]), so
			// a field read through it still unwraps the Immutable field.
			name: "field access through a ConstPtr to a val",
			body: `func main() {
    val b = Box[int](Item = 1, N = 1)
    val p = &b
    val n = p.N
}`,
			wants: []string{"p.Get().Deref().N.Get()"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.wants {
				assert.Contains(t, got, want)
			}
			assert.NotContains(t, got, "Box{")
			assert.NotContains(t, got, "Entry{")
		})
	}
}

// TestCopyOverridesAnyExpression guards that a `.Copy(...)` override accepts
// any expression, not only the forms that parse through the argument rule's
// pattern alternative. A lambda parses as the rule's lambdaExpression
// alternative and used to be rejected with "Copy overrides must be
// expressions". The overridden field's declared type is the expected type, so
// untyped lambda parameters are inferred from it — with the receiver's type
// arguments substituted for a generic struct — exactly as for a named
// constructor argument.
func TestCopyOverridesAnyExpression(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	const decls = `package main

struct Box(N int, F func() int)
struct Calc(Op func(int) int, Pair func(int, string) string, OnEvent func(string))
struct Cell[T any](Value T, Map func(T) T)
`
	tests := []struct {
		name  string
		body  string
		wants []string
	}{
		{
			name:  "zero-argument lambda",
			body:  `func f(b Box) Box = b.Copy(F = () => 2)`,
			wants: []string{"F: std.NewImmutable(func() int {\n\t\treturn 2\n\t})"},
		},
		{
			name:  "untyped parameters take the field's parameter types",
			body:  `func f(c Calc) Calc = c.Copy(Op = (x) => x + 1, Pair = (n, s) => s)`,
			wants: []string{"Op: std.NewImmutable(func(x int) int {", "Pair: std.NewImmutable(func(n int, s string) string {"},
		},
		{
			name: "block body",
			body: `func f(c Calc) Calc = c.Copy(Op = (x) => {
    val y = x * 2
    return y + 1
})`,
			wants: []string{"Op: std.NewImmutable(func(x int) int {"},
		},
		{
			name:  "void lambda",
			body:  `func f(c Calc) Calc = c.Copy(OnEvent = (e) => { Println(e) })`,
			wants: []string{"OnEvent: std.NewImmutable(func(e string) {"},
		},
		{
			name:  "if-expression",
			body:  `func f(b Box, up bool) Box = b.Copy(N = if (up) 1 else 2)`,
			wants: []string{"N: std.NewImmutable(func() int {"},
		},
		{
			name: "match expression",
			body: `func f(b Box) Box = b.Copy(N = b.N match {
    case 1 => 10
    case _ => 0
})`,
			wants: []string{"N: std.NewImmutable(func(obj int) int {"},
		},
		{
			name:  "by-name sugar lifts a plain expression into the func field",
			body:  `func f(b Box) Box = b.Copy(F = b.N + 1)`,
			wants: []string{"F: std.NewImmutable(func() int {\n\t\treturn b.N.Get() + 1\n\t})"},
		},
		{
			name:  "generic struct with a type-parameter receiver",
			body:  `func f[T any](c Cell[T]) Cell[T] = c.Copy(Map = (v) => c.Map(v))`,
			wants: []string{"Map: std.NewImmutable(func(v T) T {"},
		},
		{
			name:  "generic struct with a concrete receiver",
			body:  `func f(c Cell[string]) Cell[string] = c.Copy(Map = (v) => v + "!")`,
			wants: []string{"Cell[string]{Value: std.Copy(c.Value), Map: std.NewImmutable(func(v string) string {"},
		},
		{
			name:  "pointer receiver",
			body:  `func f(c *Cell[int]) Cell[int] = c.Copy(Map = (v) => v * 2)`,
			wants: []string{"Map: std.NewImmutable(func(v int) int {"},
		},
		{
			name: "ConstPtr receiver",
			body: `func main() {
    val c = Cell[int](Value = 1, Map = (v) => v)
    val p = &c
    val d = p.Copy(Map = (v) => v * 2)
}`,
			wants: []string{"Map: std.NewImmutable(func(v int) int {"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.wants {
				assert.Contains(t, got, want)
			}
			// No lambda parameter or result may fall back to `any`. Only the
			// function under test is checked: the generated Unapply takes `v any`.
			body := got[strings.LastIndex(got, "\nfunc "):]
			assert.NotContains(t, body, " any)")
			assert.NotContains(t, body, " any,")
			assert.NotContains(t, body, ") any {")
		})
	}
}

// TestCopyReceiverEvaluatedOnce guards that the receiver of an inlined
// `recv.Copy(...)` is evaluated exactly once. Each kept field reads the
// receiver, so a call receiver used to run once per kept field, and when
// every field was overridden the receiver was dropped: never evaluated, and
// "declared and not used" for a local val. Such receivers are bound once as
// the parameter of an immediately-applied function literal; a plain variable
// path with a kept field keeps the direct composite literal.
func TestCopyReceiverEvaluatedOnce(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	const decls = `package main

struct P(N int, M int)
struct Cell[T any](Value T, Map func(T) T)
func mk() P = P(1, 2)
`
	tests := []struct {
		name    string
		body    string
		wants   []string
		notWant string
	}{
		{
			name: "every field overridden on a local val",
			body: `func main() {
    val p = P(1, 2)
    Println(p.Copy(N = 3, M = 4).N)
}`,
			wants: []string{"func(_ P) P {\n\t\treturn P{N: std.NewImmutable(3), M: std.NewImmutable(4)}\n\t}(p.Get())"},
		},
		{
			name:  "call receiver is bound once",
			body:  `func f() int = mk().Copy(N = 5).M`,
			wants: []string{"func(_tmp_1 P) P {\n\t\treturn P{N: std.NewImmutable(5), M: std.Copy(_tmp_1.M)}\n\t}(mk())"},
		},
		{
			name:  "generic receiver keeps its type arguments",
			body:  `func f[T any](c Cell[T]) Cell[T] = c.Copy(Value = c.Value, Map = (v) => v)`,
			wants: []string{"func(_ Cell[T]) Cell[T] {", "}(c)"},
		},
		{
			name:    "plain variable path with a kept field is not bound",
			body:    `func f(p P) P = p.Copy(N = 5)`,
			wants:   []string{"return P{N: std.NewImmutable(5), M: std.Copy(p.M)}"},
			notWant: "_tmp_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.wants {
				assert.Contains(t, got, want)
			}
			if tt.notWant != "" {
				assert.NotContains(t, got, tt.notWant)
			}
		})
	}
}
