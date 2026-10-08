package goexport

import (
	"archive/zip"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/stdlib"
)

const testModule = "go.example.com/stdlib"

func exportForTest(t *testing.T) []File {
	t.Helper()
	files, err := Stdlib(Options{ModulePath: testModule, GalaVersion: "0.86.0", Commit: "abc123"})
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
	for p, content := range byPath {
		if p != "go.mod" {
			require.NotEqual(t, "go.mod", path.Base(p), "nested go.mod %s", p)
		}
		if strings.HasSuffix(p, ".go") {
			require.NotContains(t, string(content), "martianoff/gala", p)
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
	for _, mp := range []string{"", "martianoff/gala", "stdlib", "go.gala.fyi/", ".fyi/x", "go.gala.fyi//x"} {
		_, err := Stdlib(Options{ModulePath: mp})
		require.Error(t, err, mp)
	}
}

func TestRewriteImports_RejectsUnrewritableMention(t *testing.T) {
	src := []byte("package p\n\nconst where = \"see martianoff/gala/std\"\n")
	_, err := rewriteImports("p.go", src, "martianoff/gala", testModule)
	require.ErrorContains(t, err, "outside an import")
}

func TestWriteDir_Guards(t *testing.T) {
	files := []File{{Path: "go.mod", Content: []byte("module x.y/z\n")}, {Path: "a/b.go", Content: []byte("package a\n")}}

	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, "MODULE.bazel"), nil, 0o644))
	require.ErrorContains(t, WriteDir(filepath.Join(ws, "out"), files), "Bazel workspace")

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
	for _, bad := range []string{"dev", "0.86", "0.86.0-1-gabc+meta", "01.2.3", ""} {
		_, err := ModuleVersion(bad)
		require.Error(t, err, bad)
	}
}

func TestWriteProxy_Layout(t *testing.T) {
	files := exportForTest(t)
	dir := t.TempDir()
	at := time.Unix(0, 0)
	require.NoError(t, WriteProxy(dir, testModule, "v0.86.0", files, at))
	require.NoError(t, WriteProxy(dir, testModule, "v0.87.0-rc.1", files, at))
	require.NoError(t, WriteProxy(dir, testModule, "v0.86.0", files, at)) // re-run lists it once

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
	for i, zf := range zr.File {
		require.Equal(t, testModule+"@v0.86.0/"+files[i].Path, zf.Name)
	}

	again, err := moduleZip(testModule, "v0.86.0", files, at)
	require.NoError(t, err)
	require.Equal(t, data, again, "the same export must produce the same zip")
}

func TestEscapePath(t *testing.T) {
	require.Equal(t, "github.com/!azure/sdk", escapePath("github.com/Azure/sdk"))
}
