package goexport_test

import (
	"encoding/json"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/goexport"
	"martianoff/gala/internal/transpiler/fuzz/progrun"
)

// The shapes the transpiler emits for S1, written by hand: a sealed parent
// struct with its tag, Apply and Unapply, a generic type, pointers, a TCO
// loop, a switch, a const block, package-level variables and fmt.
const s1Shapes = `package main

import "fmt"
import "martianoff/gala/std"

type Expr struct {
	Value    std.Immutable[int]
	L        *Expr
	_variant uint8
}

const (
	_Expr_Num uint8 = 1
	_Expr_Add uint8 = 2
	_Expr_Big       = 0x1F_FF
)

type Num struct {
}

func (_ Num) Apply(Value int) Expr {
	return Expr{Value: std.NewImmutable(Value), _variant: _Expr_Num}
}
func (_ Num) Unapply(v Expr) std.Option[int] {
	if v._variant == _Expr_Num {
		return std.Some[int]{}.Apply(v.Value.Get())
	}
	return std.None[int]{}.Apply()
}

type Pair[A any, B any] struct {
	V1 std.Immutable[A]
	V2 std.Immutable[B]
}

func (s Expr) String() string {
	switch s._variant {
	case _Expr_Num:
		return std.FormatStruct("Num", s.Value.Get())
	default:
		return "Expr(<unknown>)"
	}
}

func (s *Expr) Link(l Expr) { s.L = &l }

func factorial(n int, acc int) int {
	for {
		if n <= 0 {
			return acc
		} else {
			_tmp_1 := n - 1
			_tmp_2 := n * acc
			n, acc = _tmp_1, _tmp_2
			continue
		}
	}
}

func sum(xs ...int) (total int) {
	for _, x := range xs {
		total += x
	}
	return
}

var Limit = std.NewImmutable(10)

func main() {
	var t = Pair[int, string]{V1: std.NewImmutable(1), V2: std.NewImmutable("a\tbé")}
	var r = func() string {
		_subj := t
		if _subj.V1.Get() == 2 && !(-1 > - -3) {
			return "two"
		}
		return ` + "`raw\\n`" + `
	}()
	var z *Expr
	var c = 'x'
	x := *new(int)
	x++
	x <<= 2
	if p, ok := any(t).(Pair[int, string]); ok && z == nil {
		fmt.Println(p.V1.Get(), c)
	}
	fmt.Println(r, factorial(10, 1), x&^3|1^7, sum(1, 2, 3), Limit.Get(), 0b101, 0o17)
	fmt.Print(Num{}.Apply(1))
	type local struct{ a int }
	var (
		u, v int = 1, 2
		w        = local{a: u + v}
	)
	_ = w
}
`

func TestRoundTripS1Shapes(t *testing.T) {
	doc := roundTrip(t, "shapes.go", s1Shapes)
	counts := countKinds(t, doc)
	for _, kind := range []string{"func-decl", "type-decl", "value-decl", "switch", "for", "range", "if",
		"composite-lit", "index", "star", "unary", "type-assert", "func-lit", "char-lit", "decl-stmt"} {
		require.Positive(t, counts[kind], "the shapes exercise %s", kind)
	}
}

func TestLiteralsAreReadLexically(t *testing.T) {
	doc := roundTrip(t, "lits.go", `package main

var a, b, c, d = 0x1F_FF, 0b101, 0o17, 99999999999999999999999999
var s, r = "a\x41é\n", `+"`x\\n`"+`
var ch = '\n'
`)
	var got []string
	walk(doc, func(n map[string]any) {
		if n["kind"] == "int-lit" || n["kind"] == "string-lit" || n["kind"] == "char-lit" {
			b, _ := json.Marshal(n["value"])
			got = append(got, string(b))
		}
	})
	require.Equal(t, []string{`"8191"`, `"5"`, `"15"`, `"99999999999999999999999999"`, `"aAé\n"`, `"x\\n"`, `10`}, got)
}

// TestUnsupportedIsReportedInPlace checks that constructs outside GoCore
// are exported as unsupported nodes where they are, never dropped, and that
// the statements around them are kept.
func TestUnsupportedIsReportedInPlace(t *testing.T) {
	prog, err := goexport.Export([]goexport.PackageSource{{Path: "main", Files: []goexport.Source{{Name: "u.go", Text: `package main

func f(ch chan int, m map[string]int, xs []int) float64 {
	defer println("x")
	go f(ch, m, xs)
	ch <- 1
	_ = <-ch
	_ = xs[1:2]
L:
	for {
		break L
	}
	switch v := any(1).(type) {
	case int:
		_ = v
	}
	return 1.5
}
`}}}})
	require.NoError(t, err)
	counts, err := goexport.ConstructCounts(prog)
	require.NoError(t, err)
	for _, name := range []string{"chan-type", "map-type", "array-type", "defer", "go", "send", "receive",
		"slice-expr", "labeled-stmt", "type-switch", "float-literal"} {
		require.Equal(t, 1, counts[name], "%s is reported once", name)
		require.Contains(t, goexport.UnsupportedKinds(), name)
	}
	require.Equal(t, 2, counts["assign"], "the statements around the unsupported ones are kept")
}

