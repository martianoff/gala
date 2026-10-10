package goexport

import (
	"archive/zip"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"

	"martianoff/gala/internal/stdlib"
)

const testModule = "go.example.com/stdlib"

func exportForTest(t *testing.T) []File {
	t.Helper()
	files, err := Stdlib(StdlibOptions{ModulePath: testModule, GalaVersion: "0.86.0", Commit: "abc123"})
	require.NoError(t, err)
	return files
}

func TestStdlib_OneModuleWithRewrittenImports(t *testing.T) {
	files := exportForTest(t)
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Content
	}

	require.Equal(t, "module "+testModule+"\n\ngo "+stdlib.GoVersion+"\n", string(byPath["go.mod"]))
	require.Equal(t, "gala 0.86.0\ncommit abc123\n", string(byPath["VERSION"]))
	for pkg, sources := range stdlib.EmbeddedPackages {
		for name := range sources {
			require.Contains(t, byPath, pkg+"/"+name)
		}
	}
	for p := range byPath {
		if p != "go.mod" {
			require.NotEqual(t, "go.mod", path.Base(p), "nested go.mod %s", p)
		}
	}
	require.Contains(t, string(byPath["collection_immutable/array.gen.go"]), `"`+testModule+`/std"`)
}

func TestStdlib_Deterministic(t *testing.T) {
	a, b := exportForTest(t), exportForTest(t)
	require.Equal(t, a, b)
	for i := 1; i < len(a); i++ {
		require.Less(t, a[i-1].Path, a[i].Path)
	}
}

func TestStdlib_RejectsUndownloadableModulePaths(t *testing.T) {
	for _, mp := range []string{"", "martianoff/gala", "stdlib", "go.gala.fyi/", "go.gala.fyi//x", "go.gala.fyi/std lib"} {
		_, err := Stdlib(StdlibOptions{ModulePath: mp})
		require.Error(t, err, mp)
	}
}

func TestExport_RewritesOnlyImportPaths(t *testing.T) {
	src := `package p

import (
	"fmt"
	. "old.mod/std"
	alias "old.mod/collection"
)

// Use it: import "old.mod/std"
const where = "old.mod/std"

func F() { fmt.Println(Some(1), alias.X) }
`
	files, err := Export(Module{
		Path:      "new.example/lib",
		GoVersion: "1.24",
		Packages: map[string]map[string]string{
			"p":          {"p.go": src, "p.gala": `import "old.mod/std"`},
			"std":        {"std.go": "package std\n"},
			"collection": {"c.go": "package collection\n"},
		},
		Remap: map[string]string{"old.mod": "new.example/lib"},
	})
	require.NoError(t, err)
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Content)
	}
	want := strings.NewReplacer(
		`. "old.mod/std"`, `. "new.example/lib/std"`,
		`alias "old.mod/collection"`, `alias "new.example/lib/collection"`,
	).Replace(src)
	require.Equal(t, want, got["p/p.go"], "comments and string literals keep the old path")
	require.Equal(t, `import "old.mod/std"`, got["p/p.gala"], "non-Go files are copied as they are")
}

func TestExport_RejectsImportsTheModuleCannotProvide(t *testing.T) {
	_, err := Export(Module{
		Path:      "new.example/lib",
		GoVersion: "1.24",
		Packages:  map[string]map[string]string{"p": {"p.go": "package p\n\nimport _ \"github.com/other/dep\"\n"}},
	})
	require.ErrorContains(t, err, `imports "github.com/other/dep"`)
}

func TestExport_RejectsUnresolvableImports(t *testing.T) {
	export := func(imp string) error {
		_, err := Export(Module{
			Path:      "new.example/lib",
			GoVersion: "1.24",
			Packages:  map[string]map[string]string{"p": {"p.go": "package p\n\nimport _ \"" + imp + "\"\n"}},
			Remap:     map[string]string{"martianoff/gala": "new.example/lib"},
		})
		return err
	}
	require.NoError(t, export("net/http"))
	require.NoError(t, export("martianoff/gala/p"))
	require.ErrorContains(t, export("martianoff/other/x"), "does not provide", "an unremapped GALA module is not Go's standard library")
	require.ErrorContains(t, export("martianoff/gala/missing"), "does not provide", "remapped into a package the export lacks")
}

