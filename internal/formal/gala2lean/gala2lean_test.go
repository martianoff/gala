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

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
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
		{"import", "package main\nimport \"strings\"\n\nfunc main() { Println(strings.ToUpper(\"a\")) }\n", "import"},
		{"a blank import of a package it is not given", "package main\nimport _ \"example.com/m/lib\"\n\nfunc main() { Println(1) }\n", "blank-import"},
		{"lambda without parameter types", "package main\nfunc main() { val f = (x) => x\n Println(1) }\n", "untyped-lambda-param"},
		{"method call", "package main\nfunc main() { Println(\"abc\".Size()) }\n", "method-call"},
		{"an Option method not modelled", "package main\nfunc main() { Println(Some(1).Hash()) }\n", "option-method"},
		{"an Either method not modelled", "package main\nfunc main() { val e Either[string, int] = Right(1)\n Println(e.Hash()) }\n", "option-method"},
		{"a case test of a generic sealed type", "package main\nsealed type S[T any] {\n case A(V T)\n case B()\n}\nfunc main() { val s S[int] = A(1)\n Println(s.isA()) }\n", "method-call"},
		{"a case test whose name a field holds", "package main\nsealed type S {\n case A(isA bool)\n case B()\n}\nfunc main() { val s S = B()\n Println(s.isA()) }\n", "method-call"},
		{"pointer method on a package-level var", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nvar g = C(0)\nfunc main() { g.Inc()\n Println(g.N) }\n", "ptr-escape"},
		{"pointer method on a captured var", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nfunc main() { var c = C(0)\n val f = () => c.N\n c.Inc()\n Println(f()) }\n", "ptr-escape"},
		{"receiver passed on", "package main\nstruct C(var N int)\nfunc show(c C) int = c.N\nfunc (c *C) Show() int = show(c)\nfunc main() { var c = C(0)\n Println(c.Show()) }\n", "ptr-escape"},
		{"pointer method on a val", "package main\nstruct C(var N int)\nfunc (c *C) Inc() { c.N = c.N + 1 }\nfunc main() { val c = C(0)\n c.Inc()\n Println(c.N) }\n", "pointer-method-on-value"},
		{"inout receiver captured", "package main\nstruct C(var N int)\nfunc (inout c C) Later() int { val f = () => c\n f().N }\nfunc main() { var c = C(1)\n Println(c.Later()) }\n", "ptr-escape"},
		{"inout parameter captured", "package main\nstruct C(var N int)\nfunc later(inout c C) int { val f = () => c.N\n f() }\nfunc main() { var c = C(1)\n Println(later(&c)) }\n", "ptr-escape"},
		{"a package-level var lent", "package main\nstruct C(var N int)\nfunc bump(inout c C) { c.N = c.N + 1 }\nvar g = C(0)\nfunc main() { bump(&g)\n Println(g.N) }\n", "ptr-escape"},
		{"a captured var lent", "package main\nstruct C(var N int)\nfunc bump(inout c C) { c.N = c.N + 1 }\nfunc main() { var c = C(0)\n val f = () => c.N\n bump(&c)\n Println(f()) }\n", "ptr-escape"},
		{"an inout parameter captured by a later default", "package main\nstruct C(var N int)\nfunc f(inout c C, get func() int = () => c.N) int { c.N = 5\n get() }\nfunc main() { var c = C(1)\n Println(f(&c)) }\n", "ptr-escape"},
		{"an inout parameter with a default", "package main\nfunc inc(inout n int = 0) { n = n + 1 }\nfunc main() { inc()\n Println(1) }\n", "param-default"},
		{"one place lent twice", "package main\nfunc both(inout a int, inout b int) { a = a + b }\nfunc main() { var n = 1\n both(&n, &n)\n Println(n) }\n", "ptr-escape"},
		{"a place and its field lent", "package main\nstruct C(var N int)\nfunc both(inout c C, inout n int) { n = c.N }\nfunc main() { var c = C(1)\n both(&c, &c.N)\n Println(c.N) }\n", "ptr-escape"},
		{"an inout argument without &", "package main\nfunc inc(inout n int) { n = n + 1 }\nfunc main() { var n = 0\n inc(n)\n Println(n) }\n", "inout-arg"},
		{"a val lent", "package main\nfunc inc(inout n int) { n = n + 1 }\nfunc main() { val n = 0\n inc(&n)\n Println(n) }\n", "inout-arg"},
		{"an inout call inside an expression on a local var", "package main\nfunc inc(inout n int) int { n = n + 1\n n }\nfunc main() { var n = 0\n Println(inc(&n)) }\n", "var-mutated-inside-expression"},
		{"an inout parameter of a method", "package main\nstruct C(var N int)\nfunc (c C) Put(inout n int) { n = c.N }\nfunc main() { var n = 0\n C(1).Put(&n)\n Println(n) }\n", "inout-method-param"},
		{"a function with inout parameters as a value", "package main\nfunc inc(inout n int) { n = n + 1 }\nfunc main() { val f = inc\n Println(1) }\n", "inout-func-value"},
		{"a lambda with an inout parameter", "package main\nfunc main() { val f = (inout n int) => { n = n + 1 }\n Println(1) }\n", "inout-lambda-param"},
		{"a function type with an inout parameter", "package main\nfunc ap(inout n int, f func(inout int)) { f(&n) }\nfunc main() { var n = 0\n Println(n) }\n", "param-modifier"},
		{"a generic inout parameter", "package main\nfunc put[T any](inout s T, v T) { s = v }\nfunc main() { var n = 0\n put(&n, 1)\n Println(n) }\n", "generic-in-state"},
		{"a Ptr taken", "package main\nstruct C(var N int)\nfunc main() { var c = C(0)\n val p = &c\n Println(c.N) }\n", "address-of"},
		{"inout method on a val", "package main\nstruct C(var N int)\nfunc (inout c C) Inc() { c.N = c.N + 1 }\nfunc main() { val c = C(0)\n c.Inc()\n Println(c.N) }\n", "pointer-method-on-value"},
		{"printing a type with its own String", "package main\nstruct C(N int)\nfunc (c C) String() string = \"c\"\nfunc main() { Println(C(1)) }\n", "custom-string"},
		{"printing a type with its own Error", "package main\nstruct E(Msg string)\nfunc (e E) Error() string = \"x\"\nfunc main() { Println(E(\"a\")) }\n", "custom-string"},
		{"printing a type with a String the translation does not see", "package main\nstruct C(N int)\nfunc (c Ptr[C]) String() string = \"c\"\nfunc main() { Println(C(1)) }\n", "custom-string"},
		{"a Ptr method on a val", "package main\nstruct C(var N int)\nfunc (c Ptr[C]) Inc() { c.N = c.N + 1 }\nfunc main() { val c = C(0)\n c.Inc()\n Println(c.N) }\n", "pointer-method-on-value"},
		{"a Ptr method on a captured var", "package main\nstruct C(var N int)\nfunc (c Ptr[C]) Inc() { c.N = c.N + 1 }\nfunc main() { var c = C(0)\n val f = () => c.N\n c.Inc()\n Println(f()) }\n", "ptr-escape"},
		{"a Ptr receiver passed on", "package main\nstruct C(var N int)\nfunc show(c C) int = c.N\nfunc (c Ptr[C]) Show() int = show(c)\nfunc main() { var c = C(0)\n Println(c.Show()) }\n", "ptr-escape"},
		{"go builtin", "package main\nfunc main() { println(1) }\n", "call-of-println"},
		{"return in an expression", "package main\nfunc f(n int) int { val x = if (n > 0) { return 1 } else 2\n x }\nfunc main() { Println(f(1)) }\n", "return-inside-expression"},
		{"no main", "package main\nfunc f() int = 1\n", "no-main"},
		{"nil function", "package main\nfunc main() { var f func() int\n Println(1) }\n", "nil"},
		{"printing a function", "package main\nfunc main() { val f = (x int) => x\n Println(f) }\n", "print-pointer"},
		{"a type holding a function of itself", "package main\nstruct Node(F func(Node) int)\nfunc main() { Println(1) }\n", "recursive-func-field"},
		{"a default that needs itself", "package main\nfunc f(n int = f()) int = n\nfunc main() { Println(f()) }\n", "recursive-default"},
		{"captured var of a function type", "package main\nfunc main() { var f = (x int) => x\n val g = () => f(1)\n Println(g()) }\n", "func-in-state"},
		{"untyped global", "package main\nval g = h()\nfunc h() (int, int) = (1, 2)\nfunc main() { Println(g) }\n", "go-results"},
		{"sealed without a default case as a zero value", "package main\nsealed type S { case A case B }\nfunc main() { var s S\n Println(s) }\n", "sealed-zero-value"},
		{"a default value with a field default", "package main\nstruct S(N int = 1)\nfunc main() { var s S\n Println(s) }\n", "field-default-value"},
		{"a default case with a field default, nested", "package main\nsealed type L { case Off()\n default case On(P int = 30) }\nstruct H(L L)\nvar h H\nfunc main() { Println(h) }\n", "field-default-value"},
		{"stable identifier of a type with its own Equal", "package main\nstruct P(X int, Y int)\nfunc (p P) Equal(o P) bool = p.X == o.X\nval Origin = P(0, 0)\n" +
			"func main() { val r = P(0, 1) match { case Origin => 1\n case _ => 2 }\n Println(r) }\n", "custom-equal"},
		{"a function type argument", "package main\nfunc id[T any](x T) T = x\nfunc main() { val f = id((x int) => x)\n Println(f(1)) }\n", "func-type-arg"},
		{"zero value of a type parameter", "package main\nfunc z[T any](x T) T { var y T\n y }\nfunc main() { Println(z(1)) }\n", "type-param-zero"},
		{"ArrayTabulate with the mutable collections dot-imported", "package main\nimport . \"martianoff/gala/collection_immutable\"\nimport . \"martianoff/gala/collection_mutable\"\n\nfunc main() { Println(ArrayTabulate(2, (i int) => i)) }\n", "import"},
		{"a conversion of a value that is not an int", "package main\nfunc main() { Println(int64(true)) }\n", "conversion"},
		{"a bracket on a HashSet builder", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(HashSetOf[int](1).Size()) }\n", "hash-order"},
		{"a bracket in a default", "package main\nfunc pick[T any](x T, d Option[T] = None[T]()) T = x\nfunc main() { Println(pick(1)) }\n", "index"},
		{"a bracket on a package-level value", "package main\nval fs = 1\nfunc main() { Println(fs[0](1)) }\n", "index"},
		{"a bracket on a local before a call", "package main\nfunc main() { val f = (x int) => x\n Println(f[0](1)) }\n", "index"},
		{"a bracket that is not a type", "package main\nfunc id[T any](x T) T = x\nfunc main() { Println(id[1 + 1](1)) }\n", "index"},
		{"a bracket on a function that is not generic", "package main\nfunc inc(x int) int = x + 1\nfunc main() { Println(inc[int](1)) }\n", "index"},
		{"a generic function with a bracket, as a value", "package main\nfunc id[T any](x T) T = x\nfunc main() { val f = id[int]\n Println(f(1)) }\n", "index"},
		{"a string literal that is not valid UTF-8", "package main\nfunc main() { Println(\"a\\xffb\") }\n", "invalid-utf8"},
		{"an interpolated run that is not valid UTF-8", "package main\nfunc main() { Println(s\"a\\xff${1}\") }\n", "invalid-utf8"},
		{"GALA's strings package, not Go's", "package main\nimport \"martianoff/gala/strings\"\n\nfunc main() { Println(strings.Contains(\"ab\", \"a\")) }\n", "import"},
		{"strings also bound in the file (conservative)", "package main\nimport \"strings\"\n\nstruct S(N int)\nfunc f(strings S) int = strings.N\nfunc main() { Println(strings.TrimSuffix(\"ab\", \"b\")) }\n", "import"},
		{"two imports named strings in one file", "package main\nimport \"strings\"\nimport strings \"strconv\"\n\nfunc main() { Println(strings.Itoa(1)) }\n", "import"},
		{"strings also the conventional name of another import", "package main\nimport \"strings\"\nimport \"gopkg.in/strings.v1\"\n\nfunc main() { Println(strings.TrimSuffix(\"ab\", \"b\")) }\n", "import"},
		{"a SortWith less that prints", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(ArrayOf(2, 1).SortWith((a, b) => { Println(a)\n a < b })) }\n", "sort-less-not-pure"},
		{"a SortWith less that calls", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc lt(a int, b int) bool = a < b\nfunc main() { Println(ArrayOf(2, 1).SortWith((a, b) => lt(a, b))) }\n", "sort-less-not-pure"},
		{"a SortWith less that may divide by zero", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val d = 0\n Println(ArrayOf(2, 1).SortWith((a, b) => a / d < b)) }\n", "sort-less-not-pure"},
		{"a SortWith less that divides by a zero literal", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(ArrayOf(2, 1).SortWith((a, b) => a % 0x0 < b)) }\n", "sort-less-not-pure"},
		{"a SortWith less that interpolates", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(ArrayOf(2, 1).SortWith((a, b) => s\"$a\" < s\"$b\")) }\n", "sort-less-not-pure"},
		{"a SortWith less comparing values with a custom Equal", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct P(N int)\nfunc (p P) Equal(o P) bool = true\nfunc main() { Println(ArrayOf(P(2), P(1)).SortWith((a, b) => a == b).Size()) }\n", "custom-equal"},
		{"a SortWith less passed as a value","package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val lt = (a int, b int) => a < b\n Println(ArrayOf(2, 1).SortWith(lt)) }\n", "sort-less-not-pure"},
		{"a strings.Replacer held as a value", "package main\nimport \"strings\"\n\nfunc main() { val r = strings.NewReplacer(\"a\", \"b\")\n Println(r.Replace(\"a\")) }\n", "import"},
		{"a spread of a GALA list into NewReplacer", "package main\nimport \"strings\"\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(strings.NewReplacer(ListOf(\"a\", \"b\")...).Replace(\"a\")) }\n", "spread-arg"},
		{"a constant outside int64", "package main\nfunc main() { Println(1 << 70) }\n", "untyped-constant"},
		{"an intermediate constant over 512 bits", "package main\nfunc main() { Println((1 << 1000) >> 998) }\n", "untyped-constant"},
		{"a constant shift count above Go's bound", "package main\nfunc main() { Println(0 << 1075) }\n", "untyped-constant"},
		{"negating the int64 minimum", "package main\nfunc main() { Println(-(-9223372036854775808)) }\n", "untyped-constant"},
		{"an order on constant bools", "package main\nfunc main() { Println(true < false) }\n", "untyped-constant"},
		{"constants of different kinds", "package main\nfunc main() { Println(1 + \"a\") }\n", "untyped-constant"},
		{"an int literal one past int64","package main\nfunc main() { Println(9223372036854775808) }\n", "untyped-constant"},
		{"a constant division by zero", "package main\nfunc main() { Println(1 / 0) }\n", "untyped-constant"},
		{"a negative constant shift count in a larger expression", "package main\nfunc main() { val x = 2\n Println(x + (1 << -1)) }\n", "untyped-constant"},
		{"a placeholder lambda of two parameters", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val t = ListOf(1, 2).FoldLeft(0, _ + _)\n Println(t) }\n", "placeholder-lambda"},
		{"a placeholder in an argument of a call inside the argument", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc inc(n int) int = n + 1\nfunc main() { val ys = ListOf(1).Map(inc(_))\n Println(ys) }\n", "placeholder-lambda"},
		{"a placeholder argument inside another call's argument", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { Println(ListOf(1).Map(_ + 1)) }\n", "placeholder-lambda"},
		{"a placeholder for a slot that is not a function", "package main\nfunc inc(n int) int = n + 1\nfunc main() { val y = inc(_ + 1)\n Println(y) }\n", "placeholder-lambda"},
		{"a placeholder inside an interpolated string", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val ys = ArrayOf(1).Map(s\"v${_}\")\n Println(ys) }\n", "placeholder-lambda"},
		{"one placeholder for a function of two parameters","package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() { val t = ListOf(1, 2).FoldLeft(0, _ + 1)\n Println(t) }\n", "placeholder-lambda"},
		{"fmt.Println of an Option", "package main\nimport \"fmt\"\n\nfunc main() { fmt.Println(Some(1)) }\n", "fmt-operand"},
		{"fmt.Println as a value", "package main\nimport \"fmt\"\n\nfunc main() { val r = fmt.Println(1)\n Println(r.IsSuccess()) }\n", "fmt-value"},
		{"fmt.Println through an alias of another package", "package main\nimport fmt \"strings\"\n\nfunc main() { fmt.Println(1) }\n", "import"},
		{"fmt hidden by a local", "package main\nimport \"fmt\"\n\nstruct P(N int)\nfunc main() { val fmt = P(1)\n fmt.Println(1) }\n", "method-call"},
		{"fmt.Printf", "package main\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\", 1) }\n", "import"},
		{"a variadic parameter printed", "package main\nfunc f(xs ...int) { Println(xs) }\nfunc main() { f(1) }\n", "slice-value"},
		{"a variadic parameter passed whole", "package main\nfunc g(n int) int = n\nfunc f(xs ...int) int = g(xs)\nfunc main() { Println(f(1)) }\n", "slice-value"},
		{"a variadic parameter captured", "package main\nfunc f(xs ...int) int { val h = () => xs\n 1 }\nfunc main() { Println(f(1)) }\n", "slice-value"},
		{"a variadic parameter indexed", "package main\nfunc f(xs ...int) int = xs[0]\nfunc main() { Println(f(1)) }\n", "index"},
		{"a spread of what is not a variadic parameter", "package main\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc f(xs ...int) int = xs.Size()\nfunc main() { Println(f(ArrayOf(1).ToGoSlice()...)) }\n", "spread-arg"},
		{"a spread beside other variadic arguments", "package main\nfunc f(xs ...int) int = xs.Size()\nfunc g(xs ...int) int = f(1, xs...)\nfunc main() { Println(g(1)) }\n", "spread-arg"},
		{"a named spread", "package main\nfunc f(a int, xs ...int) int = xs.Size()\nfunc g(xs ...int) int = f(a = 1, xs = xs...)\nfunc main() { Println(g(1)) }\n", "spread-arg"},
		{"a spread into Println", "package main\nfunc f(xs ...int) { Println(xs...) }\nfunc main() { f(1) }\n", "spread-arg"},
		{"a variadic function as a value", "package main\nfunc f(xs ...int) int = xs.Size()\nfunc main() { val g = f\n Println(1) }\n", "variadic-func-value"},
		{"a variadic method as a value", "package main\nstruct C(N int)\nfunc (c C) f(xs ...int) int = xs.Size()\nfunc main() { val g = C(1).f\n Println(1) }\n", "variadic-func-value"},
		{"a variadic function with an inout parameter", "package main\nstruct C(N int)\nfunc f(inout c C, xs ...int) int = xs.Size()\nfunc main() { var c = C(1)\n Println(f(&c, 1)) }\n", "variadic-inout"},
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
	// Counter.Get, Counter.Inc (a Ptr[Counter] receiver is a pointer
	// receiver), id and main. bad is reported for its body, takesTry for a
	// type in its signature (its body would translate); Box.Get has a
	// generic receiver, outside the subset.
	require.Equal(t, gala2lean.Stats{Funcs: 7, Methods: 3, Translated: 4}, s, "%v", err)
}

// TestOwnPtr checks that a receiver `Ptr[T]` is a pointer receiver only
// when Ptr is std's: in a program that declares a type Ptr, it is that
// type, a generic receiver outside the subset.
func TestOwnPtr(t *testing.T) {
	src := `package main

struct Ptr[T any](Value T)

struct Counter(var N int)

func (c Ptr[Counter]) Get() int = 1

func main() {
    Println(1)
}
`
	p := export(t, "main.gala", src)
	_, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Library: true})
	require.Equal(t, gala2lean.Stats{Funcs: 2, Methods: 1, Translated: 1}, gala2lean.CountTranslated(p, err), "%v", err)

	// A case named Ptr is a Go type of that name too.
	src = `package main

sealed type Ref[T any] {
    case Ptr(V T)
    case Nil()
}

struct Counter(var N int)

func (c Counter) Get() int = c.N

func (c Ptr[Counter]) Get() int = 99

func main() {
    val c = Counter(1)
    Println(c.Get())
}
`
	p = export(t, "main.gala", src)
	res, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Main: true})
	require.NoError(t, err)
	require.NotContains(t, res.Lean, "99", "Ptr[Counter] is the case's method, not Counter's")
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

// TestDotImportsPerFile checks that a library name resolves by the dot
// imports of its own file, as in the Go the transpiler writes: a file that
// dot-imports go_interop cannot have std's Tuple or the collections' ArrayOf
// (go_interop might declare them), but another file of the package can.
func TestDotImportsPerFile(t *testing.T) {
	interop := `package main

import . "martianoff/gala/go_interop"

func viaSlice() int = SliceOf(1, 2).Size()

func pair() Tuple[int, int] = Tuple(1, 2)

// widen uses Go's conversion, which no dot import can declare.
func widen(n int) int64 = int64(n)
`
	main := `package main

import . "martianoff/gala/collection_immutable"

func sizes() int = ArrayOf(1, 2).Size() + ArrayTabulate(3, (i) => i).Size()

func both() int = When(true, 1).GetOrElse(0)

func main() {
    Println(sizes(), both())
}
`
	plain := `package main

func noCollections() int = ArrayOf(1).Size()

func arrayType(a Array[int]) int = 1
`
	p, err := coreexport.Export([]coreexport.Source{{Path: "interop.gala", Text: interop}, {Path: "main.gala", Text: main},
		{Path: "plain.gala", Text: plain}}, coreexport.Options{LiteralType: analyzerhints.LiteralType})
	require.NoError(t, err)
	_, err = gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Library: true})
	var u *gala2lean.UnsupportedError
	require.ErrorAs(t, err, &u)
	stopped := map[string]string{}
	for _, it := range u.Items {
		if _, seen := stopped[it.Decl]; !seen {
			stopped[it.Decl] = it.Construct
		}
	}
	// A name another dot import might declare is reported as that import; a
	// collections name in a file without their import as the call it is.
	require.Equal(t, "import", stopped["viaSlice"])
	require.Equal(t, "import", stopped["pair"])
	require.Equal(t, "call-of-ArrayOf", stopped["noCollections"])
	require.Equal(t, "type-Array", stopped["arrayType"])
	require.NotContains(t, stopped, "widen")
	require.NotContains(t, stopped, "sizes")
	require.NotContains(t, stopped, "both")
	require.NotContains(t, err.Error(), "_gala_hidden")
	require.Equal(t, gala2lean.Stats{Funcs: 8, Translated: 4}, gala2lean.CountTranslated(p, err), "%v", err)
}
