package analyzer

import (
	"errors"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// fakeGoList is a `go list -export -deps` stand-in: closure maps a path to the
// packages listing it reports (the path and its dependencies), exports maps a
// package to its export file. It records the batches it was run with.
type fakeGoList struct {
	closure map[string][]string
	exports map[string]string
	calls   [][]string
}

func (f *fakeGoList) list(paths []string) []goListedPackage {
	f.calls = append(f.calls, slices.Clone(paths))
	var out []goListedPackage
	for _, p := range paths {
		deps, ok := f.closure[p]
		if !ok {
			deps = []string{p}
		}
		for _, d := range deps {
			out = append(out, goListedPackage{ImportPath: d, Export: f.exports[d]})
		}
	}
	return out
}

// fakeSourceImporter records the paths it is asked for and hands back an empty
// package.
type fakeSourceImporter struct{ imported []string }

func (f *fakeSourceImporter) Import(path string) (*types.Package, error) {
	f.imported = append(f.imported, path)
	return types.NewPackage(path, filepath.Base(path)), nil
}

// No export file in these cases is readable, so every import ends up read from
// source; what differs is how often `go list` runs, and with what.
func TestExportDataImporter_ListingAndFallback(t *testing.T) {
	missingFile := filepath.Join(t.TempDir(), "missing.a")
	tests := []struct {
		name      string
		closure   map[string][]string
		exports   map[string]string
		prefetch  []string
		imports   []string
		wantCalls [][]string // the batches `go list` runs with
	}{
		{
			name:      "a repeated import is listed once",
			imports:   []string{"a", "a", "a"},
			wantCalls: [][]string{{"a"}},
		},
		{
			name:      "a prefetch lists every new path in one run, duplicates dropped",
			prefetch:  []string{"a", "b", "a", "c"},
			imports:   []string{"c", "b", "a"},
			wantCalls: [][]string{{"a", "b", "c"}},
		},
		{
			name:      "a dependency an earlier listing reported is not listed again",
			closure:   map[string][]string{"top": {"dep", "top"}},
			imports:   []string{"top", "dep"},
			wantCalls: [][]string{{"top"}},
		},
		{
			name:     "unsafe and C are never listed",
			prefetch: []string{"unsafe", "C"},
			imports:  []string{"C"},
		},
		{
			name:      "a path the listing does not report is not listed again",
			closure:   map[string][]string{"gone": {}},
			imports:   []string{"gone", "gone"},
			wantCalls: [][]string{{"gone"}},
		},
		{
			name:      "unreadable export data falls back to source",
			exports:   map[string]string{"a": missingFile},
			imports:   []string{"a"},
			wantCalls: [][]string{{"a"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &fakeGoList{closure: tt.closure, exports: tt.exports}
			source := &fakeSourceImporter{}
			e := newExportDataImporter(token.NewFileSet(), lister.list, source)
			e.prefetch(tt.prefetch)
			for _, p := range tt.imports {
				pkg, err := e.Import(p)
				require.NoError(t, err)
				assert.Equal(t, p, pkg.Path())
			}
			assert.Equal(t, tt.wantCalls, lister.calls)
			assert.Equal(t, tt.imports, source.imported)
		})
	}
}

func TestExportDataImporter_PrefetchBatchesLongLists(t *testing.T) {
	lister := &fakeGoList{}
	e := newExportDataImporter(token.NewFileSet(), lister.list, &fakeSourceImporter{})
	paths := make([]string, goListBatch+5)
	for i := range paths {
		paths[i] = fmt.Sprintf("p%d", i)
	}
	e.prefetch(paths)
	require.Len(t, lister.calls, 2)
	assert.Len(t, lister.calls[0], goListBatch)
	assert.Equal(t, paths[goListBatch:], lister.calls[1])
}

func TestExportDataImporter_KeepsAnExportFileALaterListingOmits(t *testing.T) {
	calls := 0
	list := func(paths []string) []goListedPackage {
		calls++
		if calls == 1 {
			return []goListedPackage{{ImportPath: "dep", Export: "dep.a"}, {ImportPath: "a"}}
		}
		return []goListedPackage{{ImportPath: "dep"}, {ImportPath: "b"}}
	}
	e := newExportDataImporter(token.NewFileSet(), list, &fakeSourceImporter{})
	e.prefetch([]string{"a"})
	e.prefetch([]string{"b"})
	assert.True(t, e.hasExportData("dep"))
}

func TestDecodeGoList(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []goListedPackage
	}{
		{"empty", "", nil},
		{
			"a stream of objects",
			`{"ImportPath": "a", "Export": "/c/a"}` + "\n" + `{"ImportPath": "unsafe"}`,
			[]goListedPackage{{ImportPath: "a", Export: "/c/a"}, {ImportPath: "unsafe"}},
		},
		{
			"objects before a malformed one are kept",
			`{"ImportPath": "a"}{"ImportPath": `,
			[]goListedPackage{{ImportPath: "a"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decodeGoList([]byte(tt.out)))
		})
	}
}

// Export data must yield the same type info as the source importer: the
// transpiler's output may not depend on which of the two read a package. The
// check runs the real go command on standard-library packages whose closure
// is small enough to compile quickly into a fresh build cache.
func TestExportDataImporter_MatchesSourceImporter(t *testing.T) {
	goroot := GoSDKRoot()
	if goroot == "" || !GoImporterAvailable() {
		t.Skip("Go SDK not available")
	}
	// A cache of its own: the test must not depend on HOME being set, and
	// must not write into the developer's cache. And a directory outside any
	// module, whose go directive could ask for a newer Go than this SDK.
	t.Setenv("GOCACHE", t.TempDir())
	t.Chdir(t.TempDir())

	paths := []string{"container/list", "unicode/utf8", "math", "math/bits"}
	fset := token.NewFileSet()
	source := importer.ForCompiler(fset, "source", nil)
	e := newExportDataImporter(fset, goListExports(goroot, false), importerFunc(func(path string) (*types.Package, error) {
		return nil, errors.New("unexpected source fallback for " + path)
	}))
	e.prefetch(paths)
	if !e.hasExportData(paths[0]) {
		out, err := goListCmd(goroot, false, "list", "-e", "-export", "-json=ImportPath,Export,Error", "--", paths[0]).CombinedOutput()
		t.Fatalf("go list -export gave no export file (%v):\n%s", err, out)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			require.True(t, e.hasExportData(path), "go list -export gave no export file")
			fromExport, err := e.Import(path)
			require.NoError(t, err)
			fromSource, err := source.Import(path)
			require.NoError(t, err)

			want, got := transpiler.NewGoTypeInfo(), transpiler.NewGoTypeInfo()
			extractPackageInfo(fromSource, want, false)
			extractPackageInfo(fromExport, got, false)
			require.False(t, want.IsEmpty())
			assert.Equal(t, want, got)
		})
	}
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// A go command that cannot be run lists nothing, which leaves every package to
// the source importer.
func TestGoListExports_MissingGoCommand(t *testing.T) {
	list := goListExports(filepath.Join(t.TempDir(), "no-sdk"), false)
	assert.Empty(t, list([]string{"fmt"}))
}
