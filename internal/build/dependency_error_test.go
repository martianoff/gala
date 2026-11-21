package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A package declaring a type twice must be reported by `gala build` as its own
// GALA-E0011. The build transpiles files in path order, so whether it did used
// to depend on the package's name: `lib` sorts before main.gala and reported
// E0011, while `zlib` sorts after it, so main.gala was transpiled first,
// against the package's partial metadata, and the build stopped on "its
// parameter names are not known" (#1305). The transformer's
// TestImportedPackageErrorReportedBeforeImporterCascade covers the variants.
func TestBuild_ImportedPackageErrorReportedBeforeImporterCascade(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/dup", map[string]string{
		"zlib/a.gala": "package zlib\n\nstruct Box(V int)\n\nfunc (b Box) Plain(u int, w int) int = u + w\n",
		"zlib/b.gala": "package zlib\n\nstruct Box(W int)\n",
		"main.gala":   "package main\n\nimport \"example.com/dup/zlib\"\n\nfunc main() {\n    Println(zlib.Box(1).Plain(w = 2, u = 1))\n}\n",
	})
	chdirForTest(t, projectDir)
	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	_, err = b.Build("")
	require.Error(t, err)
	diags := galaerr.Diagnostics(err)
	require.NotEmpty(t, diags, "err: %v", err)
	require.Equal(t, string(galaerr.CodeTypeRedefinition), diags[0].Code, "diagnostic %q; err: %v", diags[0].Message, err)
	require.Equal(t, filepath.Join(projectDir, "zlib", "a.gala"), diags[0].File)
	require.Equal(t, 3, diags[0].Line)
}
