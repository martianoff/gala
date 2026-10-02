package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// The Go-export scan backs type existence for a package whose Go type info is
// not loaded, so it has to see every form an exported type is declared in.
func TestExportedGoTypeNames(t *testing.T) {
	src := "package dep\n\n" +
		"// type Commented int\n" +
		"type Plain struct{}\n" +
		"type Alias = Plain\n" +
		"type Box[T any] struct{ v T }\n" +
		"type unexported int\n" +
		"type (\n" +
		"    Grouped int\n" +
		"    Pair[A, B any] struct {\n" +
		"        Field A\n" +
		"    }\n" +
		"    hidden int\n" +
		")\n" +
		"var s = `\ntype InString int\n`\n"
	assert.Equal(t, []string{"Plain", "Alias", "Box", "Grouped", "Pair"}, exportedGoTypeNames(src))
}

// A file Go would not build exports nothing.
func TestExtractGoFileExportsSkipsUnbuiltFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dep.go"), []byte("package dep\n\ntype Real int\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.go"), []byte("//go:build ignore\n\npackage dep\n\ntype Template int\n"), 0644))
	var infos []os.FileInfo
	for _, name := range []string{"dep.go", "gen.go"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		infos = append(infos, fi)
	}
	rich := &transpiler.RichAST{PackageName: "dep"}
	(&galaAnalyzer{}).extractGoFileExports(infos, dir, "dep", rich, false)
	assert.Equal(t, []string{"Real"}, rich.GoExports["dep"])
}