func TestRemapImports(t *testing.T) {
	src := `package p

import (
	"fmt"
	"old.mod/std"
	. "old.mod/collection"
	other "elsewhere.example/x"
)

// Use it: import "old.mod/std"
func F() { fmt.Println(std.Some(1), ArrayOf(1), other.X) }
`
	cases := []struct {
		name  string
		remap map[string]string
		want  string
	}{
		{
			name:  "rewrites only the remapped imports",
			remap: map[string]string{"old.mod": "new.example/stdlib"},
			want: strings.NewReplacer(
				`	"old.mod/std"`, `	"new.example/stdlib/std"`,
				`. "old.mod/collection"`, `. "new.example/stdlib/collection"`,
			).Replace(src),
		},
		{
			name:  "leaves a file without remapped imports unchanged",
			remap: map[string]string{"unused.mod": "new.example/stdlib"},
			want:  src,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RemapImports("p.go", []byte(src), tc.remap)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestStdlibRemap_OnlyStandardLibraryPackages(t *testing.T) {
	src := `package p

import (
	"martianoff/gala/collection_immutable"
	"martianoff/gala/examples/shapes"
	"martianoff/gala/std"
)

var _ = collection_immutable.ArrayOf[int]
var _ = shapes.X
var _ = std.Some[int]{}
`
	got, err := RemapImports("p.go", []byte(src), StdlibRemap("go.gala.fyi/stdlib"))
	require.NoError(t, err)
	want := strings.NewReplacer(
		`"martianoff/gala/collection_immutable"`, `"go.gala.fyi/stdlib/collection_immutable"`,
		`"martianoff/gala/std"`, `"go.gala.fyi/stdlib/std"`,
	).Replace(src)
	require.Equal(t, want, string(got), "a package of the module that is not in the standard library keeps its path")
}

func TestRemapImport_LongestPrefixWins(t *testing.T) {
	remap := map[string]string{"a.io/x": "n.io/m", "a.io/x/sub": "o.io/k"}
	for i := 0; i < 20; i++ {
		require.Equal(t, "o.io/k/p", remapImport("a.io/x/sub/p", remap))
		require.Equal(t, "n.io/m/other", remapImport("a.io/x/other", remap))
	}
	require.Equal(t, "a.io/xy", remapImport("a.io/xy", remap))
}

func TestWriteDir(t *testing.T) {
	files := []File{{Path: "go.mod", Content: []byte("module x.y/z\n")}, {Path: "a/b.go", Content: []byte("package a\n")}}

	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o644))
	require.ErrorContains(t, WriteDir(notADir, files), "cannot export")

	nonEmpty := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(nonEmpty, "stale"), nil, 0o644))
	require.ErrorContains(t, WriteDir(nonEmpty, files), "not empty")

	out := filepath.Join(t.TempDir(), "out")
	require.NoError(t, WriteDir(out, files))
	got, err := os.ReadFile(filepath.Join(out, "a", "b.go"))
	require.NoError(t, err)
	require.Equal(t, "package a\n", string(got))
}

func TestModuleVersion(t *testing.T) {
	for in, want := range map[string]string{
		"0.86.0":       "v0.86.0",
		"v0.86.0":      "v0.86.0",
		"0.87.0-rc.1":  "v0.87.0-rc.1",
		"1.0.0-dev.20": "v1.0.0-dev.20",
	} {
		got, err := ModuleVersion(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"dev", "0.86", "0.86.0+meta", "01.2.3", ""} {
		_, err := ModuleVersion(bad)
		require.Error(t, err, bad)
	}
}

func TestWriteProxy_Layout(t *testing.T) {
	files := exportForTest(t)
	dir := t.TempDir()
	require.NoError(t, WriteProxy(dir, testModule, "v0.86.0", files))
	require.NoError(t, WriteProxy(dir, testModule, "v0.87.0-rc.1", files))
	require.NoError(t, WriteProxy(dir, testModule, "v0.86.0", files)) // a re-run lists it once

	vdir := filepath.Join(dir, "go.example.com", "stdlib", "@v")
	list, err := os.ReadFile(filepath.Join(vdir, "list"))
	require.NoError(t, err)
	require.Equal(t, "v0.86.0\nv0.87.0-rc.1\n", string(list))

	mod, err := os.ReadFile(filepath.Join(vdir, "v0.86.0.mod"))
	require.NoError(t, err)
	require.Equal(t, "module "+testModule+"\n\ngo "+stdlib.GoVersion+"\n", string(mod))

	data, err := os.ReadFile(filepath.Join(vdir, "v0.86.0.zip"))
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, zr.File, len(files))
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	for _, f := range files {
		require.True(t, names[testModule+"@v0.86.0/"+f.Path], f.Path)
	}

	other := t.TempDir()
	require.NoError(t, WriteProxy(other, testModule, "v0.86.0", files))
	again, err := os.ReadFile(filepath.Join(other, "go.example.com", "stdlib", "@v", "v0.86.0.zip"))
	require.NoError(t, err)
	require.Equal(t, data, again, "the same export must produce the same zip")
}

