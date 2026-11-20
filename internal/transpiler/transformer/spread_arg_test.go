package transformer_test

import (
	"maps"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestSpreadNotSlice pins GALA-E0138: a spread argument `xs...` must be a Go
// slice whose elements are of the variadic parameter's type, as Go requires.
// A GALA collection, a string, a number, or a slice of other elements used to
// pass every GALA check and fail go build against the generated call ("cannot
// use xs (variable of struct type collection_immutable.Array[int]) as []int
// value"), for a GALA or Go callee, by position or by name. The hint names the
// conversion the value's type has. A Go slice that fits keeps compiling.
func TestSpreadNotSlice(t *testing.T) {
	const decls = `package main

import (
    "errors"
    "fmt"
    "log"
    "os"
    "path/filepath"
    "slices"
    "sort"

    . "martianoff/gala/collection_immutable"
    "martianoff/gala/collection_mutable"
    "martianoff/gala/go_interop"
)

func count(xs ...int) int = xs.Size()

func label(prefix string, parts ...string) string = prefix + fmt.Sprint(parts.Size())

func gcount[T any](xs ...T) int = xs.Size()

func bytesOf(b ...byte) int = b.Size()

func runesOf(r ...rune) int = r.Size()

struct Counter(N int)

func (c Counter) Plus(x int, xs ...int) int = c.N + x + xs.Size()

struct Box[T any](V T)

func (b Box[T]) Count(extra int, xs ...T) int = xs.Size() + extra

func (b Box[T]) Tag[U any](lbl U, xs ...U) int = xs.Size()

struct Join()

func (j Join) Apply(parts ...string) string = ArrayOf(parts...).MkString(",")

struct Joiner(Sep string)

func (j Joiner) Apply(parts ...string) string = ArrayOf(parts...).MkString(j.Sep)

struct Pair()

func (p Pair) Apply(a string, b []string) int = b.Size()

struct Tags[T any]()

func (c Tags[T]) Apply(meta StructMeta[T], tags ...string) int = tags.Size()

struct Vals[V any]()

func (c Vals[V]) Apply(meta ValueMeta[V], xs ...V) int = xs.Size()

struct Bag[T any]()

func (b Bag[T]) Apply(xs ...T) int = xs.Size()

func pairs(a string, b []string) int = b.Size()

func concat(parts ...Array[int]) int = parts.Size()

func outer[U any](u U) int = Box(0).Tag("a", go_interop.SliceOf("b")...)

type Names []string

func touch() {
    Println(log.Ldate, os.Args.Size(), filepath.Separator, slices.Max(go_interop.SliceOf(1)), sort.StringSlice(nil).Len(),
        errors.ErrUnsupported, collection_mutable.EmptyArray[int]().Size())
}

func main() {
    touch()
`
	rejected := []struct{ name, body, want string }{
		{
			"an Array into a function",
			`val xs = ArrayOf(1, 2, 3)
    Println(count(xs...))`,
			"cannot spread `xs` of type collection_immutable.Array[int]: only a Go slice can be spread into a variadic parameter",
		},
		{
			"a List",
			"Println(count(ListOf(1, 2)...))",
			"spread the Go slice it converts to; here, `ListOf(1, 2).ToGoSlice()...`",
		},
		{
			"a HashSet",
			`val xs = HashSetOf(1)
    Println(count(xs...))`,
			"cannot spread `xs` of type collection_immutable.HashSet[int]",
		},
		{
			"a mutable Array",
			`val xs = collection_mutable.ArrayOf(1, 2)
    Println(count(xs...))`,
			"`xs.ToGoSlice()...`",
		},
		{
			"a struct with no Go slice",
			`val o = Some(1)
    Println(count(o...))`,
			"pass it without `...`, or spread a Go slice",
		},
		{
			"an int",
			`val n = 5
    Println(count(n...))`,
			"cannot spread `n` of type int: only a Go slice can be spread into a variadic parameter",
		},
		{
			"a string into a string parameter",
			`val s = "ab"
    Println(label("x", s...))`,
			"pass it as one argument, without `...`",
		},
		{
			"a string into a byte parameter",
			`val s = "ab"
    Println(bytesOf(s...))`,
			"`go_interop.ToBytes(s)...`",
		},
		{
			"a string into a rune parameter",
			`val s = "ab"
    Println(runesOf(s...))`,
			"`go_interop.ToRunes(s)...`",
		},
		{
			"a slice of other elements into a function",
			`val ws = go_interop.SliceOf("a")
    Println(count(ws...))`,
			"cannot spread `ws` of type []string into a variadic parameter of int: only a []int can be spread into it",
		},
		{
			"a slice of other elements into a generic function",
			`val ws = go_interop.SliceOf("a")
    Println(ArrayOf[int](ws...))`,
			"cannot spread `ws` of type []string into a variadic parameter of int",
		},
		{
			"an Array into a generic function",
			"Println(gcount(ArrayOf(1)...))",
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"an Array into a method",
			`val xs = ArrayOf(1, 2)
    Println(Counter(1).Plus(1, xs...))`,
			"cannot spread `xs` of type collection_immutable.Array[int]",
		},
		{
			"a slice of other elements into a method",
			`val ws = go_interop.SliceOf("a")
    Println(Counter(1).Plus(1, ws...))`,
			"cannot spread `ws` of type []string into a variadic parameter of int",
		},
		{
			"an Array into a method of a generic type",
			"Println(Box(0).Count(1, ArrayOf(1)...))",
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"an Array into a generic method",
			"Println(Box(0).Tag(1, ArrayOf(1)...))",
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"an Array into a companion Apply",
			"Println(Join(ArrayOf(\"a\")...))",
			"cannot spread `ArrayOf(\"a\")` of type collection_immutable.Array[string]",
		},
		{
			"a slice of other elements into a companion Apply",
			"Println(Join(go_interop.SliceOf(1)...))",
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string",
		},
		{
			"an Array into a Go function",
			`val xs = ArrayOf(1, 2)
    fmt.Println(xs...)`,
			"cannot spread `xs` of type collection_immutable.Array[int]",
		},
		{
			"a slice of strings into a Go `...any`",
			`val ws = go_interop.SliceOf("a")
    fmt.Println(ws...)`,
			"cannot spread `ws` of type []string into a variadic parameter of any: only a []any can be spread into it",
		},
		{
			"a slice of other elements into a Go function",
			`val ws = go_interop.SliceOf("a")
    Println(errors.Join(ws...))`,
			"cannot spread `ws` of type []string into a variadic parameter of error",
		},
		{
			"an Array into a Go method",
			`val l = log.New(os.Stdout, "", 0)
    l.Println(ArrayOf(1)...)`,
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"a slice of other elements into a Go method",
			`val l = log.New(os.Stdout, "", 0)
    l.Println(go_interop.SliceOf(1)...)`,
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of any",
		},
		{
			"an Array by name into a function",
			"Println(label(prefix = \"r\", parts = ArrayOf(\"a\")...))",
			"cannot spread `ArrayOf(\"a\")` of type collection_immutable.Array[string]",
		},
		{
			"a slice of other elements by name into a function",
			"Println(label(prefix = \"r\", parts = go_interop.SliceOf(1)...))",
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string",
		},
		{
			"an Array by name into a method",
			"Println(Counter(1).Plus(x = 1, xs = ArrayOf(1)...))",
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"a slice of other elements by name into a method of a generic type",
			"Println(Box(0).Count(extra = 1, xs = go_interop.SliceOf(\"a\")...))",
			"cannot spread `go_interop.SliceOf(\"a\")` of type []string into a variadic parameter of int",
		},
		{
			"an Array by name into a generic method",
			"Println(Box(0).Tag(lbl = 1, xs = ArrayOf(1)...))",
			"cannot spread `ArrayOf(1)` of type collection_immutable.Array[int]",
		},
		{
			"an Array by name into a Go function",
			"Println(filepath.Join(elem = ArrayOf(\"a\")...))",
			"cannot spread `ArrayOf(\"a\")` of type collection_immutable.Array[string]",
		},
		{
			"a slice of other elements by name into a Go function",
			"Println(filepath.Join(elem = go_interop.SliceOf(1)...))",
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string",
		},
		{
			"a slice of other elements into the Apply of a value",
			`val j = Joiner("-")
    Println(j(go_interop.SliceOf(1)...))`,
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string",
		},
		{
			"a slice of other elements into the Apply of a construction",
			"Println(Joiner(\",\")(go_interop.SliceOf(1)...))",
			"cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string",
		},
		{
			"a string into the Apply of a value, hint",
			`val j = Joiner("-")
    Println(j("a"...))`,
			"pass it as one argument, without `...`",
		},
		{
			"a collection whose Go slice does not fit the Apply of a value, hint",
			`val j = Joiner("-")
    Println(j(ArrayOf(1)...))`,
			"(hint: pass it without `...`, or spread a Go slice)",
		},
		{
			"a collection of the element type, hint",
			`val xs = ArrayOf(1)
    Println(concat(xs...))`,
			"pass it as one argument, without `...`",
		},
		{
			"a collection whose Go slice does not fit, hint",
			"Println(count(ArrayOf(\"a\")...))",
			"(hint: pass it without `...`, or spread a Go slice)",
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+"    "+tc.body+"\n}\n")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeSpreadNotSlice))
			require.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct{ name, body string }{
		{"a Go slice", "Println(count(go_interop.SliceOf(1, 2)...))"},
		{"a collection's Go slice", `val xs = ArrayOf(1, 2)
    Println(count(xs.ToGoSlice()...))`},
		{"a mutable collection's Go slice", `val xs = collection_mutable.ArrayOf(1, 2)
    Println(count(xs.ToGoSlice()...))`},
		{"a string's bytes", "Println(bytesOf(go_interop.ToBytes(\"ab\")...))"},
		{"a Go slice into a generic function", "Println(ArrayOf(go_interop.SliceOf(1, 2)...).Size(), gcount(go_interop.SliceOf(1)...))"},
		{"a Go slice into methods", "Println(Counter(1).Plus(1, go_interop.SliceOf(2)...), Box(0).Count(1, go_interop.SliceOf(1)...), Box(0).Tag(1, go_interop.SliceOf(2)...))"},
		{"a Go slice into a companion Apply", "Println(Join(go_interop.SliceOf(\"a\", \"b\")...))"},
		{"a slice of any into a Go function", "fmt.Println(go_interop.SliceOf[any](1, \"a\")...)"},
		{"a slice of errors into a Go function", "Println(errors.Join(go_interop.SliceOf(errors.New(\"x\"))...))"},
		{"a slice of any into a Go method", `val l = log.New(os.Stdout, "", 0)
    l.Println(go_interop.SliceOf[any]("a", 1)...)`},
		{"a Go named slice type", "Println(filepath.Join(sort.StringSlice(go_interop.SliceOf(\"b\", \"a\"))...))"},
		{"a GALA alias of a Go slice", `val ns = Names(go_interop.SliceOf("a"))
    Println(label("x", ns...))`},
		{"a slice into a generic Go function", "Println(slices.Concat(go_interop.SliceOf(go_interop.SliceOf(1), go_interop.SliceOf(2))...))"},
		{"Go slices by name", "Println(label(prefix = \"r\", parts = go_interop.SliceOf(\"a\")...), filepath.Join(elem = go_interop.SliceOf(\"a\", \"b\")...), Counter(1).Plus(x = 1, xs = go_interop.SliceOf(3)...))"},
		{"nil", "Println(count(nil...))"},
		{"a Go slice into a generic companion Apply", "Println(Bag(go_interop.SliceOf(1, 2)...))"},
		{"a Go slice into the Apply of a construction", `Println(Joiner(",")(go_interop.SliceOf("a", "b")...))`},
		{"a Go slice into the Apply of a value", `val j = Joiner("-")
    Println(j(go_interop.SliceOf("a", "b")...))`},
		{"a Go slice into a companion Apply that takes injected metadata", `Println(Tags[Counter](go_interop.SliceOf("a")...))`},
		{"a Go slice binding the type argument of a companion Apply that takes injected metadata", `Println(Vals(go_interop.SliceOf(1, 2)...))`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+"    "+tc.body+"\n}\n")
			require.NoError(t, err)
		})
	}

	// A spread at a parameter that is not variadic is told so (checkSpreads),
	// not compared against that parameter's type.
	misplaced := []struct{ name, body, want string }{
		{"after an argument of the Apply of a construction", `Println(Joiner(",")("a", go_interop.SliceOf("b")...))`, `a spread argument must be the only argument of variadic parameter "parts" of Joiner.Apply`},
		{"a slice parameter", `val ws = go_interop.SliceOf("a")
    Println(pairs("x", ws...))`, `a spread argument cannot fill parameter "b" of pairs`},
		{"after a variadic argument", "Println(label(\"r:\", \"a\", go_interop.SliceOf(\"b\")...))", `a spread argument must be the only argument of variadic parameter "parts" of label`},
		{"a slice parameter by name", "Println(pairs(a = \"x\", b = go_interop.SliceOf(\"a\")...))", `a spread argument cannot fill parameter "b" of pairs`},
		{"followed by another argument", "Println(count(go_interop.SliceOf(1)..., 2))", `a spread argument must be the only argument of variadic parameter "xs" of count`},
		{"a companion Apply's slice parameter", "Println(Pair(\"x\", go_interop.SliceOf(\"a\")...))", `a spread argument cannot fill parameter "b" of Pair.Apply`},
		{"after an argument of a companion Apply's variadic parameter", "Println(Join(\"a\", go_interop.SliceOf(\"b\")...))", `a spread argument must be the only argument of variadic parameter "parts" of Join.Apply`},
		{"after an argument of a value's Apply", `val j = Joiner("-")
    Println(j("a", go_interop.SliceOf("b")...))`, `a spread argument must be the only argument of variadic parameter "parts" of Joiner.Apply`},
	}
	for _, tc := range misplaced {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+"    "+tc.body+"\n}\n")
			require.Error(t, err)
			require.NotContains(t, err.Error(), string(galaerr.CodeSpreadNotSlice))
			require.Contains(t, err.Error(), tc.want)
		})
	}

	// A spread binds a generic companion Apply's type argument through its
	// injected metadata parameter.
	out, err := transpileBareVariant(t, decls+"    Println(Vals(go_interop.SliceOf(1, 2)...))\n}\n")
	require.NoError(t, err)
	require.Contains(t, out, "Vals[int]{}.Apply(_ValueMeta_int")
	// Through the slice a GALA alias names, too.
	out, err = transpileBareVariant(t, decls+`    val ns = Names(go_interop.SliceOf("a"))
    Println(Vals(ns...))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "Vals[string]{}.Apply(_ValueMeta_string")

	// A spread into the Apply of another package's type is checked against
	// that type's Apply, not a same-named one of this package.
	lib := map[string]string{
		"go.mod":       "module example.com/spreads\n\ngo 1.25\n",
		"gala.mod":     "module example.com/spreads\n",
		"lib/lib.gala": "package lib\n\nstruct Joiner(Sep string)\n\nfunc (j Joiner) Apply(parts ...string) int = parts.Size()\n",
	}
	for _, tc := range []struct {
		name, call, want string
		coded            bool // the error is GALA-E0138, not a placement error
	}{
		{"a fitting slice", `lib.Joiner(",")(go_interop.SliceOf("a")...)`, "", false},
		{"a slice of other elements", `lib.Joiner(",")(go_interop.SliceOf(1)...)`, "into a variadic parameter of string", true},
		{"after an argument", `lib.Joiner(",")("a", go_interop.SliceOf("b")...)`, `must be the only argument of variadic parameter "parts" of lib.Joiner.Apply`, false},
	} {
		t.Run("another package's Apply: "+tc.name, func(t *testing.T) {
			files := maps.Clone(lib)
			files["main.gala"] = `package main

import (
    "example.com/spreads/lib"
    "martianoff/gala/go_interop"
)

struct Joiner()

func (j Joiner) Apply(xs ...int) int = xs.Size()

func main() {
    Println(Joiner()(go_interop.SliceOf(1)...), ` + tc.call + `)
}
`
			_, err := transpileInModule(t, files, "main.gala")
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			require.Equal(t, tc.coded, strings.Contains(err.Error(), string(galaerr.CodeSpreadNotSlice)))
		})
	}

	// Under an import alias, the Apply is called and checked, and an error
	// names the type as the file does: for a construction and for a val of it,
	// beside a same-named type of this package.
	for _, tc := range []struct{ name, body, want string }{
		{"a fitting slice", `Println(l.Joiner(",")(go_interop.SliceOf("a")...))`, `l.Joiner{Sep: std.NewImmutable(",")}.Apply(go_interop.SliceOf("a")...)`},
		{"a slice of other elements", `Println(l.Joiner(",")(go_interop.SliceOf(1)...))`, string(galaerr.CodeSpreadNotSlice)},
		{"after an argument", `Println(l.Joiner(",")("a", go_interop.SliceOf("b")...))`, `variadic parameter "parts" of l.Joiner.Apply`},
		{"after an argument, through a val", `val j = l.Joiner(",")
    Println(j("a", go_interop.SliceOf("b")...))`, `variadic parameter "parts" of l.Joiner.Apply`},
	} {
		t.Run("an aliased package's Apply: "+tc.name, func(t *testing.T) {
			files := maps.Clone(lib)
			files["main.gala"] = `package main

import (
    l "example.com/spreads/lib"
    "martianoff/gala/go_interop"
)

struct Joiner()

func (j Joiner) Apply(xs ...int) int = xs.Size()

func main() {
    Println(Joiner()(go_interop.SliceOf(1)...))
    ` + tc.body + `
}
`
			out, err := transpileInModule(t, files, "main.gala")
			if tc.name == "a fitting slice" {
				require.NoError(t, err)
				require.Contains(t, out, tc.want)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	// A variadic parameter forwarded, or a type parameter's slice spread into
	// a generic callee, fits whatever the type argument is.
	_, err = transpileBareVariant(t, `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/go_interop"
)

func wrap[T any](xs []T) Array[T] = ArrayOf(xs...)

func count(xs ...int) int = xs.Size()

func forward(ns ...int) int = count(ns...)

func main() {
    Println(wrap(go_interop.SliceOf(1, 2)).Size(), forward(1, 2, 3))
}
`)
	require.NoError(t, err)
}
