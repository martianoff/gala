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
var p2 = std.NewImmutable(Person{name: std.NewImmutable("Bob"), age: std.NewImmutable(31)})
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
	return Inner{Selected: std.NewImmutable(idx)}
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
