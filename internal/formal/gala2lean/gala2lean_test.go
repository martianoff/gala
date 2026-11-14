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
		{"lambda", "package main\nfunc main() { val f = (x int) => x\n Println(1) }\n", "lambda"},
		{"method call", "package main\nstruct P(X int)\nfunc main() { Println(P(1).Copy()) }\n", "method-call"},
		{"go builtin", "package main\nfunc main() { println(1) }\n", "call-of-println"},
		{"return in an expression", "package main\nfunc f(n int) int { val x = if (n > 0) { return 1 } else 2\n x }\nfunc main() { Println(f(1)) }\n", "return-inside-expression"},
		{"no main", "package main\nfunc f() int = 1\n", "no-main"},
		{"default argument", "package main\nfunc f(n int = 1) int = n\nfunc main() { Println(f()) }\n", "param-default"},
		{"untyped global", "package main\nval g = h()\nfunc h() (int, int) = (1, 2)\nfunc main() { Println(g) }\n", "go-results"},
		{"sealed without a default case as a zero value", "package main\nsealed type S { case A case B }\nfunc main() { var s S\n Println(s) }\n", "sealed-zero-value"},
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
