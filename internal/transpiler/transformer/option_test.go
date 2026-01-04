package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOption(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name: "Some and None expressions",
			input: `package main

val x = Some(10)
val y Option[int] = None()`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(std.Some(10))
var y std.Immutable[std.Option[int]] = std.NewImmutable(std.None())
`,
		},
		{
			name: "Option type in val declaration",
			input: `package main

val x Option[int] = Some(10)`,
			expected: `package main

import "martianoff/gala/std"

var x std.Immutable[std.Option[int]] = std.NewImmutable(std.Some(10))
`,
		},
		{
			name: "Option Map and FlatMap",
			input: `package main

val x = Some(10)
val y = x.Map((v int) => v * 2)
val z = y.FlatMap((v int) => Some(v + 1))`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(std.Some(10))
var y = std.NewImmutable(std.Map(x.Get(), func(v int) any {
	return v * 2
}))
var z = std.NewImmutable(std.FlatMap(y.Get(), func(v int) any {
	return std.Some(v + 1)
}))
`,
		},
		{
			name: "Option ForEach",
			input: `package main

val x = Some(10)
func test() {
    x.ForEach((v int) => {
        val y = v * 2
    })
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(std.Some(10))

func test() {
	std.ForEach(x.Get(), func(v int) any {
		var y = std.NewImmutable(v * 2)
		return nil
	})
}
`,
		},
		{
			name: "Option with mutable variable",
			input: `package main

var o Option[int] = None()
func update() {
    o = Some(42)
}`,
			expected: `package main

import "martianoff/gala/std"

var o std.Option[int] = std.None()

func update() {
	o = std.Some(42)
}
`,
		},
		{
			name: "None() without explicit type in val",
			input: `package main

val x = None()`,
			wantErr: true,
		},
		{
			name: "None() without explicit type in var",
			input: `package main

var x = None()`,
			wantErr: true,
		},
		{
			name: "None() in short var decl",
			input: `package main

func main() {
    x := None()
}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "variable assigned to None() must have an explicit type")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
