package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptrVariadicDecls declares, beside ptrReceiverDecls, a function and a method
// with a variadic `...Ptr[Counter]` parameter, each after a fixed one.
const ptrVariadicDecls = `func total(cs ...Ptr[Counter]) int = ArrayOf(cs...).FoldLeft(0, (acc, c) => acc + c.Count())

func labelled(label string, cs ...Ptr[Counter]) string = s"$label=${total(cs...)}"

struct Group(Name string)

func (g Group) Total(cs ...Ptr[Counter]) int = total(cs...)

func (g Group) Labelled(label string, cs ...Ptr[Counter]) string = labelled(label, cs...)
`

// ptrVariadicSource is a program of ptrReceiverDecls and ptrVariadicDecls
// whose main runs body.
func ptrVariadicSource(body string) string {
	return "package main\n\nimport . \"martianoff/gala/collection_immutable\"\nimport . \"martianoff/gala/go_interop\"\n\n" +
		ptrReceiverDecls + "\n" + ptrVariadicDecls + "\nfunc main() {\n    var c = Counter(1)\n    " + body + "\n}\n"
}

// TestPtrVariadicRejected checks that every argument filling a variadic
// `...Ptr[T]` parameter is checked against its element type (GALA-E0080), not
// only the first, for functions and methods alike (#823), and that a Go slice
// of nullable pointers spread into one is rejected as an `Array[*T]` is where
// an `Array[Ptr[T]]` is expected.
func TestPtrVariadicRejected(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name, body, msg string
	}{
		{name: "function, second argument", body: "Println(total(&c, lookup(false)))", msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "function, third argument", body: "Println(total(&c, &c, lookup(false)))", msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "function, nil after a Ptr", body: "Println(total(&c, nil))", msg: "cannot use nil as Ptr[Counter]"},
		{name: "function after a fixed parameter", body: `Println(labelled("n", &c, lookup(false)))`, msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "method, second argument", body: `Println(Group("g").Total(&c, lookup(false)))`, msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "method, third argument", body: `Println(Group("g").Total(&c, &c, lookup(false)))`, msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "method after a fixed parameter", body: `Println(Group("g").Labelled("n", &c, lookup(false)))`, msg: "cannot use lookup(false) (*Counter) as Ptr[Counter]"},
		{name: "function, spread slice of nullable pointers", body: "val xs = SliceOf(lookup(true), lookup(false))\n    Println(total(xs...))", msg: "cannot use xs ([]*Counter) as []Ptr[Counter]"},
		{name: "method, spread slice of nullable pointers", body: "val xs = SliceOf(lookup(true), lookup(false))\n    Println(Group(\"g\").Labelled(\"n\", xs...))", msg: "cannot use xs ([]*Counter) as []Ptr[Counter]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(ptrVariadicSource(tt.body), "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}
}

// TestPtrVariadicAllowed checks that Ptr values, and a spread slice of them,
// fill a variadic `...Ptr[T]` parameter.
func TestPtrVariadicAllowed(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name, body string
	}{
		{name: "no arguments", body: `Println(total(), Group("g").Total())`},
		{name: "Ptr arguments", body: `Println(total(&c, &c, RequirePtr(lookup(true))), Group("g").Labelled("n", &c, &c, &c))`},
		{name: "spread slice of Ptr", body: "val xs = SliceOf(&c, &c)\n    Println(total(xs...), Group(\"g\").Labelled(\"n\", xs...))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(ptrVariadicSource(tt.body), "")
			require.NoError(t, err)
		})
	}
}

// TestPtrVariadicCrossPackage checks the variadic arguments of a function and
// a method of another package (FunctionMetadata.Variadic, MethodMetadata.Variadic).
func TestPtrVariadicCrossPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/ptrvariadic\n\ngala dev\n")
	write("lib/lib.gala", `package lib

import . "martianoff/gala/collection_immutable"

struct Counter(var N int)

func (c Ptr[Counter]) Count() int = c.N

func Total(cs ...Ptr[Counter]) int = ArrayOf(cs...).Size()

struct Group(Name string)

func (g Group) Total(cs ...Ptr[Counter]) int = ArrayOf(cs...).Size()

func Lookup(found bool) *Counter = if (found) &Counter(1) else nil
`)
	tests := []struct {
		name, body, msg string
	}{
		{name: "function", body: "Println(lib.Total(&c, lib.Lookup(false)))", msg: "cannot use lib.Lookup(false) (*lib.Counter) as Ptr[lib.Counter]"},
		{name: "method", body: `Println(lib.Group("g").Total(&c, &c, lib.Lookup(false)))`, msg: "cannot use lib.Lookup(false) (*lib.Counter) as Ptr[lib.Counter]"},
		{name: "Ptr", body: `Println(lib.Total(&c, &c), lib.Group("g").Total(&c, &c))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, "package main\n\nimport \"example.com/ptrvariadic/lib\"\n\nfunc main() {\n    var c = lib.Counter(0)\n    "+tt.body+"\n}\n")
			if tt.msg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}
}

// TestVariadicTailArgsTakeElementType checks that every argument filling a
// variadic parameter, not only the first, is lowered against its element
// type: a lambda there takes its parameter types from it, and a later
// argument binds the type parameters an earlier lambda needs.
func TestVariadicTailArgsTakeElementType(t *testing.T) {
	trans := newTranspiler()
	_, err := trans.Transpile(`package main

import . "martianoff/gala/collection_immutable"

func pipe(fs ...func(int) int) int = ArrayOf(fs...).FoldLeft(1, (acc, f) => f(acc))

func label(name string, fs ...func(int) string) string = ArrayOf(fs...).FoldLeft(name, (acc, f) => acc + f(1))

struct Runner(N int)

func (r Runner) Pipe(fs ...func(int) int) int = pipe(fs...)

func sizes[T any](f func(T) int, xs ...Option[T]) int = ArrayOf(xs...).FoldLeft(0, (acc, o) => acc + o.Map(f).GetOrElse(0))

func main() {
    Println(sizes((s) => s.Size(), None(), Some("abc")))
    Println(pipe((x) => x + 1, (x) => x * 10, _ + 5))
    Println(label("n", (x) => s"$x", (x) => s"${x + 1}"))
    Println(Runner(0).Pipe((x) => x + 1, (x) => x * 10))
}`, "")
	require.NoError(t, err)
}
