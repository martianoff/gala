package transformer_test

import (
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

// TestSameNameImports covers files whose imports bind one package name twice:
// Go `strings` beside an aliased GALA `strings`, or one GALA path imported both
// plainly and under an alias.
//
// GALA metadata names a package by NAME, and the import manager's name index
// kept whichever import was declared last. With the Go import after the GALA
// one, a generic method on GALA's `Str` was lowered to `strings.Str_Fold` and
// resolved against Go's `strings`. With one path imported twice, only the later
// entry survived, so calls through the other name lost their inferred types.
func TestSameNameImports(t *testing.T) {
	const foldBody = `
func count(s string) int = gs.S(s).Fold(0, (acc, c) => acc + 1)

func main() {
    Println(count("héllo"))
    Println(strings.TrimSpace("  x  "))
}
`
	cases := []struct {
		name        string
		src         string
		sibling     string
		contains    []string
		notContains []string
	}{
		{
			name: "go strings declared before aliased gala strings",
			src: `package main

import (
    "strings"
    gs "martianoff/gala/strings"
)
` + foldBody,
			contains:    []string{"gs.Str_Fold("},
			notContains: []string{"strings.Str_Fold("},
		},
		{
			name: "go strings declared after aliased gala strings",
			src: `package main

import (
    gs "martianoff/gala/strings"
    "strings"
)
` + foldBody,
			contains:    []string{"gs.Str_Fold("},
			notContains: []string{"strings.Str_Fold("},
		},
		{
			name: "one gala path imported plainly and under an alias",
			src: `package main

import (
    "martianoff/gala/strings"
    gs "martianoff/gala/strings"
)

func main() {
    Println(strings.Split("a bb ccc", " ").Map((w) => w.Size()).MkString(","))
    Println(gs.Split("dd e", " ").Map((w) => w.Size()).MkString(","))
}
`,
			contains:    []string{"strings.Split(", "gs.Split(", "func(w string) int"},
			notContains: []string{"func(w any)"},
		},
		{
			// Not a name collision, but the same lowering path: the generic
			// `Map` on an Array another package returned is emitted as
			// `collection_immutable.Array_Map`, which needs an import this
			// file never wrote.
			name: "generic method lowering records the import it references",
			src: `package main

import "martianoff/gala/strings"

func main() {
    Println(strings.Split("a bb ccc", " ").Map((w) => w.Size()).MkString(","))
}
`,
			contains: []string{"collection_immutable.Array_Map(", `"martianoff/gala/collection_immutable"`},
		},
		{
			// A Go type inferred for a lambda parameter carries its import
			// path; a dot import of GALA's `strings` must not strip the Go
			// qualifier from it.
			name: "go type inferred beside a dot-imported gala package of the same name",
			src: `package main

import (
    . "martianoff/gala/strings"
    . "martianoff/gala/collection_immutable"
    "strings"
)

func main() {
    Println(ArrayOf(strings.NewReader("ab")).Map((r) => r.Len()).MkString(","), S("xy").Length())
}
`,
			contains:    []string{"func(r *strings.Reader) int"},
			notContains: []string{"func(r *Reader)"},
		},
		{
			// GALA's `strings` reaches this file only through a sibling's
			// import, while this file imports Go's `strings`: the lowered
			// generic method needs its own import under a qualifier that does
			// not collide with the Go one.
			name: "gala package known only through a sibling beside a go import of the same name",
			src: `package main

import "strings"

func main() {
    Println(label("héllo").Fold(0, (n, c) => n + 1), strings.TrimSpace(" x "))
}
`,
			sibling: `package main

import . "martianoff/gala/strings"

func label(s string) Str = S(s)
`,
			contains:    []string{"gala_strings.Str_Fold(", `gala_strings "martianoff/gala/strings"`},
			notContains: []string{"(strings.Str_Fold("},
		},
		{
			// A Go type written in GALA source carries no import path. With
			// GALA's `strings` known through a sibling, the pattern binding's
			// type must still use this file's Go `strings`.
			name: "go type written in source beside a gala package known through a sibling",
			src: `package main

import "strings"

sealed type Out {
    case ToBuilder(sb *strings.Builder)
    case Discard()
}

func emit(o Out) string = o match {
    case ToBuilder(sb) => sb.String()
    case Discard() => label("x").ToString()
}

func main() {
    var sb strings.Builder
    sb.WriteString("hi")
    Println(emit(ToBuilder(&sb)))
}
`,
			sibling: `package main

import . "martianoff/gala/strings"

func label(s string) Str = S(s)
`,
			contains:    []string{"*strings.Builder"},
			notContains: []string{"gala_strings.Builder"},
		},
		{
			// A Go type from a GALA signature carries no import path; a dot
			// import of GALA's `strings` must not strip its Go qualifier.
			name: "go type from a gala signature beside a dot-imported gala package of the same name",
			src: `package main

import (
    . "martianoff/gala/strings"
    . "martianoff/gala/collection_immutable"
    "strings"
)

func newBuf() *strings.Builder = &strings.Builder{}

func main() {
    Println(ArrayOf(newBuf()).Map((b) => b.Len()).MkString(","), S("xy").Length())
}
`,
			contains:    []string{"func(b *strings.Builder) int"},
			notContains: []string{"func(b *Builder)"},
		},
		{
			// `FileInfo` is declared by both Go's `io/fs` and GALA's `fs`.
			// This file imports the GALA package itself, so a GALA FileInfo
			// inferred from metadata keeps the GALA qualifier.
			name: "type declared by both packages stays gala when the file imports the gala package",
			src: `package main

import (
    "io/fs"
    gfs "martianoff/gala/fs"
)

func mode(m fs.FileMode) string = m.String()

func main() {
    Println(gfs.Stat(".").Map((info) => info.Name).GetOrElse(""), mode(fs.ModeDir))
}
`,
			contains:    []string{"func(info gfs.FileInfo) string"},
			notContains: []string{"func(info fs.FileInfo)"},
		},
		{
			// os.Stat returns io/fs's FileInfo, which this file does not
			// import. A dot import of GALA's `fs` must neither strip its
			// qualifier nor lend it GALA's FileInfo fields.
			name: "go type from an unimported package beside a dot-imported gala package of the same name",
			src: `package main

import (
    "os"
    . "martianoff/gala/fs"
)

func main() {
    Println(Try(os.Stat(".")).Map((fi) => fi.IsDir()).GetOrElse(false), Exists("."))
}
`,
			contains:    []string{"func(fi fs.FileInfo) bool", "fi.IsDir()", `"io/fs"`},
			notContains: []string{"func(fi FileInfo)", "fi.IsDir.Get()"},
		},
		{
			// An unaliased `math/rand/v2` binds `rand`, not the last path
			// segment `v2`.
			name: "go type from a major-version import path",
			src: `package main

import (
    "math/rand/v2"
    . "martianoff/gala/collection_immutable"
)

func main() {
    val r = rand.New(rand.NewPCG(1, 2))
    Println(ArrayOf(r).Map((x) => x.IntN(1)).MkString(","))
}
`,
			contains:    []string{"func(x *rand.Rand) int"},
			notContains: []string{"v2.Rand"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			mainPath := filepath.Join(tmp, "main.gala")
			require.NoError(t, os.WriteFile(mainPath, []byte(tc.src), 0644))
			var siblings []string
			if tc.sibling != "" {
				sibPath := filepath.Join(tmp, "sibling.gala")
				require.NoError(t, os.WriteFile(sibPath, []byte(tc.sibling), 0644))
				siblings = append(siblings, sibPath)
			}

			p := transpiler.NewAntlrGalaParser()
			tree, _, err := p.Parse(tc.src)
			require.NoError(t, err)
			richAST, err := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), siblings).Analyze(tree, nil, mainPath)
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
