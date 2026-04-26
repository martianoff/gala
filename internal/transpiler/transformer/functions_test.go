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

func TestFunctions(t *testing.T) {
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
			name: "Lambda with explicit return nil should not duplicate return",
			input: `package main

import . "martianoff/gala/std"

func test() {
    var arr = EmptyArray[int]()
    arr.ForEach((x int) => {
        println(x)
        return nil
    })
}`,
			expected: `package main

import . "martianoff/gala/std"

func test() {
	var arr = EmptyArray[int]()
	arr.ForEach(func(x int) {
		println(x)
	})
}
`,
		},
		{
			name: "Standard Go-style function",
			input: `package main

func add(a int, b int) int { return a + b }`,
			expected: `package main

func add(a int, b int) int {
	return a + b
}
`,
		},
		{
			name: "Scala-style shorthand function",
			input: `package main

func square(x int) int = x * x`,
			expected: `package main

func square(x int) int {
	return x * x
}
`,
		},
		{
			name: "Lambda expression",
			input: `package main

val f = (x int) => x * x`,
			expected: `package main

import "martianoff/gala/std"

var f = std.NewImmutable(func(x int) int {
	return x * x
})
`,
		},
		{
			// When HM-based inference cannot resolve the branches' types
			// (here `c` is undeclared), per-branch fallback inference picks
			// the common arm type — both arms are int literals, so the IIFE
			// is typed `func() int` rather than the legacy `func() any`.
			name: "If expression",
			input: `package main

val res = if (c) 1 else 2`,
			expected: `package main

import "martianoff/gala/std"

var res = std.NewImmutable(func() int {
	if c {
		return 1
	} else {
		return 2
	}
}())
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