func TestVersionAndOrder(t *testing.T) {
	prog, err := goexport.Export([]goexport.PackageSource{
		{Path: "main", Files: []goexport.Source{{Name: "b.go", Text: "package main\n"}, {Name: "a.go", Text: "package main\n"}}},
		{Path: "martianoff/gala/std", Files: []goexport.Source{{Name: "s.go", Text: "package std\n"}}},
	})
	require.NoError(t, err)
	require.Equal(t, goexport.Version, prog.GoCore)
	require.Equal(t, "main", prog.Packages[0].Name)
	require.Equal(t, "std", prog.Packages[1].Name)
	require.Equal(t, []string{"a.go", "b.go"}, []string{prog.Packages[0].Files[0].Name, prog.Packages[0].Files[1].Name})

	_, err = goexport.Export([]goexport.PackageSource{{Path: "main", Files: []goexport.Source{{Name: "x.go", Text: "package main\nfunc {"}}}})
	require.Error(t, err, "Go that does not parse is an error")
	_, err = goexport.Export([]goexport.PackageSource{{Path: "main", Files: []goexport.Source{{Name: "a.go", Text: "package main\n"}, {Name: "b.go", Text: "package other\n"}}}})
	require.Error(t, err, "files of two packages under one path are an error")
}

// TestExamplesRoundTrip transpiles every example, exports the Go it emits
// with the Go of the GALA packages it imports, and checks every file: each
// node's kind is a schema kind, each unsupported name is listed, and each file
// without unsupported nodes prints back to its own syntax tree.
func TestExamplesRoundTrip(t *testing.T) {
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	var examples []string
	for _, f := range files {
		if path.Dir(f.ShortPath) == "examples" && strings.HasSuffix(f.ShortPath, ".gala") {
			examples = append(examples, f.Path)
		}
	}
	sort.Strings(examples)
	require.Greater(t, len(examples), 200, "//examples:all_gala_sources is not staged")

	imp := progrun.OracleImporter(t)
	seen := map[string]bool{}
	var exported, roundTripped, transpiled int
	check := func(name, text string) {
		exported++
		doc := roundTrip(t, name, text)
		clean := true
		walk(doc, func(n map[string]any) {
			if u, ok := n["unsupported"].(string); ok {
				clean = false
				require.Contains(t, goexport.UnsupportedKinds(), u, "%s", name)
			}
			if k, ok := n["kind"].(string); ok {
				require.Contains(t, goexport.Kinds(), k, "%s", name)
			}
		})
		if clean {
			roundTripped++
		}
	}
	for _, ex := range examples {
		src, err := os.ReadFile(ex)
		require.NoError(t, err)
		res := progrun.Transpile(t, string(src), path.Base(ex))
		if res.Err != nil || res.Panic != nil {
			continue
		}
		transpiled++
		check(path.Base(ex)+".go", res.Go)
		if tc := imp.TypeCheck("main.go", res.Go); tc.Skipped != "" {
			continue
		}
		for _, p := range importsOf(t, res.Go) {
			if seen[p] {
				continue
			}
			seen[p] = true
			sources, ok := imp.Sources(p)
			if !ok {
				continue
			}
			names := make([]string, 0, len(sources))
			for n := range sources {
				names = append(names, n)
			}
			slices.Sort(names)
			for _, n := range names {
				check(p+"/"+n, sources[n])
			}
		}
	}
	t.Logf("%d examples transpiled; %d files exported, %d of them wholly inside GoCore and round-tripped", transpiled, exported, roundTripped)
	require.Greater(t, transpiled, 200)
	require.Greater(t, roundTripped, transpiled/2)
	require.True(t, seen["martianoff/gala/std"], "the std package was exported")
}

func importsOf(t *testing.T, src string) []string {
	t.Helper()
	prog, err := goexport.Export([]goexport.PackageSource{{Path: "x", Files: []goexport.Source{{Name: "x.go", Text: src}}}})
	require.NoError(t, err)
	var out []string
	for _, i := range prog.Packages[0].Files[0].Imports {
		out = append(out, i.Path)
	}
	return out
}

func walk(v any, f func(map[string]any)) {
	switch v := v.(type) {
	case map[string]any:
		// In key order, so a walk is deterministic.
		f(v)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			walk(v[k], f)
		}
	case []any:
		for _, c := range v {
			walk(c, f)
		}
	}
}

func countKinds(t *testing.T, doc map[string]any) map[string]int {
	t.Helper()
	counts := map[string]int{}
	walk(doc, func(n map[string]any) {
		if k, ok := n["kind"].(string); ok {
			counts[k]++
		}
	})
	return counts
}
