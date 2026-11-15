package gala2lean_test

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gala2lean"
)

// unparsable is the examples the parser rejects (as in coreexport's tests).
var unparsable = map[string]bool{"examples/slice_type_param.gala": true}

// programs is every single-file program the tests translate: the galaspec
// fixtures' sources and the top-level examples.
func programs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for p, text := range stagedFiles(t, "formal/tests/src/", "examples/") {
		if dir := path.Dir(p); strings.HasSuffix(p, ".gala") && (dir == "examples" || dir == "formal/tests/src") && !unparsable[p] {
			out[p] = text
		}
	}
	require.Greater(t, len(out), 200, "//examples:all_gala_sources or //formal:fixture_sources is not staged")
	return out
}

// TestDeterministic translates every program twice, each from its own
// export, and requires byte-identical Lean (or the same report of what is
// not translated). It logs the share of examples that translate and what
// stops the rest.
func TestDeterministic(t *testing.T) {
	srcs := programs(t)
	translated, examples := 0, 0
	stops := map[string]int{}
	for _, p := range slices.Sorted(maps.Keys(srcs)) {
		opts := gala2lean.Options{Namespace: "GalaProgram", Main: true}
		r1, err1 := gala2lean.Translate(export(t, p, srcs[p]), opts)
		r2, err2 := gala2lean.Translate(export(t, p, srcs[p]), opts)
		if err1 != nil || err2 != nil {
			require.Equal(t, fmt.Sprint(err1), fmt.Sprint(err2), p)
			var u *gala2lean.UnsupportedError
			require.True(t, errors.As(err1, &u), "%s: %v", p, err1)
			if strings.HasPrefix(p, "examples/") {
				examples++
				stops[u.Construct()]++
			}
			continue
		}
		require.Equal(t, r1.Lean, r2.Lean, "%s: two translations differ", p)
		require.Equal(t, r1.Decls, r2.Decls, p)
		require.NotContains(t, r1.Lean, "partial def", "%s: every definition must have equations", p)
		if strings.HasPrefix(p, "examples/") {
			examples++
			translated++
		}
	}
	kinds := slices.SortedFunc(maps.Keys(stops), func(a, b string) int {
		return cmp.Or(cmp.Compare(stops[b], stops[a]), cmp.Compare(a, b))
	})
	var top []string
	for _, k := range kinds[:min(len(kinds), 15)] {
		top = append(top, fmt.Sprintf("%s %d", k, stops[k]))
	}
	t.Logf("gala2lean translates %d of %d examples (%.1f%%); the first construct not translated, by examples: %s",
		translated, examples, 100*float64(translated)/float64(examples), strings.Join(top, ", "))
}

// TestCommittedTranslation keeps the translation formal/Gala/Gala2Lean
// proves theorems about equal to what the translator writes, so that no
// proof rests on a hand edit. To update it after changing the translator:
//
//	bazel run //internal/formal/gala2lean/cmd/gala2lean -- -namespace Gala.Gala2Lean.Examples.Match \
//	    "$PWD/formal/tests/src/match.gala" > formal/Gala/Gala2Lean/Examples/Match.lean
//
// and check that the proofs in formal/Gala/Gala2Lean/Theorems.lean still go
// through.
func TestCommittedTranslation(t *testing.T) {
	files := stagedFiles(t, "formal/")
	src, ok := files["formal/tests/src/match.gala"]
	require.True(t, ok, "formal/tests/src/match.gala is not staged")
	committed, ok := files["formal/Gala/Gala2Lean/Examples/Match.lean"]
	require.True(t, ok, "formal/Gala/Gala2Lean/Examples/Match.lean is not staged")
	res, err := gala2lean.Translate(export(t, "match.gala", src), gala2lean.Options{Namespace: "Gala.Gala2Lean.Examples.Match"})
	require.NoError(t, err)
	require.Equal(t, res.Lean, committed,
		"formal/Gala/Gala2Lean/Examples/Match.lean is not the translator's output; regenerate it (see this test's comment)")
}

