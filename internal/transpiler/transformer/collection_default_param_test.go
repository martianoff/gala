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

// TestCollectionDefaultParamLowered verifies that a function parameter whose
// default is an empty generic-collection literal (e.g. `EmptyArray[string]()`,
// `EmptyHashMap[string, string]()`) is filled in at call sites that omit the
// argument. Acts as a regression guard against the emitter regressing to
// special-casing only primitive literal defaults — the call-site lowering
// must accept any expression as a default, including generic-collection
// constructors.
//
// The Go-level proof of correctness is the matching example program at
// examples/collection_default_param.gala which compiles and runs end-to-end.
func TestCollectionDefaultParamLowered(t *testing.T) {
	cases := []struct {
		name string
		// 'src' must yield generated Go that contains the expected substrings.
		src    string
		expect []string
	}{
		{
			name: "function with two collection defaults, partial call",
			src: `package main

import . "martianoff/gala/collection_immutable"

func makeRow(
    name string,
    tags Array[string] = EmptyArray[string](),
    attrs HashMap[string, string] = EmptyHashMap[string, string](),
) string = name

func main() {
    val r = makeRow("alpha")
    Println(r)
}
`,
			expect: []string{
				`makeRow("alpha", EmptyArray[string](), EmptyHashMap[string, string]())`,
			},
		},
		{
			name: "method with collection default",
			src: `package main

import . "martianoff/gala/collection_immutable"

struct Bag(name string)

func (b Bag) Tagged(
    name string,
    tags Array[string] = EmptyArray[string](),
) string = name

func main() {
    val b = Bag("hello")
    Println(b.Tagged("alpha"))
}
`,
			expect: []string{"EmptyArray[string]()"},
		},
		{
			name: "all-default zero-arg call",
			src: `package main

import . "martianoff/gala/collection_immutable"

func makeRow(
    name string = "default",
    tags Array[string] = EmptyArray[string](),
) string = name

func main() {
    val r = makeRow()
    Println(r)
}
`,
			expect: []string{`makeRow("default", EmptyArray[string]())`},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			tr := transformer.NewGalaASTTransformer()
			g := generator.NewGoCodeGenerator()
			trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

			got, err := trans.Transpile(c.src, "")
			assert.NoError(t, err, "expected transpile to succeed")
			if err != nil {
				return
			}
			for _, want := range c.expect {
				if !strings.Contains(got, want) {
					t.Fatalf("expected generated Go to contain %q, got:\n%s", want, got)
				}
			}
		})
	}
}
