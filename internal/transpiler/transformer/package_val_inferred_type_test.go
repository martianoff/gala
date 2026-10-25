package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// packageValInferredTypeFixture is a module whose package p declares
// package-level vals typed only by inference from initializers the analyzer's
// cheap shapes do not cover: a generic function call, a method chain over
// another val, and a val reading a val of a sibling file.
func packageValInferredTypeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/pv\n\ngala dev\n")
	write("p/a.gala", `package p

import . "martianoff/gala/collection_immutable"

val Names = ArrayOf("a", "b")

val Loud = Names.Map((n) => n + "?")

val Lengths = Shouted.Map((s) => s.Size())
`)
	write("p/b.gala", `package p

import . "martianoff/gala/collection_immutable"

val Shouted = Names.Map((n) => n + "!")

func Bang() Array[string] = Names.Map((n) => n + "!")

func Quiet() Array[string] = Loud.Map((n) => n + ".")

func Total() int = Lengths.FoldLeft(0, (acc, n) => acc + n)
`)
	return root
}

// TestPackageValInferredTypeAcrossFiles: a package-level val whose type is
// only known by inferring its initializer carries that type to the other
// files of its package, and to importers, so a lambda passed to one of its
// methods takes its parameter type from it. It used to be recorded with no
// type outside its own file: `Names.Map((n) => ...)` failed with GALA-E0033
// "lambda parameter "n" has no type and none can be inferred from context".
func TestPackageValInferredTypeAcrossFiles(t *testing.T) {
	root := packageValInferredTypeFixture(t)

	t.Run("sibling file", func(t *testing.T) {
		path := filepath.Join(root, "p", "b.gala")
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		out, err := transpileCrossPkgFile(t, root, string(src), path)
		require.NoError(t, err)
		assert.Contains(t, out, "func(n string) string", "generated:\n%s", out)
	})

	t.Run("declaring file", func(t *testing.T) {
		path := filepath.Join(root, "p", "a.gala")
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		out, err := transpileCrossPkgFile(t, root, string(src), path)
		require.NoError(t, err)
		assert.Contains(t, out, "func(s string) int", "generated:\n%s", out)
	})

	t.Run("importing package", func(t *testing.T) {
		out, err := transpileCrossPkg(t, root, `package main

import "example.com/pv/p"

func main() {
    p.Names.Map((n) => n + "!").ForEach((s) => Println(s))
    Println(p.Lengths.FoldLeft(0, (acc, n) => acc + n))
}`)
		require.NoError(t, err)
		assert.Contains(t, out, "func(n string) string", "generated:\n%s", out)
	})
}
