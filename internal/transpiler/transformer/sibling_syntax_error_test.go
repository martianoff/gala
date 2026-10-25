package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// brokenSibling declares B but does not parse: a case body must be a simple
// statement or a block, not `return v`.
const brokenSibling = `package p

func B() int {
    val x = Some(1)
    x match {
        case Some(v) => return v
        case None() => Println("none")
    }
    0
}
`

const usesSibling = "package p\n\nfunc A() int = B()\n"

// writeSiblingPackage writes a.gala (using B) and b.gala (declaring it) into a
// fresh directory and returns their paths.
func writeSiblingPackage(t *testing.T, b string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.gala")
	bPath := filepath.Join(dir, "b.gala")
	require.NoError(t, os.WriteFile(aPath, []byte(usesSibling), 0o644))
	require.NoError(t, os.WriteFile(bPath, []byte(b), 0o644))
	return aPath, bPath
}

// requireOnlyErrorsIn asserts that every diagnostic err carries is located in
// file, which is how a sibling's syntax errors must surface: at their own
// file, line and column, not as an undefined name in the file being compiled.
func requireOnlyErrorsIn(t *testing.T, err error, file string) {
	t.Helper()
	require.Error(t, err)
	diags := galaerr.Diagnostics(err)
	require.NotEmpty(t, diags)
	for _, d := range diags {
		require.Equal(t, file, d.File, "diagnostic %q is not in the sibling that does not parse; err: %v", d.Message, err)
		require.Empty(t, d.Code, "a syntax error is uncoded; got %s %q", d.Code, d.Message)
		require.Positive(t, d.Line)
	}
}

// A file of a package that does not parse used to be dropped from its
// siblings' analysis without a word, so a name it declares surfaced as
// GALA-E0023 "undefined: B" in another file, with a hint to import the package
// into itself. The sibling's syntax errors are what is reported now.
func TestSiblingSyntaxErrorReportedInsteadOfUndefined(t *testing.T) {
	newTranspiler := func(a transpiler.Analyzer) transpiler.Transpiler {
		p := transpiler.NewAntlrGalaParser()
		return transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
	}

	t.Run("explicit package files", func(t *testing.T) {
		aPath, bPath := writeSiblingPackage(t, brokenSibling)
		a := analyzer.NewGalaAnalyzerWithPackageFiles(transpiler.NewAntlrGalaParser(), getStdSearchPath(), []string{aPath, bPath})
		_, err := newTranspiler(a).Transpile(usesSibling, aPath)
		requireOnlyErrorsIn(t, err, bPath)
	})

	t.Run("directory discovery, cached, then fixed", func(t *testing.T) {
		aPath, bPath := writeSiblingPackage(t, brokenSibling)
		tr := newTranspiler(analyzer.NewGalaAnalyzer(transpiler.NewAntlrGalaParser(), getStdSearchPath()))
		_, err := tr.Transpile(usesSibling, aPath)
		requireOnlyErrorsIn(t, err, bPath)

		// The second analysis is served from the sibling cache.
		_, err = tr.Transpile(usesSibling, aPath)
		requireOnlyErrorsIn(t, err, bPath)

		// Fixing the sibling must not leave its syntax error cached.
		require.NoError(t, os.WriteFile(bPath, []byte("package p\n\nfunc B() int = 1\n"), 0o644))
		_, err = tr.Transpile(usesSibling, aPath)
		require.NoError(t, err)
	})

	t.Run("a broken test file is not a sibling of a source file", func(t *testing.T) {
		aPath, _ := writeSiblingPackage(t, "package p\n\nfunc B() int = 1\n")
		testPath := filepath.Join(filepath.Dir(aPath), "a_test.gala")
		require.NoError(t, os.WriteFile(testPath, []byte(brokenSibling), 0o644))
		tr := newTranspiler(analyzer.NewGalaAnalyzer(transpiler.NewAntlrGalaParser(), getStdSearchPath()))
		_, err := tr.Transpile(usesSibling, aPath)
		require.NoError(t, err)
	})
}
