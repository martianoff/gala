package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDefaultsTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestStructFieldDefaultsLowering covers the positive half of GALA-E0045: a
// shorthand struct field declared with `= value` must reach the emitted
// composite literal when the construction omits it. Before this, the default
// was parsed and discarded and the field took Go's zero value.
func TestStructFieldDefaultsLowering(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			name: "named construction fills omitted defaults",
			input: `package main

struct Cfg(Name string, Mask rune = '*', Tries int = 3)

func main() {
    val a = Cfg(Name = "a")
    Println(a.Name, a.Mask, a.Tries)
}`,
			contains: []string{"Mask:", "'*'", "Tries:", "3"},
		},
		{
			name: "positional construction fills omitted defaults",
			input: `package main

struct Cfg(Name string, Mask rune = '*', Tries int = 3)

func main() {
    val a = Cfg("a")
    Println(a.Name, a.Mask, a.Tries)
}`,
			contains: []string{"Mask:", "'*'", "Tries:", "3"},
		},
		{
			name: "explicit values win over defaults",
			input: `package main

struct Cfg(Name string, Tries int = 3)

func main() {
    val a = Cfg(Name = "a", Tries = 9)
    Println(a.Name, a.Tries)
}`,
			contains: []string{"Tries:", "9"},
		},
		{
			// A Go-style composite literal is not a constructor call. It keeps
			// Go's semantics: partial, and no defaults consulted.
			name: "go-style literal stays partial",
			input: `package main

struct Cfg(Name string, Tries int = 3)

func main() {
    val a = Cfg{Name: "a"}
    Println(a.Name, a.Tries)
}`,
			absent: []string{"Tries:"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "struct_defaults_test.gala")
			require.NoError(t, err)
			// Scope the assertions to main; the generated Copy/Equal/Unapply
			// methods mention every field name unconditionally.
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			for _, notWant := range tc.absent {
				assert.NotContains(t, body, notWant)
			}
		})
	}
}

// TestBlockStructStaysPartial pins the exemption that keeps the standard
// library building. A block-form struct has no syntax for a field default, so
// requiring every field would leave no way to opt out — collection_immutable's
// HAMT nodes set two of four fields and mean the rest to be zero.
func TestBlockStructStaysPartial(t *testing.T) {
	trans := newDefaultsTranspiler()

	input := `package main

type Node struct {
    bitmap  int
    entries string
    isLeaf  bool
}

func main() {
    val n = Node(entries = "x", isLeaf = true)
    Println(n.entries, n.isLeaf)
}`

	out, err := trans.Transpile(input, "struct_defaults_test.gala")
	require.NoError(t, err, "block-form structs must keep partial construction")
	assert.Contains(t, out, "entries:")
}

// TestGoImportedTypeStaysPartial pins the Go-interop boundary. A Go struct can
// only be constructed partially — url.URL has ten fields and callers set two,
// and http.Server and tls.Config are the same shape — so requiring every field
// would make the Go standard library unconstructable.
//
// Both spellings must stay exempt. For a Go type the call form is sugar for the
// composite literal rather than a GALA constructor, so it must not pick up the
// required-field check the shorthand form gets.
func TestGoImportedTypeStaysPartial(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "composite literal over a subset of fields",
			input: `package main

import (
    "net/url"
)

func main() {
    val u = url.URL{Scheme: "https", Host: "example.com"}
    Println(u.String())
}`,
		},
		{
			name: "call syntax with named args over a subset of fields",
			input: `package main

import (
    "net/url"
)

func main() {
    val u = url.URL(Scheme = "https", Host = "example.com")
    Println(u.String())
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "struct_defaults_test.gala")
			require.NoError(t, err, "a Go struct must stay partially constructable")
			assert.Contains(t, out, "url.URL{")
			assert.Contains(t, out, "Scheme:")
			// The eight fields the call site did not name must not be invented.
			assert.NotContains(t, out, "RawQuery:")
		})
	}
}

// TestStructFieldDefaultEvaluatedPerConstruction pins that a default is
// re-parsed and re-transformed at each construction site rather than shared,
// which is what lets `= time.Now()` mean the time of each construction — the
// same contract function parameter defaults have.
func TestStructFieldDefaultEvaluatedPerConstruction(t *testing.T) {
	trans := newDefaultsTranspiler()

	input := `package main

func next() int = 1

struct Cfg(Name string, Seq int = next())

func main() {
    val a = Cfg(Name = "a")
    val b = Cfg(Name = "b")
    Println(a.Seq, b.Seq)
}`

	out, err := trans.Transpile(input, "struct_defaults_test.gala")
	require.NoError(t, err)
	body := out[strings.Index(out, "func main()"):]
	assert.Equal(t, 2, strings.Count(body, "next()"),
		"each construction must emit its own call to the default expression")
}