func TestAppendVersionList_AddsMissingNewline(t *testing.T) {
	list := filepath.Join(t.TempDir(), "list")
	require.NoError(t, os.WriteFile(list, []byte("v0.85.0"), 0o644))
	require.NoError(t, appendVersionList(list, "v0.86.0"))
	got, err := os.ReadFile(list)
	require.NoError(t, err)
	require.Equal(t, "v0.85.0\nv0.86.0\n", string(got))
}

func TestWriteProxy_RejectsFilesTheGoCommandWouldReject(t *testing.T) {
	files := []File{
		{Path: "go.mod", Content: []byte("module " + testModule + "\n")},
		{Path: "a/X.go", Content: []byte("package a\n")},
		{Path: "a/x.go", Content: []byte("package a\n")},
	}
	require.Error(t, WriteProxy(t.TempDir(), testModule, "v0.1.0", files), "case-insensitive file name collision")
}

func TestExport_Requires(t *testing.T) {
	files, err := Export(Module{
		Path:      "example.com/lib",
		GoVersion: "1.24",
		Packages: map[string]map[string]string{
			"":    {"lib.go": "package lib\n\nimport (\n\t_ \"gala-build-workspace/gen/sub\"\n\t_ \"github.com/google/uuid\"\n\t_ \"martianoff/gala/std\"\n)\n"},
			"sub": {"sub.go": "package sub\n"},
		},
		Remap: map[string]string{
			"gala-build-workspace/gen": "example.com/lib",
			"martianoff/gala":          "go.gala.fyi/stdlib",
		},
		Requires: []module.Version{
			{Path: "go.gala.fyi/stdlib", Version: "v0.87.0"},
			{Path: "github.com/google/uuid", Version: "v1.6.0"},
			{Path: "golang.org/x/sys", Version: "v0.26.0"},
		},
		Indirect: map[string]bool{"golang.org/x/sys": true},
	})
	require.NoError(t, err)
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Content)
	}
	require.Equal(t, "module example.com/lib\n\ngo 1.24\n\nrequire (\n\tgithub.com/google/uuid v1.6.0\n\tgo.gala.fyi/stdlib v0.87.0\n\tgolang.org/x/sys v0.26.0 // indirect\n)\n", got["go.mod"])
	require.Contains(t, got["lib.go"], `"example.com/lib/sub"`)
	require.Contains(t, got["lib.go"], `"go.gala.fyi/stdlib/std"`)

	_, err = Export(Module{
		Path:      "example.com/lib",
		GoVersion: "1.24",
		Packages:  map[string]map[string]string{"": {"lib.go": "package lib\n\nimport _ \"example.com/otherlib\"\n"}},
		Requires:  []module.Version{{Path: "go.gala.fyi/stdlib", Version: "v0.87.0"}},
	})
	require.ErrorContains(t, err, `imports "example.com/otherlib"`, "a dependency the go.mod does not require")
}

func TestReadPackages(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	write("go.mod", "module gala-build-workspace\n")
	write("lib.gen.go", "package lib\n")
	write("lib_test.go", "package lib\n")
	write("sub/sub.go", "package sub\n")
	write("sub/data/page.html", "<p>embedded</p>")
	write("sub/testdata/fixture.txt", "x")
	write(".hidden/x.go", "package x\n")

	pkgs, err := ReadPackages(dir)
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]string{
		"":         {"lib.gen.go": "package lib\n"},
		"sub":      {"sub.go": "package sub\n"},
		"sub/data": {"page.html": "<p>embedded</p>"},
	}, pkgs)
}

func TestExport_UnsupportedModuleNamesTheReason(t *testing.T) {
	_, err := Export(Module{
		Path:        "example.com/lib",
		GoVersion:   "1.24",
		Packages:    map[string]map[string]string{"": {"lib.go": "package lib\n\nimport _ \"example.com/galadep/x\"\n"}},
		Unsupported: map[string]string{"example.com/galadep": "it is a GALA module"},
	})
	require.ErrorContains(t, err, `imports "example.com/galadep/x": it is a GALA module`)
}
