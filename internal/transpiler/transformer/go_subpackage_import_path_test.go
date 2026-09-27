package transformer_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestGoSubpackageTypeImportedByModulePath covers GALA code that uses a type
// from a hand-written Go package inside the same module, where the type only
// reaches the GALA code through inference (a match whose arms call the Go
// constructor). The generated Go must import that package by its module import
// path. It used to import it by the directory the analyzer type-checked, which
// is unparseable Go on Windows and "not a package path" to `go` elsewhere.
func TestGoSubpackageTypeImportedByModulePath(t *testing.T) {
	cases := []struct {
		name    string
		galaDir string // directory of the GALA file under the module root
		source  string
		imports []string // import paths the generated Go must declare
		absent  []string // text the generated Go must not contain
	}{
		{
			name:    "package main at the module root",
			galaDir: ".",
			source: `package main

import "example.com/gosubpkg/box"

func open(o Option[int]) *box.Box {
    val b = o match {
        case Some(n) => box.New(n)
        case None() => box.New(0)
    }
    return b
}

func main() {
    Println(s"size: ${open(Some(3)).Size}")
}
`,
			imports: []string{"example.com/gosubpkg/box"},
		},
		{
			name:    "a library package in a subdirectory",
			galaDir: "lib",
			source: `package lib

import "example.com/gosubpkg/box"

func Open(o Option[int]) int {
    val b = o match {
        case Some(n) => box.New(n)
        case None() => box.New(0)
    }
    return b.Size
}
`,
			imports: []string{"example.com/gosubpkg/box"},
		},
		{
			name:    "a GALA package beside its own hand-written Go",
			galaDir: "box",
			source: `package box

func Pick(o Option[int]) *Box {
    val b = o match {
        case Some(n) => New(n)
        case None() => New(0)
    }
    return b
}
`,
			absent: []string{"box.Box"}, // its own type is unqualified
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range map[string]string{
				"go.mod":     "module example.com/gosubpkg\n\ngo 1.25\n",
				"gala.mod":   "module example.com/gosubpkg\n",
				"box/box.go": "package box\n\ntype Box struct{ Size int }\n\nfunc New(size int) *Box { return &Box{Size: size} }\n",
			} {
				p := filepath.Join(root, filepath.FromSlash(name))
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
				require.NoError(t, os.WriteFile(p, []byte(content), 0644))
			}
			galaFile := filepath.Join(root, tc.galaDir, "main.gala")
			require.NoError(t, os.MkdirAll(filepath.Dir(galaFile), 0755))
			require.NoError(t, os.WriteFile(galaFile, []byte(tc.source), 0644))

			searchPaths := []string{root}
			for _, sp := range getStdSearchPath() {
				abs, err := filepath.Abs(sp)
				require.NoError(t, err)
				searchPaths = append(searchPaths, abs)
			}
			wd, err := os.Getwd()
			require.NoError(t, err)
			require.NoError(t, os.Chdir(root))
			defer func() { _ = os.Chdir(wd) }()

			p := transpiler.NewAntlrGalaParser()
			trans := newCheckedTranspiler(p, analyzer.NewGalaAnalyzer(p, searchPaths),
				transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			goCode, err := trans.Transpile(tc.source, galaFile)
			require.NoError(t, err)

			f, err := parser.ParseFile(token.NewFileSet(), "gen.go", goCode, parser.ImportsOnly)
			require.NoError(t, err, "generated Go must parse:\n%s", goCode)
			var got []string
			for _, spec := range f.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				require.NoError(t, err)
				assert.True(t, transpiler.IsValidGoImportPath(path), "not an import path: %s", spec.Path.Value)
				got = append(got, path)
			}
			for _, want := range tc.imports {
				assert.Contains(t, got, want, "generated Go:\n%s", goCode)
			}
			assert.NotContains(t, got, "example.com/gosubpkg/"+filepath.ToSlash(tc.galaDir),
				"a package must not import itself:\n%s", goCode)
			for _, text := range tc.absent {
				assert.NotContains(t, goCode, text, "generated Go:\n%s", goCode)
			}
		})
	}
}