// TestUnsupportedIsNamed checks that a construct outside the translated
// subset is reported by name, and that nothing is translated then.
func TestUnsupportedIsNamed(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"import", "package main\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n", "import"},
		{"lambda without parameter types", "package main\nfunc main() { val f = (x) => x\n Println(1) }\n", "untyped-lambda-param"},
		{"method call", "package main\nfunc main() { Println(\"abc\".Size()) }\n", "method-call"},
		{"pointer method on a package-level var", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nvar g = C(0)\nfunc main() { g.Inc()\n Println(g.N) }\n", "ptr-escape"},
		{"pointer method on a captured var", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nfunc main() { var c = C(0)\n val f = () => c.N\n c.Inc()\n Println(f()) }\n", "ptr-escape"},
		{"receiver passed on", "package main\nstruct C(var N int)\nfunc show(c C) int = c.N\nfunc (c *C) Show() int = show(c)\nfunc main() { var c = C(0)\n Println(c.Show()) }\n", "ptr-escape"},
		{"pointer method on a val", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nfunc main() { val c = C(0)\n c.Inc()\n Println(c.N) }\n", "pointer-method-on-value"},
		{"inout receiver replaced whole", "package main\nstruct C(var N int)\nfunc (inout c C) Reset() { c = C(0) }\nfunc main() { var c = C(1)\n c.Reset()\n Println(c.N) }\n", "ptr-escape"},
		{"inout method on a val", "package main\nstruct C(var N int)\nfunc (inout c C) Inc() { c.N = c.N + 1 }\nfunc main() { val c = C(0)\n c.Inc()\n Println(c.N) }\n", "pointer-method-on-value"},
		{"printing a type with its own String", "package main\nstruct C(N int)\nfunc (c C) String() string = \"c\"\nfunc main() { Println(C(1)) }\n", "custom-string"},
		{"printing a type with its own Error", "package main\nstruct E(Msg string)\nfunc (e E) Error() string = \"x\"\nfunc main() { Println(E(\"a\")) }\n", "custom-string"},
		{"printing a type with a String the translation does not see", "package main\nstruct C(N int)\nfunc (c Ptr[C]) String() string = \"c\"\nfunc main() { Println(C(1)) }\n", "custom-string"},
		{"go builtin","package main\nfunc main() { println(1) }\n", "call-of-println"},
		{"return in an expression", "package main\nfunc f(n int) int { val x = if (n > 0) { return 1 } else 2\n x }\nfunc main() { Println(f(1)) }\n", "return-inside-expression"},
		{"no main", "package main\nfunc f() int = 1\n", "no-main"},
		{"nil function", "package main\nfunc main() { var f func() int\n Println(1) }\n", "nil"},
		{"printing a function", "package main\nfunc main() { val f = (x int) => x\n Println(f) }\n", "print-pointer"},
		{"a type holding a function of itself", "package main\nstruct Node(F func(Node) int)\nfunc main() { Println(1) }\n", "recursive-func-field"},
		{"a default that needs itself", "package main\nfunc f(n int = f()) int = n\nfunc main() { Println(f()) }\n", "recursive-default"},
		{"captured var of a function type", "package main\nfunc main() { var f = (x int) => x\n val g = () => f(1)\n Println(g()) }\n", "func-in-state"},
		{"untyped global", "package main\nval g = h()\nfunc h() (int, int) = (1, 2)\nfunc main() { Println(g) }\n", "go-results"},
		{"sealed without a default case as a zero value", "package main\nsealed type S { case A case B }\nfunc main() { var s S\n Println(s) }\n", "sealed-zero-value"},
		{"stable identifier of a type with its own Equal", "package main\nstruct P(X int, Y int)\nfunc (p P) Equal(o P) bool = p.X == o.X\nval Origin = P(0, 0)\n" +
			"func main() { val r = P(0, 1) match { case Origin => 1\n case _ => 2 }\n Println(r) }\n", "custom-equal"},
		{"a function type argument", "package main\nfunc id[T any](x T) T = x\nfunc main() { val f = id((x int) => x)\n Println(f(1)) }\n", "func-type-arg"},
		{"zero value of a type parameter", "package main\nfunc z[T any](x T) T { var y T\n y }\nfunc main() { Println(z(1)) }\n", "type-param-zero"},
		{"a generic function as a value", "package main\nfunc id[T any](x T) T = x\nfunc main() { val f = id\n Println(1) }\n", "generic-func-value"},
		{"a constraint other than any and comparable", "package main\nfunc id[T Hashable](x T) T = x\nfunc main() { Println(id(1)) }\n", "type-constraint"},
		{"a captured var of a type parameter", "package main\nfunc f[T any](x T) T { var y = x\n val g = () => y\n g() }\nfunc main() { Println(f(1)) }\n", "generic-in-state"},
		{"printing a type parameter beside a type with its own String", "package main\nstruct C(N int)\nfunc (c C) String() string = \"c\"\nfunc show[T any](x T) { Println(x) }\nfunc main() { show(1) }\n", "custom-string"},
		{"comparing a type parameter beside a type with its own Equal", "package main\nstruct C(N int)\nfunc (c C) Equal(o C) bool = true\nfunc same[T comparable](a T, b T) bool = a == b\nfunc main() { Println(same(1, 1)) }\n", "custom-equal"},
		{"printing a type parameter beside a type holding a function", "package main\ntype F struct { G func() int }\nfunc show[T any](x T) { Println(x) }\nfunc main() { show(1) }\n", "print-pointer"},
		{"an Option printed raw", "package main\ntype S struct { n Option[int] }\nfunc main() { var s S\n Println(s) }\n", "print-raw-generic"},
		{"a generic type's raw text", "package main\ntype S[T any] struct { n T }\nfunc main() { var s S[int]\n Println(s) }\n", "print-raw-generic"},
		{"a HashMap", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val m HashMap[int, string] = EmptyHashMap()\n Println(m.Size()) }\n", "hash-order"},
		{"building a HashSet", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(HashSetOf(1, 2).Size()) }\n", "hash-order"},
		{"an Array function not modelled", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(ArrayOf(2, 1).Sorted()) }\n", "collection-method"},
		{"an Array holding functions compared", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val f = (x int) => x\n Println(ArrayOf(f).Contains(f)) }\n", "func-in-equal"},
		{"Contains on a type parameter that is not comparable", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc has[T any](a Array[T], x T) bool = a.Contains(x)\nfunc main() { Println(has(ArrayOf(1), 1)) }\n", "type-param-equal"},
		{"another package of the standard library", "package main\nimport . \"martianoff/gala/collection_mutable\"\n\nfunc main() { Println(Thing(1)) }\n", "import"},
		{"a type nested in itself", "package main\nstruct Node(Next Option[Node])\nfunc main() { Println(1) }\n", "nested-recursive-type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := strings.Replace(c.src, "package main\n", "package main\n\n", 1)
			res, err := gala2lean.Translate(export(t, "main.gala", src), gala2lean.Options{Namespace: "P"})
			require.Nil(t, res)
			var u *gala2lean.UnsupportedError
			require.True(t, errors.As(err, &u), "%v", err)
			require.Equal(t, c.want, u.Construct(), "%v", err)
			require.Contains(t, err.Error(), c.want)
		})
	}
}

// TestCountTranslated checks what -stats counts as translated: not a
// function or method gala2lean does not translate, whether the report names
// it (an unsupported construct in its body) or it is outside the subset as a
// whole (a method with a Ptr[T] or generic receiver).
func TestCountTranslated(t *testing.T) {
	src := `package main

struct Counter(var N int)

struct Box[T any](Value T)

func (c Counter) Get() int = c.N

func (c Ptr[Counter]) Inc() { c.N = c.N + 1 }

func (b Box[T]) Get() T = b.Value

func id[T any](x T) T = x

func bad() int = println(1)

func takesTry(o Try[int]) int = 1

func main() {
    Println(Counter(1).Get())
}
`
	p := export(t, "main.gala", src)
	_, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Library: true})
	s := gala2lean.CountTranslated(p, err)
	// Counted: Get, Inc, Box.Get, id, bad, takesTry, main. Translated:
	// Counter.Get, id and main. bad is reported for its body, takesTry for
	// a type in its signature (its body would translate); Inc and Box.Get
	// have receivers outside the subset.
	require.Equal(t, gala2lean.Stats{Funcs: 7, Methods: 3, Translated: 3}, s, "%v", err)
}

