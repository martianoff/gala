package transformer_test

import (
	"fmt"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTupleFieldUnwrapRepro(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		check    func(t *testing.T, got string)
	}{
		{
			name: "simple getPair return",
			input: `package main

import "fmt"

func getPair() Tuple[string, int] = ("hello", 42)

func main() {
    val pair = getPair()
    fmt.Println(pair.V1)
    fmt.Println(pair.V2)
}
`,
			check: func(t *testing.T, got string) {
				assert.True(t, strings.Contains(got, "pair.Get().V1.Get()"), "pair.V1 should unwrap to pair.Get().V1.Get(), got:\n%s", got)
			},
		},
		{
			name: "tuple literal field access",
			input: `package main

import "fmt"

func main() {
    val pair = ("hello", 42)
    fmt.Println(pair.V1)
    fmt.Println(pair.V2)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple literal field access ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "pair.Get().V1.Get()"), "pair.V1 should unwrap to pair.Get().V1.Get(), got:\n%s", got)
			},
		},
		{
			name: "tuple field access in expression",
			input: `package main

import "fmt"

func main() {
    val pair = (10, 20)
    val sum = pair.V1 + pair.V2
    fmt.Println(sum)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple field access in expression ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "V1.Get()"), "pair.V1 should have .Get() unwrap, got:\n%s", got)
				assert.True(t, strings.Contains(got, "V2.Get()"), "pair.V2 should have .Get() unwrap, got:\n%s", got)
			},
		},
		{
			name: "var tuple field access",
			input: `package main

import "fmt"

func main() {
    var pair = ("hello", 42)
    fmt.Println(pair.V1)
    fmt.Println(pair.V2)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== var tuple field access ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "V1.Get()"), "pair.V1 should have .Get() unwrap, got:\n%s", got)
			},
		},
		{
			name: "function param tuple field access",
			input: `package main

import "fmt"

func useTuple(pair Tuple[string, int]) {
    fmt.Println(pair.V1)
    fmt.Println(pair.V2)
}

func main() {
    useTuple(("hello", 42))
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== function param tuple field access ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "V1.Get()"), "pair.V1 should have .Get() unwrap, got:\n%s", got)
			},
		},
		{
			name: "tuple from option.Get field access",
			input: `package main

import "fmt"

func main() {
    val opt = Some(("hello", 42))
    val pair = opt.Get()
    fmt.Println(pair.V1)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple from option.Get field access ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "V1.Get()"), "pair.V1 should have .Get() unwrap, got:\n%s", got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			tt.check(t, got)
		})
	}
}

func TestTupleTypeWidening(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		check    func(t *testing.T, got string)
	}{
		{
			name: "tuple return type with val string",
			input: `package main

func parse(url string) Tuple[string, string] {
    val path = "/test"
    val query = "q=1"
    return (path, query)
}

func main() {
    val result = parse("/test?q=1")
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple return type with val string ===")
				fmt.Println(got)
				// Should NOT contain "any" - both elements should be string
				assert.False(t, strings.Contains(got, "Tuple[any"), "should not widen to any, got:\n%s", got)
				assert.True(t, strings.Contains(got, "Tuple[string, string]"), "should preserve Tuple[string, string], got:\n%s", got)
			},
		},
		{
			name: "tuple return type with val and complex type - no import",
			input: `package main

func parse(url string) Tuple[string, HashMap[string, string]] {
    val path = "/test"
    val params = EmptyHashMap[string, string]()
    return (path, params)
}

func main() {
    val result = parse("/test")
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple return type with val and complex type - no import ===")
				fmt.Println(got)
				// Even without import, the return type context should prevent widening to any
				assert.False(t, strings.Contains(got, "Tuple[string, any]"), "should not widen to Tuple[string, any] when return type is declared, got:\n%s", got)
			},
		},
		{
			name: "tuple return type with val and complex type - with import",
			input: `package main

import . "martianoff/gala/collection_immutable"

func parse(url string) Tuple[string, HashMap[string, string]] {
    val path = "/test"
    val params = EmptyHashMap[string, string]()
    return (path, params)
}

func main() {
    val result = parse("/test")
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple return type with val and complex type - with import ===")
				fmt.Println(got)
				// No param should be widened to "any"
				assert.False(t, strings.Contains(got, "Tuple[string, any]"), "should not widen second param to any, got:\n%s", got)
			},
		},
		{
			name: "tuple with HashMap val element - no import",
			input: `package main

func main() {
    val params = EmptyHashMap[string, string]()
    val pair = ("/test", params)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with HashMap val element - no import ===")
				fmt.Println(got)
				// Without proper import, HashMap type can't be resolved - this is expected to widen
			},
		},
		{
			name: "tuple with HashMap val element - dot import",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val params = EmptyHashMap[string, string]()
    val pair = ("/test", params)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with HashMap val element - dot import ===")
				fmt.Println(got)
				assert.False(t, strings.Contains(got, "Tuple[string, any]"), "should not widen HashMap to any, got:\n%s", got)
			},
		},
		{
			name: "tuple with val string and val int",
			input: `package main

func main() {
    val name = "hello"
    val age = 42
    val pair = (name, age)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with val string and val int ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "Tuple[string, int]"), "should be Tuple[string, int], got:\n%s", got)
			},
		},
		{
			name: "tuple with Option val element",
			input: `package main

func main() {
    val opt = Some(42)
    val pair = ("hello", opt)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with Option val element ===")
				fmt.Println(got)
				// Option type should be correctly inferred from std
				assert.False(t, strings.Contains(got, "Tuple[string, any]"), "should not widen Option to any, got:\n%s", got)
			},
		},
		{
			name: "tuple with function result val",
			input: `package main

func extractPath(url string) string = url

func main() {
    val path = extractPath("/test")
    val query = "q=1"
    val pair = (path, query)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with function result val ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "Tuple[string, string]"), "should be Tuple[string, string], got:\n%s", got)
			},
		},
		{
			name: "tuple with external function result val",
			input: `package main

import "fmt"

func main() {
    val path = fmt.Sprintf("/test/%s", "foo")
    val query = "q=1"
    val pair = (path, query)
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== tuple with external function result val ===")
				fmt.Println(got)
				assert.True(t, strings.Contains(got, "Tuple[string, string]"), "should be Tuple[string, string], got:\n%s", got)
			},
		},
		{
			name: "simple string tuple in return",
			input: `package main

func parse() Tuple[string, string] {
    val path = "/test"
    val query = "q=1"
    return (path, query)
}

func main() {
    val r = parse()
}
`,
			check: func(t *testing.T, got string) {
				fmt.Println("=== simple string tuple in return ===")
				fmt.Println(got)
				assert.False(t, strings.Contains(got, "[any"), "should not widen any param to any, got:\n%s", got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			tt.check(t, got)
		})
	}
}
