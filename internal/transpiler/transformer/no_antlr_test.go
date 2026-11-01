package transformer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// antlrPackages are the packages of ANTLR's tree: its runtime and the
// generated parser.
var antlrPackages = []string{
	"github.com/antlr4-go/antlr/v4",
	"martianoff/gala/internal/parser/grammar",
}

// TestTransformerReadsNoANTLR keeps the transformer on the native tree: no
// source of the library imports ANTLR's runtime or its generated parser, and
// the library's Bazel target does not depend on them. A file's tree reaches
// the transformer as RichAST.Native only.
func TestTransformerReadsNoANTLR(t *testing.T) {
	srcs, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var read int
	for _, src := range srcs {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, src, nil, parser.ImportsOnly)
		require.NoError(t, err)
		read++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			for _, banned := range antlrPackages {
				assert.NotEqualf(t, banned, path, "%s imports %s", src, path)
			}
		}
	}
	require.Greater(t, read, 100, "the library's sources are not staged")

	build, err := os.ReadFile("BUILD.bazel")
	require.NoError(t, err)
	library := regexp.MustCompile(`(?s)go_library\(\s*name = "transformer",.*?\n\)`).Find(build)
	require.NotNil(t, library, "no go_library named transformer in BUILD.bazel")
	deps := regexp.MustCompile(`(?s)\n    deps = \[(.*?)\n    \]`).FindSubmatch(library)
	require.NotNil(t, deps, "the transformer library lists no deps")
	for _, dep := range []string{"//internal/parser/grammar", "@com_github_antlr4_go_antlr_v4//:antlr"} {
		assert.NotContainsf(t, string(deps[1]), `"`+dep+`"`, "the transformer library depends on %s", dep)
	}
}
