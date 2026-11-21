package transformer_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// dupBoxPackage is the package pkg of module example.com/dup, which declares
// Box twice: a.gala, with Box's method Plain, and b.gala. Each file's analysis
// fails with GALA-E0011, so an importer has the package without a.gala.
func dupBoxPackage(pkg string, files map[string]string) map[string]string {
	out := map[string]string{
		"gala.mod":      "module example.com/dup\n\ngala dev\n",
		pkg + "/a.gala": "package " + pkg + "\n\nstruct Box(V int)\n\nfunc (b Box) Plain(u int, w int) int = u + w\n",
		pkg + "/b.gala": "package " + pkg + "\n\nstruct Box(W int)\n",
	}
	maps.Copy(out, files)
	return out
}

// mainImporting is a main package importing example.com/dup/imp, with body
// as the body of main.
func mainImporting(imp, body string) string {
	return "package main\n\nimport \"example.com/dup/" + imp + "\"\n\nfunc main() {\n    " + body + "\n}\n"
}

// A file of an imported package that fails its analysis, here because it
// declares a type a sibling declares too (GALA-E0011), is left out of the
// package's metadata. An importer transpiled before the package used to fail
// on what the missing declarations caused in it — "its parameter names are
// not known" — and the build stopped there, so the error a user saw depended
// on whether the package's path sorted before or after the importer's
// (#1305). The package's own error is reported now. An importer that
// compiles against the partial metadata still does: the package's error is
// reported when the package itself is transpiled.
func TestImportedPackageErrorReportedBeforeImporterCascade(t *testing.T) {
	variants := []struct {
		name     string
		files    func(pkg string) map[string]string
		galaFile func(pkg string) string // the file transpiled
	}{
		{
			// a.gala is left out, so Plain's parameter names are not known.
			name: "named arguments",
			files: func(pkg string) map[string]string {
				return dupBoxPackage(pkg, map[string]string{
					"main.gala": mainImporting(pkg, "Println("+pkg+".Box(1).Plain(w = 2, u = 1))"),
				})
			},
		},
		{
			// Reached through another package, which compiles.
			name: "transitive",
			files: func(pkg string) map[string]string {
				return dupBoxPackage(pkg, map[string]string{
					"mid/mid.gala": "package mid\n\nimport \"example.com/dup/" + pkg + "\"\n\n" +
						"func Get() " + pkg + ".Box = " + pkg + ".Box(1)\n",
					"main.gala": mainImporting("mid", "Println(mid.Get().Plain(w = 2, u = 1))"),
				})
			},
		},
		{
			// The package's own file, with both declarations in its siblings:
			// the diagnostic is rendered against one of them. Its caret used to
			// be put on c.gala's line in a.gala, which has no such line.
			name: "declarations in two other files",
			files: func(pkg string) map[string]string {
				return map[string]string{
					"gala.mod":      "module example.com/dup\n\ngala dev\n",
					pkg + "/a.gala": "package " + pkg + "\n\nfunc Plain(u int, w int) int = u + w\n",
					pkg + "/b.gala": "package " + pkg + "\n\nstruct Box(V int)\n",
					pkg + "/c.gala": "package " + pkg + "\n\n// A second Box, declared further down\n// than a.gala has lines.\n\nstruct Box(W int)\n",
				}
			},
			galaFile: func(pkg string) string { return pkg + "/a.gala" },
		},
	}
	for _, v := range variants {
		for _, pkg := range []string{"lib", "zlib", "shapes"} {
			t.Run(v.name+"/"+pkg, func(t *testing.T) {
				galaFile := "main.gala"
				if v.galaFile != nil {
					galaFile = v.galaFile(pkg)
				}
				root := t.TempDir()
				_, err := transpileInModuleAt(t, root, v.files(pkg), galaFile)
				requireTypeRedefinedIn(t, err, filepath.Join(root, pkg))
			})
		}
	}
}

// What an earlier analysis of the package, before its second declaration
// was added, left in the analysis cache must not change what is reported.
func TestImportedPackageErrorReportedAfterEarlierAnalysis(t *testing.T) {
	for _, pkg := range []string{"lib", "zlib", "shapes"} {
		t.Run(pkg, func(t *testing.T) {
			root := t.TempDir()
			files := dupBoxPackage(pkg, map[string]string{
				"main.gala": mainImporting(pkg, "Println("+pkg+".Box(1).Plain(w = 2, u = 1))"),
			})
			healthy := maps.Clone(files)
			delete(healthy, pkg+"/b.gala")
			_, err := transpileInModuleAt(t, root, healthy, "main.gala")
			require.NoError(t, err)

			for range 2 { // the second analysis would be served from the cache
				_, err = transpileInModuleAt(t, root, files, "main.gala")
				requireTypeRedefinedIn(t, err, filepath.Join(root, pkg))
			}
		})
	}
}

// requireTypeRedefinedIn requires err to report GALA-E0011 first, with its
// caret on a declaration of Box in a file of the package in pkgDir.
func requireTypeRedefinedIn(t *testing.T, err error, pkgDir string) {
	t.Helper()
	require.Error(t, err)
	diags := galaerr.Diagnostics(err)
	require.NotEmpty(t, diags, "err: %v", err)
	d := diags[0]
	require.Equal(t, string(galaerr.CodeTypeRedefinition), d.Code, "diagnostic %q; err: %v", d.Message, err)
	require.Contains(t, d.Message, `type "Box"`)
	require.Equal(t, pkgDir, filepath.Dir(d.File), "diagnostic %q", d.Message)
	src, readErr := os.ReadFile(d.File)
	require.NoError(t, readErr)
	lines := strings.Split(string(src), "\n")
	require.True(t, d.Line >= 1 && d.Line <= len(lines), "diagnostic %q at %s:%d", d.Message, d.File, d.Line)
	require.Contains(t, lines[d.Line-1], "struct Box", "diagnostic %q at %s:%d", d.Message, d.File, d.Line)
}
