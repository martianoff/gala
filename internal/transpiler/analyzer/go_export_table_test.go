package analyzer

import (
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// exportFileOf returns the export data file `go list -export` compiles for the
// standard-library package path, skipping the test without a Go SDK.
func exportFileOf(t *testing.T, path string) string {
	t.Helper()
	goroot := GoSDKRoot()
	if goroot == "" || !GoImporterAvailable() {
		t.Skip("Go SDK not available")
	}
	// A cache of its own, as in TestExportDataImporter_MatchesSourceImporter.
	t.Setenv("GOCACHE", t.TempDir())
	t.Chdir(t.TempDir())
	for _, pkg := range goListExports(goroot, false)([]string{path}) {
		if pkg.ImportPath == path && pkg.Export != "" {
			return pkg.Export
		}
	}
	t.Fatalf("go list -export gave no export file for %s", path)
	return ""
}

// copyFile copies src to dst, creating dst's directory.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()
	out, err := os.Create(dst)
	require.NoError(t, err)
	defer out.Close()
	_, err = io.Copy(out, in)
	require.NoError(t, err)
}

// setGoExportFiles sets the shared table for the length of the test.
func setGoExportFiles(t *testing.T, files map[string]string) {
	t.Helper()
	SetGoExportFiles(files, "")
	t.Cleanup(func() { SetGoExportFiles(nil, "") })
}

// typeInfo is what the analyzer keeps of pkg.
func typeInfo(pkg *types.Package) *transpiler.GoTypeInfo {
	info := transpiler.NewGoTypeInfo()
	extractPackageInfo(pkg, info, false)
	return info
}

func TestTableExportImporter_WithoutAnEntryUsesTheFallback(t *testing.T) {
	fallback := &fakeSourceImporter{}
	table := newGoExportFiles()
	table.set(map[string]string{"example.com/other": "other.x"}, "")
	imp := newTableExportImporter(token.NewFileSet(), table, fallback)

	pkg, err := imp.Import("example.com/a")
	require.NoError(t, err)
	assert.Equal(t, "example.com/a", pkg.Path())
	assert.Equal(t, []string{"example.com/a"}, fallback.imported)
}

func TestTableExportImporter_UnreadableExportDataUsesTheFallback(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "a.x")
	require.NoError(t, os.WriteFile(bad, []byte("not export data"), 0o644))
	missing := filepath.Join(t.TempDir(), "missing.x")
	for name, file := range map[string]string{"garbage": bad, "missing file": missing} {
		t.Run(name, func(t *testing.T) {
			fallback := &fakeSourceImporter{}
			table := newGoExportFiles()
			table.set(map[string]string{"example.com/a": file}, "")
			imp := newTableExportImporter(token.NewFileSet(), table, fallback)

			for range 2 {
				_, err := imp.Import("example.com/a")
				require.NoError(t, err)
			}
			assert.Equal(t, []string{"example.com/a", "example.com/a"}, fallback.imported)
		})
	}
}

// A package the table names an export file for is read from it, not from
// the fallback, and has the types the source importer gives it.
func TestTableExportImporter_ReadsTheExportFile(t *testing.T) {
	const path = "container/list"
	exportFile := exportFileOf(t, path)
	stdlibDir := t.TempDir()
	copyFile(t, exportFile, filepath.Join(stdlibDir, "container", "list.a"))

	want, err := importer.ForCompiler(token.NewFileSet(), "source", nil).Import(path)
	require.NoError(t, err)

	for name, set := range map[string]func(*goExportFiles){
		"--go-export entry":       func(f *goExportFiles) { f.set(map[string]string{path: exportFile}, "") },
		"--go-stdlib-export file": func(f *goExportFiles) { f.set(nil, stdlibDir) },
	} {
		t.Run(name, func(t *testing.T) {
			fallback := &fakeSourceImporter{}
			table := newGoExportFiles()
			set(table)
			imp := newTableExportImporter(token.NewFileSet(), table, fallback)

			got, err := imp.Import(path)
			require.NoError(t, err)
			assert.Empty(t, fallback.imported)
			assert.True(t, got.Complete())
			assert.Equal(t, typeInfo(want), typeInfo(got))
		})
	}
}

func TestTableExportImporter_StdlibDirWithoutTheArchiveUsesTheFallback(t *testing.T) {
	fallback := &fakeSourceImporter{}
	table := newGoExportFiles()
	table.set(nil, t.TempDir())
	imp := newTableExportImporter(token.NewFileSet(), table, fallback)

	_, err := imp.Import("container/list")
	require.NoError(t, err)
	assert.Equal(t, []string{"container/list"}, fallback.imported)
}

