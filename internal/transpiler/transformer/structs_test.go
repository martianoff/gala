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

func TestStructs(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{"."})
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Simple struct",
			input: `package main

type Person struct {
	Name string
	Age  int
}`,
			expected: `package main

import "martianoff/gala/std"

type Person struct {
	Name std.Immutable[string]
	Age  std.Immutable[int]
}

func (s Person) Copy() Person {
	return Person{Name: std.Copy(s.Name), Age: std.Copy(s.Age)}
}
func (s Person) Equal(other Person) bool {
	return std.Equal(s.Name, other.Name) && std.Equal(s.Age, other.Age)
}
func (s Person) Unapply(v any) (std.Immutable[string], std.Immutable[int], bool) {
	if p, ok := v.(Person); ok {
		return p.Name, p.Age, true
	}
	if p, ok := v.(*Person); ok && p != nil {
		return p.Name, p.Age, true
	}
	return *new(std.Immutable[string]), *new(std.Immutable[int]), false
}
`,
		},
		{
			name: "Struct with val and var fields",
			input: `package main

type Config struct {
	val ID string
	var Count int
}`,
			expected: `package main

import "martianoff/gala/std"

type Config struct {
	ID    std.Immutable[string]
	Count int
}

func (s Config) Copy() Config {
	return Config{ID: std.Copy(s.ID), Count: std.Copy(s.Count)}
}
func (s Config) Equal(other Config) bool {
	return std.Equal(s.ID, other.ID) && std.Equal(s.Count, other.Count)
}
func (s Config) Unapply(v any) (std.Immutable[string], int, bool) {
	if p, ok := v.(Config); ok {
		return p.ID, p.Count, true
	}
	if p, ok := v.(*Config); ok && p != nil {
		return p.ID, p.Count, true
	}
	return *new(std.Immutable[string]), *new(int), false
}
`,
		},
		{
			name: "Struct with tags",
			input: `package main

type User struct {
	Name string "json:\"name\""
}`,
			expected: `package main

import "martianoff/gala/std"

type User struct {
	Name std.Immutable[string] "json:\"name\""
}

func (s User) Copy() User {
	return User{Name: std.Copy(s.Name)}
}
func (s User) Equal(other User) bool {
	return std.Equal(s.Name, other.Name)
}
func (s User) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(User); ok {
		return p.Name, true
	}
	if p, ok := v.(*User); ok && p != nil {
		return p.Name, true
	}
	return *new(std.Immutable[string]), false
}
`,
		},
		{
			name: "Shorthand struct with named arguments",
			input: `package main

struct Person(name string, age int)
val p = Person(age = 30, name = "Alice")`,
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
