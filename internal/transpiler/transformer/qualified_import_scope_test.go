package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qualifiedImportScopeFixture is a module whose package p declares a function
// Format while one of its files imports package d, which declares a type of
// the same name. A qualified import binds only its qualifier: d.Format is
// reachable as `d.Format`, never as a bare `Format`, in the importing file or
// any other file of the package.
func qualifiedImportScopeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/r\n\ngala dev\n")
	write("d/d.gala", `package d

sealed type Format {
    case Plain()
    case Fancy()
}
`)
	write("p/a.gala", `package p

import "example.com/r/d"

func Format(n int) string = s"<$n>"

func Style() d.Format = d.Plain()

func Thrice(n int) string = Format(n) + Format(n) + Format(n)
`)
	write("p/b.gala", `package p

func Twice(n int) string = Format(n) + Format(n)
`)
	// A zero-argument function named like a qualified import's interface:
	// a bare `Target()` is the function, never a construction of the type.
	write("codegen/codegen.gala", `package codegen

type Target interface {
    Generate(n int) string
}
`)
	write("golang/golang.gala", `package golang

import "example.com/r/codegen"

struct goTarget()

func (g goTarget) Generate(n int) string = s"go$n"

func Target() codegen.Target = goTarget()
`)
	write("golang/golang_test.gala", `package golang

import . "martianoff/gala/test"

func TestTarget(t T) T = Eq(t, Target().Generate(1), "go1")
`)
	// A method's signature may name a type its receiver type's file
	// dot-imports, even from a file that imports nothing.
	write("m/box.gala", `package m

import . "example.com/r/d"

struct Box(f Format)
`)
	write("m/methods.gala", `package m

func (b Box) Style() Format = b.f
`)
	return root
}

func TestQualifiedImportTypeDoesNotShadowPackageFunction(t *testing.T) {
	root := qualifiedImportScopeFixture(t)
	tests := []struct {
		file, want string
	}{
		// b.gala does not import d at all.
		{"p/b.gala", "return Format(n) + Format(n)"},
		// a.gala imports d, but only under its qualifier.
		{"p/a.gala", "return Format(n) + Format(n) + Format(n)"},
		// It used to lower as the struct literal `Target{}`: Go's "Target is
		// not a type".
		{"golang/golang_test.gala", "Target().Generate(1)"},
		// The receiver file's dot import resolves the signature's bare type.
		{"m/methods.gala", "Style() d.Format"},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(tc.file))
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			out, err := transpileCrossPkgFile(t, root, string(src), path)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want, "generated:\n%s", out)
		})
	}
}
