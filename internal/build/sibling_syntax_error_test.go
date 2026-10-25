package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A package file that does not parse must be reported at its own file, line
// and column by `gala build` and `gala test` (and so by `--json`, which emits
// galaerr.Diagnostics). It used to be dropped from its siblings' analysis, and
// the first file transpiled failed instead with GALA-E0023 "undefined: B" plus
// a hint to import the package into itself.
func TestBuild_SiblingSyntaxErrorReportedAtItsFile(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{
			"a.gala": "package sib\n\nfunc A() int = B()\n",
			// A newline inside a string literal: the lexer rejects it.
			"b.gala":      "package sib\n\nfunc B() int {\n    val s = \"abc\n def\"\n    1\n}\n",
			"a_test.gala": "package sib\n\nimport . \"martianoff/gala/test\"\n\nfunc TestA(t T) T = Eq(t, A(), 1)\n",
		}
	}
	requireSiblingSyntaxError := func(t *testing.T, err error, projectDir string) {
		t.Helper()
		require.Error(t, err)
		diags := galaerr.Diagnostics(err)
		require.NotEmpty(t, diags)
		for _, d := range diags {
			require.Equal(t, filepath.Join(projectDir, "b.gala"), d.File, "diagnostic %q; err: %v", d.Message, err)
			require.Empty(t, d.Code, "got %s %q, want b.gala's syntax error", d.Code, d.Message)
		}
		require.Equal(t, 4, diags[0].Line, "the unterminated string starts on line 4")
	}

	t.Run("build", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sib", files())
		chdirForTest(t, projectDir)
		b, err := NewBuilder(projectDir, "test", false)
		require.NoError(t, err)
		_, err = b.Build("")
		requireSiblingSyntaxError(t, err, projectDir)
	})

	t.Run("test", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sib", files())
		chdirForTest(t, projectDir)
		b, err := NewBuilderForMode(projectDir, "test", false, ModeTest)
		require.NoError(t, err)
		requireSiblingSyntaxError(t, b.Test(false), projectDir)
	})
}
