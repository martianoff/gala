package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// The Go-export scan feeds the dot-import collision check and backs type
// existence for a package whose Go type info is not loaded, so it has to see
// every form an export is declared in, and nothing that is not one.
func TestExportedGoNames(t *testing.T) {
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
		"func Make[T any]() T { var z T; return z }\n" +
		"func (p Plain) Method() {}\n" +
		"var (\n    One, Two = 1, 2\n)\n" +
		"const Max = 3\n" +
		"var s = `\ntype InString int\nfunc InString2() {}\n`\n"
	pkg, names := exportedGoNames(src)
	assert.Equal(t, "dep", pkg)
	assert.Equal(t, []string{"Plain", "Alias", "Box", "Grouped", "Pair", "Make", "One", "Two", "Max"}, names)
}

// A file no platform builds exports nothing; one built only for some
// platforms is kept, whatever the host.
func TestExtractGoFileExportsBuildConstraints(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dep.go"), []byte("package dep\n\ntype Real int\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.go"), []byte("//go:build ignore\n\npackage dep\n\ntype Template int\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "term_plan9.go"), []byte("//go:build plan9\n\npackage dep\n\ntype Termios int\n"), 0644))
	var infos []os.FileInfo
	for _, name := range []string{"dep.go", "gen.go", "term_plan9.go"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		infos = append(infos, fi)
	}
	rich := &transpiler.RichAST{PackageName: "dep"}
	(&galaAnalyzer{}).extractGoFileExports(infos, dir, "dep", rich, false)
	assert.Equal(t, []string{"Real", "Termios"}, rich.GoExports["dep"])
}
