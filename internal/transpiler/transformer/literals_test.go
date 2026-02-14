package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

func TestLiteralRestrictions(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name         string
		input        string
		expectError  bool
		errorContain string
	}{
		{
			name: "slice literal should fail",
			input: `package main

func main() {
    val x = []int{1, 2, 3}
}`,
			expectError:  true,
			errorContain: "slice literals are not supported",
		},
		{
			name: "map literal should fail",
			input: `package main

func main() {
    val x = map[string]int{"a": 1, "b": 2}
}`,
			expectError:  true,
			errorContain: "map literals are not supported",
		},
		{
			name: "empty map literal should fail",
			input: `package main

func main() {
    val x = map[string]int{}
}`,
			expectError:  true,
			errorContain: "map literals are not supported",
		},
		{
			name: "map type in function signature is allowed",
			input: `package main

func process(m map[string]int) map[string]int {
    return m
}`,
			expectError: false,
		},
		{
			name: "map type in variable declaration is allowed",
			input: `package main

var m map[string]int`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContain != "" && err != nil {
					assert.Contains(t, err.Error(), tt.errorContain)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestQualifiedImportMethodCall(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "basic qualified call",
			input: `package main

import (
    "fmt"
    cm "martianoff/gala/collection_mutable"
)

func main() {
    var parts = cm.EmptyArray[string]()
    parts.Append("hello")
    for i := 0; i < parts.Length(); i++ {
        fmt.Println(parts.Get(i))
    }
}`,
		},
		{
			name: "qualified type in struct field with method call",
			input: `package main

import (
    "fmt"
    cm "martianoff/gala/collection_mutable"
)

type Builder struct {
    var parts *cm.Array[string]
}

func NewBuilder() *Builder = &Builder{parts: cm.EmptyArray[string]()}

func (b *Builder) Add(s string) {
    b.parts.Append(s)
}

func (b *Builder) Build() string {
    var result = ""
    for i := 0; i < b.parts.Length(); i++ {
        result = result + b.parts.Get(i)
    }
    return result
}

func main() {
    val b = NewBuilder()
    b.Add("hello")
    b.Add(" world")
    fmt.Println(b.Build())
}`,
		},
		{
			name: "qualified generic type with local type param",
			input: `package main

import (
    "fmt"
    cm "martianoff/gala/collection_mutable"
)

type Wrapper struct {
    val value string
}

type Builder struct {
    var parts *cm.Array[Wrapper]
}

func NewBuilder() *Builder = &Builder{parts: cm.EmptyArray[Wrapper]()}

func (b *Builder) Add(w Wrapper) {
    b.parts.Append(w)
}

func (b *Builder) Build() string {
    var result = ""
    for i := 0; i < b.parts.Length(); i++ {
        result = result + b.parts.Get(i).value
    }
    return result
}

func main() {
    val b = NewBuilder()
    b.Add(Wrapper{value: "hello"})
    b.Add(Wrapper{value: " world"})
    fmt.Println(b.Build())
}`,
		},
		{
			name: "qualified generic with dot imports (string_utils pattern)",
			input: `package string_utils

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
    cm "martianoff/gala/collection_mutable"
)

type Str struct {
    runes Array[rune]
}

func S(s string) Str = Str{runes: EmptyArray[rune]()}

type StringBuilder struct {
    var parts *cm.Array[Str]
}

func NewStringBuilder() *StringBuilder = &StringBuilder{parts: cm.EmptyArray[Str]()}

func (sb *StringBuilder) AppendStr(s Str) {
    sb.parts.Append(s)
}

func (sb *StringBuilder) ToString() string {
    var result = ""
    for i := 0; i < sb.parts.Length(); i++ {
        result = result + sb.parts.Get(i).runes.Length()
    }
    return result
}

func (sb *StringBuilder) Reset() {
    sb.parts = cm.EmptyArray[Str]()
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation failed")
			t.Logf("Generated:\n%s", result)
		})
	}
}

func TestCharAndRawStringLiterals(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		expectError bool
		contains    string
	}{
		{
			name: "char literal simple",
			input: `package main

func main() {
    val c = 'a'
}`,
			expectError: false,
			contains:    "'a'",
		},
		{
			name: "char literal with escape",
			input: `package main

func main() {
    val c = '\n'
}`,
			expectError: false,
			contains:    `'\n'`,
		},
		{
			name: "raw string literal",
			input: "package main\n\nfunc main() {\n    val s = `hello raw`\n}",
			expectError: false,
			contains:    "`hello raw`",
		},
		{
			name: "multi-line raw string",
			input: "package main\n\nfunc main() {\n    val s = `line one\nline two`\n}",
			expectError: false,
			contains:    "`line one\nline two`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.Transpile(tt.input, "")
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.contains != "" {
					assert.Contains(t, result, tt.contains)
				}
			}
		})
	}
}