// A request is served with its own files only, and a library rebuilt in
// place, as a worker that outlives builds sees, is read again.
func TestTableExportImporter_ReadsWhatTheLatestTableNames(t *testing.T) {
	const path = "example.com/rebuilt"
	file := filepath.Join(t.TempDir(), "rebuilt.x")
	list, utf8 := exportFileOf(t, "container/list"), exportFileOf(t, "unicode/utf8")
	copyFile(t, list, file)
	fallback := &fakeSourceImporter{}
	table := newGoExportFiles()
	imp := newTableExportImporter(token.NewFileSet(), table, fallback)

	table.set(map[string]string{path: file}, "")
	pkg, err := imp.Import(path)
	require.NoError(t, err)
	assert.NotNil(t, pkg.Scope().Lookup("List"))

	copyFile(t, utf8, file)
	table.set(map[string]string{path: file}, "")
	pkg, err = imp.Import(path)
	require.NoError(t, err)
	assert.Nil(t, pkg.Scope().Lookup("List"))
	assert.NotNil(t, pkg.Scope().Lookup("RuneLen"))

	table.set(nil, "")
	_, err = imp.Import(path)
	require.NoError(t, err)
	assert.Equal(t, []string{path}, fallback.imported, "a table without the path")
}

func TestGoExportFiles_SetReportsChangedPaths(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.x"), filepath.Join(dir, "b.x")
	require.NoError(t, os.WriteFile(a, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("b"), 0o644))
	table := newGoExportFiles()
	set := func(files map[string]string, stdlibDir string) []any {
		paths, stdlib, _ := table.set(files, stdlibDir)
		_, generation := table.state()
		return []any{paths, stdlib, generation}
	}

	assert.Equal(t, []any{[]string(nil), false, 0}, set(nil, ""), "nothing")
	assert.Equal(t, []any{[]string{"a"}, false, 0}, set(map[string]string{"a": a}, ""), "a new path")
	assert.Equal(t, []any{[]string(nil), false, 0}, set(map[string]string{"a": a, "c": ""}, ""), "the same table, and an empty file")
	assert.Equal(t, []any{[]string{"a", "b"}, false, 1}, set(map[string]string{"b": a, "a": b}, ""), "another file for a path, and a new one")

	require.NoError(t, os.WriteFile(b, []byte("rebuilt, longer"), 0o644))
	assert.Equal(t, []any{[]string{"a"}, false, 2}, set(map[string]string{"b": a, "a": b}, ""), "a rewritten file")
	assert.Equal(t, []any{[]string{"b"}, false, 2}, set(map[string]string{"a": b}, ""), "a path dropped")

	assert.Equal(t, []any{[]string(nil), true, 2}, set(map[string]string{"a": b}, dir), "a stdlib dir")
	assert.Equal(t, []any{[]string(nil), false, 2}, set(map[string]string{"a": b}, dir), "the same stdlib dir")
	assert.Equal(t, []any{[]string{"a"}, true, 2}, set(nil, ""), "a cleared table")
	assert.Equal(t, []any{[]string{"b"}, false, 3}, set(map[string]string{"b": b}, ""), "a path given another file than it had in an earlier table")
}

// The shared importer reads the table whether or not it had entries when it
// was built: a Bazel worker builds it on its first request, which may name no
// export files, and a later request's must still be read. So must the importer
// that type-checks hand-written .go files, which would otherwise read a package
// --go-src names from its source.
func TestExportFilesSetAfterTheImporterIsBuiltAreRead(t *testing.T) {
	// The export data of container/list under a path no source importer can
	// find, so that only the table can give it.
	const path = "example.com/go-export-table-test/list"
	exportFile := exportFileOf(t, "container/list")
	shared := getGoImporter()
	require.NotNil(t, shared)

	setGoExportFiles(t, map[string]string{path: exportFile})

	pkg, err := shared.Import(path)
	require.NoError(t, err)
	assert.NotNil(t, pkg.Scope().Lookup("List"))

	// An empty source directory: reading the package from it would fail.
	modules := newModuleSourceImporter(map[string]string{path: t.TempDir()})
	pkg, err = modules.Import(path)
	require.NoError(t, err)
	assert.NotNil(t, pkg.Scope().Lookup("List"))
}

// A path whose export file changes drops the type information read for it
// before, and only for it.
func TestSetGoExportFiles_DropsTypeInfoReadBefore(t *testing.T) {
	const stale, other = "example.com/go-export-table-test/stale", "example.com/go-export-table-test/other"
	goPackageCache.mu.Lock()
	goPackageCache.cache[stale] = transpiler.NewGoTypeInfo()
	goPackageCache.cache[other] = transpiler.NewGoTypeInfo()
	goPackageCache.mu.Unlock()
	t.Cleanup(func() { forgetGoPackages([]string{stale, other}, false) })
	cached := func(path string) bool {
		goPackageCache.mu.Lock()
		defer goPackageCache.mu.Unlock()
		_, ok := goPackageCache.cache[path]
		return ok
	}

	SetGoExportFiles(nil, "")
	assert.True(t, cached(stale), "an unchanged table keeps it")

	setGoExportFiles(t, map[string]string{stale: filepath.Join(t.TempDir(), "stale.x")})
	assert.False(t, cached(stale), "a new entry drops it")
	assert.True(t, cached(other), "and keeps the other packages")
}
