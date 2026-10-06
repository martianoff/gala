package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// privateFieldCtorFixture is a module whose lib package declares structs with
// unexported fields in every position a constructor call can reach: supplied,
// defaulted, required, and in the block form.
func privateFieldCtorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/privctor\n\ngala dev\n")
	write("lib/lib.gala", `package lib

struct Box(var N int = 0, var seen bool = false)

struct Email(v string)

struct Two(N int, hidden int)

struct Pub(N int, M int = 2)

type Blk struct {
    A int
    b int
}

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}
`)
	return root
}

// TestPrivateFieldConstructionIsRejected: constructing another package's struct
// by calling its type is GALA-E0043 whenever the literal would set a field that
// package keeps unexported. A defaulted unexported field used to slip through:
// the default was lowered into the literal (`lib.Box{N: 1, seen: false}`) and
// Go rejected it with "cannot refer to unexported field seen". The named and
// zero-argument forms missed the check altogether, and a required unexported
// field was reported as missing, though the caller has no way to supply it.
func TestPrivateFieldConstructionIsRejected(t *testing.T) {
	root := privateFieldCtorFixture(t)

	cases := []struct {
		name, call, field string
	}{
		{"defaulted private field, positional", "lib.Box(1)", "seen"},
		{"defaulted private field, named", "lib.Box(N = 1)", "seen"},
		{"defaulted private field, no arguments", "lib.Box()", "seen"},
		{"supplied private field, positional", `lib.Email("x")`, "v"},
		{"supplied private field, named", `lib.Email(v = "x")`, "v"},
		{"required private field left out", "lib.Two(1)", "hidden"},
		{"block form, private field named", "lib.Blk(b = 1)", "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import "example.com/privctor/lib"

func main() {
    val x = `+tc.call+`
    Println(x)
}`)
			require.Error(t, err)
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeTypeUsedAsConstructor))
			assert.Contains(t, msg, "is a type, not a constructor")
			assert.Contains(t, msg, `field "`+tc.field+`"`, "the hint must name the private field")
			assert.Contains(t, msg, "only package lib can construct it")
		})
	}
}

// TestSealedParentKeepsGenericHint: a sealed parent's layout carries a
// synthetic `_variant` field. Calling the parent type from another package is
// still GALA-E0043, but the hint must not name that field or offer the
// parent's zero value, which is no variant at all.
func TestSealedParentKeepsGenericHint(t *testing.T) {
	root := privateFieldCtorFixture(t)
	_, err := transpileCrossPkg(t, root, `package main

import "example.com/privctor/lib"

func main() {
    val s = lib.Shape(1, 2, 3, 4, 5)
    Println(s)
}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(galaerr.CodeTypeUsedAsConstructor))
	assert.NotContains(t, err.Error(), "_variant")
}

// TestExportedFieldConstructionStillWorks is the false-positive guard: a
// struct whose constructor sets only exported fields stays constructible from
// another package, a block-form struct may leave its private fields zero, and
// the composite literal keeps Go's partial semantics.
func TestExportedFieldConstructionStillWorks(t *testing.T) {
	root := privateFieldCtorFixture(t)

	cases := []struct {
		name, call, want string
	}{
		{"exported fields with a default", "lib.Pub(1)", "lib.Pub{N: std.NewImmutable(1), M: std.NewImmutable(2)}"},
		{"block form, exported field named", "lib.Blk(A = 1)", "lib.Blk{A: std.NewImmutable(1)}"},
		{"zero value literal", "lib.Box{}", "lib.Box{}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, `package main

import "example.com/privctor/lib"

func main() {
    val x = `+tc.call+`
    Println(x)
}`)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}
}
