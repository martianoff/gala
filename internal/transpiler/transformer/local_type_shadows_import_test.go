package transformer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestLocalTypeShadowsImport covers a bare type name that a hand-written .go
// sibling in the same package declares, beside an import whose package declares
// a type of the same name.
//
// Resolution swept non-dot imports for an UNQUALIFIED name, so `Array` — which
// collection_immutable also declares — was rewritten to
// `collection_immutable.Array` and the local literal's own fields went with it,
// leaving Go that does not compile. A non-dot import binds only its qualifier,
// so a bare name cannot mean the imported type through it: the declaration in
// the current package is the one meant, and the qualified name is how the
// author reaches the import.
//
// The last case is the one that needs no .go sibling: a GALA package named after
// the Go package it imports. That is what rules out GoTypeInfo as the oracle —
// it holds every imported Go package and is keyed by package name, so it cannot
// distinguish a local declaration from an import of the same name.
func TestLocalTypeShadowsImport(t *testing.T) {
	// Hand-written .go files beside each .gala source, declaring an `Array` that
	// collides with the imported collection_immutable.Array. A localGo body of
	// `%s` is preceded by the package clause matching the .gala file's.
	localGo := func(pkgName, decl string) string {
		return fmt.Sprintf("package %s\n\n%s", pkgName, decl)
	}

	cases := []struct {
		name        string
		pkgName     string
		galaName    string
		localGo     string
		src         string
		contains    []string
		notContains []string
	}{
		{
			// The reported shape: a library package's .gala file beside a
			// hand-written .go file of the same package.
			name:     "local struct in a go sibling of a library package",
			pkgName:  "cmds",
			galaName: "use.gala",
			localGo: localGo("cmds", `// The local type. A bare Array in the .gala sibling must mean this one.
type Array struct {
	Held int
}
`),
			src: `package cmds

import "martianoff/gala/collection_immutable"

func Describe() int {
	var a = Array{Held: 3}
	Println(a.Held)
	var b = collection_immutable.ArrayOf(1)
	Println(b)
	return 1
}
`,
			contains: []string{
				"var a = Array{Held: 3}",
				"collection_immutable.ArrayOf(1)",
			},
			notContains: []string{"collection_immutable.Array{"},
		},
		{
			// The same shape in `package main`, whose siblings the analyzer
			// used not to scan at all.
			name:     "local struct in a go sibling of a main package",
			pkgName:  "main",
			galaName: "main.gala",
			localGo: localGo("main", `// The local type. A bare Array in the .gala sibling must mean this one.
type Array struct {
	Held int
}
`),
			src: `package main

import "martianoff/gala/collection_immutable"

func main() {
	var a = Array{Held: 3}
	Println(a.Held)
	var b = collection_immutable.ArrayOf(1)
	Println(b)
}
`,
			contains: []string{
				"var a = Array{Held: 3}",
				"collection_immutable.ArrayOf(1)",
			},
			notContains: []string{"collection_immutable.Array{"},
		},
		{
			// A non-struct local declaration, which no type-metadata synthesis
			// can turn into an entry. What settles it is that the local type is
			// found through the package's own Go declarations, not through
			// typeMetas — which is why this case is not redundant with the two
			// above.
			name:     "local non-struct type in a go sibling",
			pkgName:  "cmds",
			galaName: "use.gala",
			localGo: localGo("cmds", `// A non-struct local type of a name the import also declares.
type Array string
`),
			src: `package cmds

import "martianoff/gala/collection_immutable"

func label(a Array) string = a

func Describe() int {
	Println(label("x"))
	var b = collection_immutable.ArrayOf(1)
	Println(b)
	return 1
}
`,
			contains: []string{
				"func label(a Array) string",
				"collection_immutable.ArrayOf(1)",
			},
			notContains: []string{"collection_immutable.Array)", "a collection_immutable.Array"},
		},
		{
			// A GALA package named after the Go package it imports, with NO
			// hand-written .go sibling at all. This is the case where consulting
			// GoTypeInfo is wrong for a structural reason: every imported Go
			// package is merged into it and it is keyed by package *name*, so
			// DeclaresType("list", "List") is satisfied by container/list and
			// cannot tell that apart from a local declaration. The bare `List`
			// resolved to list.List, the lambda lost its parameter types, and the
			// generated Go did not compile — while `checkGeneratedGo` did not
			// catch it, because the failure is in the types, not the syntax.
			name:     "gala package named after an imported go package",
			pkgName:  "list",
			galaName: "list.gala",
			src: `package list

import (
	golist "container/list"
	. "martianoff/gala/collection_immutable"
)

func Twice(l List[int]) List[int] {
	val m = l.Map((x) => x * 2)
	return m
}

func Unused() int {
	var l = golist.New()
	Println(l.Len())
	return 1
}
`,
			contains: []string{
				// The parameter keeps its type parameter - the thing that broke.
				// The current package's own type is emitted unqualified, per
				// OwnImportPath, so this is `List[int]`, not `list.List[int]`.
				"func Twice(l List[int]) List[int]",
				// And the lambda stays monomorphic. Asserted on the signature
				// rather than the whole body: gofmt breaks the return onto its
				// own line, and an over-specified string here fails on
				// formatting while the bug it guards goes unnoticed. It did,
				// once - the first version of this assertion expected
				// `func(x int) int { return x * 2 }` and failed on a fix that
				// was working correctly.
				"func(x int) int",
			},
			notContains: []string{
				"func(x any) any",
				"func(x interface{}) interface{}",
				"List[any]",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			galaPath := filepath.Join(tmp, tc.galaName)
			require.NoError(t, os.WriteFile(galaPath, []byte(tc.src), 0644))
			// Only the cases that model a hand-written sibling write one. The
			// package-name collision needs none, and writing an empty local.go
			// would give it a Go file it does not have.
			if tc.localGo != "" {
				require.NoError(t, os.WriteFile(filepath.Join(tmp, "local.go"), []byte(tc.localGo), 0644))
			}

			p := transpiler.NewAntlrGalaParser()
			tree, _, err := p.Parse(tc.src)
			require.NoError(t, err)
			richAST, err := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), nil).Analyze(tree, nil, galaPath)
			require.NoError(t, err)
			fset, file, err := transformer.NewGalaASTTransformer().Transform(richAST)
			require.NoError(t, err)
			out, err := generator.NewGoCodeGenerator().Generate(fset, file)
			require.NoError(t, err)
			checkGeneratedGo(t, out)

			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, unwanted := range tc.notContains {
				assert.NotContains(t, out, unwanted)
			}
		})
	}
}
