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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
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
