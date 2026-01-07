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

func TestApply(t *testing.T) {
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
			name: "Apply method on instance",
			input: `package main

type Append struct { Name string }
func (a Append) Apply(param string) string = param + a.Name
func main() {
    val a = Append("cherry")
    val res = a("apple")
}`,
			expected: `package main

import "martianoff/gala/std"

type Append struct {
	Name std.Immutable[string]
}

func (s Append) Copy() Append {
	return Append{Name: std.Copy(s.Name)}
}
func (s Append) Equal(other Append) bool {
	return std.Equal(s.Name, other.Name)
}
func (s Append) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(Append); ok {
		return p.Name, true
	}
	if p, ok := v.(*Append); ok && p != nil {
		return p.Name, true
	}
	return *new(std.Immutable[string]), false
}
func (a Append) Apply(param string) string {
	return param + a.Name.Get()
}
func main() {
	var a = std.NewImmutable(Append{Name: std.NewImmutable("cherry")})
	var res = std.NewImmutable(a.Get().Apply("apple"))
}`,
		},
		{
			name: "Apply method on expression",
			input: `package main

struct Append(Name string)
func (a Append) Apply(param string) string = param + a.Name
func main() {
    val res = Append("cherry")("apple")
}`,
			expected: `package main

import "martianoff/gala/std"

type Append struct {
	Name std.Immutable[string]
}

func (s Append) Copy() Append {
	return Append{Name: std.Copy(s.Name)}
}
func (s Append) Equal(other Append) bool {
	return std.Equal(s.Name, other.Name)
}
func (s Append) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(Append); ok {
		return p.Name, true
	}
	if p, ok := v.(*Append); ok && p != nil {
		return p.Name, true
	}
	return *new(std.Immutable[string]), false
}
func (a Append) Apply(param string) string {
	return param + a.Name.Get()
}
func main() {
	var res = std.NewImmutable(Append{Name: std.NewImmutable("cherry")}.Apply("apple"))
}`,
		},
		{
			name: "Struct without properties used without parentheses",
			input: `package main

type Implode struct {}
func (i Implode) Apply(param string) string = param + "!"
func main() {
    val res = Implode("apple")
}`,
			expected: `package main

import "martianoff/gala/std"

type Implode struct {
}

func (s Implode) Copy() Implode {
	return Implode{}
}
func (s Implode) Equal(other Implode) bool {
	return true
}
func (s Implode) Unapply(v any) bool {
	if _, ok := v.(Implode); ok {
		return true
	}
	if _, ok := v.(*Implode); ok {
		return true
	}
	return false
}
func (i Implode) Apply(param string) string {
	return param + "!"
}
func main() {
	var res = std.NewImmutable(Implode{}.Apply("apple"))
}`,
		},
		{
			name: "Generic struct with Apply method",
			input: `package main

type Identity[T any] struct {}
func (i Identity[T]) Apply(v T) T = v
func main() {
    val res = Identity[int](10)
}`,
			expected: `package main

import "martianoff/gala/std"

type Identity[T any] struct {
}

func (s Identity[T]) Copy() Identity[T] {
	return Identity[T]{}
}
func (s Identity[T]) Equal(other Identity[T]) bool {
	return true
}
func (s Identity[T]) Unapply(v any) bool {
	if _, ok := v.(Identity[T]); ok {
		return true
	}
	if _, ok := v.(*Identity[T]); ok {
		return true
	}
	return false
}
func (i Identity[T]) Apply(v T) T {
	return v
}
func main() {
	var res = std.NewImmutable(Identity[int]{}.Apply(10))
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			if err != nil {
				t.Fatalf("Transpile failed: %v", err)
			}
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