// TestRecursion checks how functions are defined: one that calls itself, or
// a group that call each other, by partial_fixpoint (a mutual block for a
// group); any other by a plain def; callees before callers.
func TestRecursion(t *testing.T) {
	src := `package main

func even(n int) bool = if (n == 0) true else odd(n - 1)
func odd(n int) bool = if (n == 0) false else even(n - 1)
func fact(n int) int = if (n <= 1) 1 else n * fact(n - 1)
func twice(n int) int = n * 2

func main() {
    Println(even(4), fact(5), twice(3))
}
`
	res, err := gala2lean.Translate(export(t, "main.gala", src), gala2lean.Options{Namespace: "P"})
	require.NoError(t, err)
	var names []string
	rec := map[string]bool{}
	for _, d := range res.Decls {
		names = append(names, d.Name)
		rec[d.Name] = d.Recursive
	}
	require.Equal(t, []string{"even", "odd", "fact", "twice", "main", "<init>"}, names)
	require.Equal(t, map[string]bool{"even": true, "odd": true, "fact": true, "twice": false, "main": false, "<init>": false}, rec)
	require.Contains(t, res.Lean, "mutual\n\ndef even")
	require.Equal(t, 3, strings.Count(res.Lean, "\npartial_fixpoint\n"))
	require.NotContains(t, res.Lean, "partial def")
}

// TestInoutReceiver checks that a method with an inout receiver is
// translated by state passing, as a *T receiver's is: it returns its result
// with the updated receiver, and the caller writes that back.
func TestInoutReceiver(t *testing.T) {
	src := "package main\n\nstruct C(var N int)\nfunc (inout c C) Add(by int) int {\n    c.N = c.N + by\n    c.N\n}\nfunc main() {\n    var c = C(1)\n    val r = c.Add(2)\n    Println(r, c.N)\n}\n"
	res, err := gala2lean.Translate(export(t, "main.gala", src), gala2lean.Options{Namespace: "P"})
	require.NoError(t, err)
	require.Contains(t, res.Lean, "def C.Add (_gala_self : C) («by» : Int64) : _gala_M (Int64 × C)")
	require.Contains(t, res.Lean, "← (C.Add c (2 : Int64))")
}
