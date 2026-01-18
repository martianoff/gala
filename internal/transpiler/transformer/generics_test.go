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

func TestGenerics(t *testing.T) {
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
			name: "Generic function",
			input: `package main

func identity[T any](x T) T { return x }`,
			expected: `package main

func identity[T any](x T) T {
	return x
}
`,
		},
		{
			name: "Generic struct",
			input: `package main

type Box[T any] struct {
	Value T
}`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value std.Immutable[T]
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}
`,
		},
		{
			name: "Generic struct field usage",
			input: `package main

type Box[T any] struct {
	Value T
}
func getValue[T any](b Box[T]) T = b.Value`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value std.Immutable[T]
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}
func getValue[T any](b Box[T]) T {
	return b.Value.Get()
}
`,
		},
		{
			name: "Generic struct with immutable field",
			input: `package main

type Box[T any] struct {
	val Value T
}`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value std.Immutable[T]
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}
`,
		},
		{
			name: "Generic struct with mutable field",
			input: `package main

type Box[T any] struct {
	var Value T
}`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value T
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (T, bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(T), false
}
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
