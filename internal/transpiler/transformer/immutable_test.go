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

func TestImmutable(t *testing.T) {
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
			name: "val variable and usage",
			input: `package main

val x = 10
val y = x + 1
`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var y = std.NewImmutable(x.Get() + 1)
`,
		},
		{
			name: "val parameter usage",
			input: `package main

func f(val x int) int = x + 1`,
			expected: `package main

import "martianoff/gala/std"

func f(x std.Immutable[int]) int {
	return x.Get() + 1
}
`,
		},
		{
			name: "val struct field and usage",
			input: `package main

type Config struct {
	val ID string
}
func getID(c Config) string = c.ID`,
			expected: `package main

import "martianoff/gala/std"

type Config struct {
	ID std.Immutable[string]
}

func (s Config) Copy() Config {
	return Config{ID: std.Copy(s.ID)}
}
func (s Config) Equal(other Config) bool {
	return std.Equal(s.ID, other.ID)
}
func (s Config) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(Config); ok {
		return p.ID, true
	}
	if p, ok := v.(*Config); ok && p != nil {
		return p.ID, true
	}
	return *new(std.Immutable[string]), false
}
func getID(c Config) string {
	return c.ID.Get()
}
`,
		},
		{
			name: "local var with immutable field access",
			input: `package main

type Node struct {
	value int
	isEmpty bool
}

func test(n Node) bool {
	var local = n
	return local.isEmpty
}`,
			expected: `package main

import "martianoff/gala/std"

type Node struct {
	value   std.Immutable[int]
	isEmpty std.Immutable[bool]
}

func (s Node) Copy() Node {
	return Node{value: std.Copy(s.value), isEmpty: std.Copy(s.isEmpty)}
}
func (s Node) Equal(other Node) bool {
	return std.Equal(s.value, other.value) && std.Equal(s.isEmpty, other.isEmpty)
}
func test(n Node) bool {
	var local = n
	return local.isEmpty.Get()
}
`,
		},
		{
			name: "generic type with local var immutable field access",
			input: `package main

type Container[T any] struct {
	value T
	isEmpty bool
}

func testEmpty[T any](c Container[T]) bool {
	var local = c
	return local.isEmpty
}`,
			expected: `package main

import "martianoff/gala/std"

type Container[T any] struct {
	value   std.Immutable[T]
	isEmpty std.Immutable[bool]
}

func (s Container[T]) Copy() Container[T] {
	return Container[T]{value: std.Copy(s.value), isEmpty: std.Copy(s.isEmpty)}
}
func (s Container[T]) Equal(other Container[T]) bool {
	return std.Equal(s.value, other.value) && std.Equal(s.isEmpty, other.isEmpty)
}

type ContainerInstance interface {
	IsContainer() bool
}

func (_ Container[T]) IsContainer() bool {
	return true
}
func testEmpty[T any](c Container[T]) bool {
	var local = c
	return local.isEmpty.Get()
}
`,
		},
		{
			name: "pointer dereference with immutable field access",
			input: `package main

type Node struct {
	value int
	next *Node
	isEmpty bool
}

func test(n Node) bool {
	var current = n
	var next = *current.next
	return next.isEmpty
}`,
			expected: `package main

import "martianoff/gala/std"

type Node struct {
	value   std.Immutable[int]
	next    std.Immutable[*Node]
	isEmpty std.Immutable[bool]
}

func (s Node) Copy() Node {
	return Node{value: std.Copy(s.value), next: std.Copy(s.next), isEmpty: std.Copy(s.isEmpty)}
}
func (s Node) Equal(other Node) bool {
	return std.Equal(s.value, other.value) && std.Equal(s.next, other.next) && std.Equal(s.isEmpty, other.isEmpty)
}
func test(n Node) bool {
	var current = n
	var next = *current.next.Get()
	return next.isEmpty.Get()
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

func TestPointerToValModification(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Test: Modifying through pointer to val should be an error
	// since it would break immutability guarantees
	input := `package main

func main() {
    val data = 42
    val ptr = &data
    *ptr = 100
}`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot assign")
}
