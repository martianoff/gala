package gocoreexport_test

import (
	"encoding/json"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gocoreexport"
	"martianoff/gala/internal/transpiler/fuzz/progrun"
)

func TestRoundTripS1Shapes(t *testing.T) {
	doc, ok := roundTrip(t, "shapes.go", s1Shapes)
	require.True(t, ok, "the shapes are wholly inside GoCore")
	counts := counts(doc)
	for _, kind := range []string{"func-decl", "type-decl", "value-decl", "switch", "for", "range", "if",
		"composite-lit", "index", "star", "unary", "type-assert", "func-lit", "char-lit", "decl-stmt"} {
		require.Positive(t, counts[kind], "the shapes exercise %s", kind)
	}
}

// TestLiteralsAreReadLexically pins literal values against the Go
// specification, independently of how the exporter reads them.
func TestLiteralsAreReadLexically(t *testing.T) {
	doc, ok := roundTrip(t, "lits.go", `package main

var a, b, c, d = 0x1F_FF, 0b101, 0o17, 99999999999999999999999999
var s, r = "a\x41é\n", `+"`x\\n`"+`
var ch, hi, uni = '\n', '\xff', 'é'

type T struct {
	F int `+"`json:\"f\"`"+`
}
`)
	require.True(t, ok)
	var got []string
	walk(doc, func(n obj) {
		switch n["kind"] {
		case "int-lit", "string-lit", "char-lit":
			b, _ := json.Marshal(n["value"])
			got = append(got, string(b))
		}
		if tag, ok := n["tag"].(string); ok && tag != "" {
			got = append(got, "tag "+tag)
		}
	})
	require.Equal(t, []string{`"8191"`, `"5"`, `"15"`, `"99999999999999999999999999"`,
		`"aAé\n"`, `"x\\n"`, `10`, `255`, `233`, `tag json:"f"`}, got)
}

// TestEveryConstructRoundTrips exports the constructs GoCore does not run
// (deciding that is the Lean side's business) and checks that they
// round-trip like any other.
func TestEveryConstructRoundTrips(t *testing.T) {
	doc, ok := roundTrip(t, "all.go", `package main

type Num interface {
	~int | ~int64
}

func f(ch chan int, in <-chan int, out chan<- int, m map[string]int, xs []int, a [3]int) float64 {
	defer println("x")
	go f(ch, in, out, m, xs, a)
	ch <- 1
	_ = <-ch
	_ = xs[1:2]
	_ = xs[:2:3]
	_ = [...]int{1, 2}
	_ = 0x1p-2 + 1e3 + 2.5i
L:
	for {
		break L
	}
	switch v := any(1).(type) {
	case int, string:
		_ = v
	default:
	}
	switch any(1).(type) {
	}
	select {
	case v := <-ch:
		_ = v
	case out <- 2:
	default:
	}
	goto M
M:
	switch 1 {
	case 1:
		fallthrough
	default:
	}
	return 1.5
}
`)
	require.True(t, ok)
	counts := counts(doc)
	for _, kind := range []string{"chan-type", "map-type", "array-type", "ellipsis", "defer", "go", "send",
		"slice-expr", "labeled", "type-switch", "select", "float-lit", "imag-lit"} {
		require.Positive(t, counts[kind], "%s is exported", kind)
	}
}

// TestUnrepresentable checks the nodes JSON cannot carry exactly: they are
// reported in place, and the statements around them are kept.
func TestUnrepresentable(t *testing.T) {
	prog, err := gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "u.go", Text: `package main

func f() {
	x := 1
	_ = "\xff"
	x = 2
}

func () g() {}

type T struct {
	F int "\xff"
}
`}}}})
	require.NoError(t, err)
	counts, err := gocoreexport.ConstructCounts(prog)
	require.NoError(t, err)
	require.Equal(t, 2, counts["invalid-utf8"], "a string and a struct whose tag is not UTF-8")
	require.Equal(t, 1, counts["bad-node"], "a method without a receiver")
	require.Equal(t, 3, counts["assign"], "the statements around them are kept")
}

func TestVersionAndOrder(t *testing.T) {
	prog, err := gocoreexport.Export([]gocoreexport.PackageSource{
		{Path: "main", Files: []gocoreexport.Source{{Name: "b.go", Text: "package main\n"}, {Name: "a.go", Text: "package main\n"}}},
		{Path: "martianoff/gala/std", Files: []gocoreexport.Source{{Name: "s.go", Text: "package std\n"}}},
	})
	require.NoError(t, err)
	require.Equal(t, gocoreexport.Version, prog.GoCore)
	require.Equal(t, "main", prog.Packages[0].Name)
	require.Equal(t, "std", prog.Packages[1].Name)
	require.Equal(t, []string{"a.go", "b.go"}, []string{prog.Packages[0].Files[0].Name, prog.Packages[0].Files[1].Name})

	_, err = gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "x.go", Text: "package main\nfunc {"}}}})
	require.Error(t, err, "Go that does not parse is an error")
	_, err = gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "a.go", Text: "package main\n"}, {Name: "b.go", Text: "package other\n"}}}})
	require.Error(t, err, "files of two packages under one path are an error")
}

// TestExamplesRoundTrip transpiles every example, exports the Go it emits
// with the Go of every GALA package it imports, directly or not, and checks
// every file: each node's kind is a schema kind, and the file prints back
// to its own syntax tree. The files with an unrepresentable node, which do
// not print, are listed.
func TestExamplesRoundTrip(t *testing.T) {
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	var examples []string
	for _, f := range files {
		if path.Dir(f.ShortPath) == "examples" && strings.HasSuffix(f.ShortPath, ".gala") {
			examples = append(examples, f.Path)
		}
	}
	slices.Sort(examples)
	require.Greater(t, len(examples), 200, "//examples:all_gala_sources is not staged")

	kinds, unrepresentable := set(gocoreexport.Kinds()), set(gocoreexport.UnrepresentableKinds())
	imp := progrun.OracleImporter(t)
	seen, exportedPkgs := map[string]bool{}, map[string]bool{}
	exported, transpiled := 0, 0
	var untranspiled, unloaded, notPrinted []string
	// check exports one file and returns the packages it imports.
	check := func(name, text string) []string {
		exported++
		doc, ok := roundTrip(t, name, text)
		if !ok {
			notPrinted = append(notPrinted, name)
		}
		walk(doc, func(n obj) {
			if u, ok := n["unrepresentable"].(string); ok && !unrepresentable[u] {
				t.Errorf("%s: unrepresentable %q is not listed", name, u)
			}
			if k, ok := n["kind"].(string); ok && !kinds[k] {
				t.Errorf("%s: kind %q is not a schema kind", name, k)
			}
		})
		var imports []string
		file := doc["packages"].([]any)[0].(obj)["files"].([]any)[0].(obj)
		for _, i := range list(file["imports"]) {
			imports = append(imports, str(i.(obj)["path"]))
		}
		return imports
	}
	for _, ex := range examples {
		src, err := os.ReadFile(ex)
		require.NoError(t, err)
		res := progrun.Transpile(t, string(src), filepath.Base(ex))
		if res.Err != nil || res.Panic != nil {
			// Examples of rejected programs, and packages of multi-file ones.
			untranspiled = append(untranspiled, filepath.Base(ex))
			continue
		}
		transpiled++
		queue := check(filepath.Base(ex)+".go", res.Go)
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			if seen[p] || !strings.HasPrefix(p, "martianoff/gala/") {
				continue
			}
			seen[p] = true
			// Importing loads the package into the importer, whose sources
			// are then the Go it generated for it.
			_, err := imp.Import(p)
			sources, loaded := imp.Sources(p)
			if err != nil || !loaded {
				unloaded = append(unloaded, p)
				continue
			}
			exportedPkgs[p] = true
			for _, n := range slices.Sorted(maps.Keys(sources)) {
				queue = append(queue, check(p+"/"+n, sources[n])...)
			}
		}
	}
	t.Logf("%d examples transpiled, %d not: %v", transpiled, len(untranspiled), untranspiled)
	t.Logf("%d packages exported; %d not staged for this test: %v", len(exportedPkgs), len(unloaded), unloaded)
	t.Logf("%d files exported and printed back; %d with an unrepresentable node: %v", exported-len(notPrinted), len(notPrinted), notPrinted)
	require.Greater(t, transpiled, 400)
	for _, p := range []string{"martianoff/gala/std", "martianoff/gala/collection_immutable"} {
		require.True(t, exportedPkgs[p], "%s was exported", p)
	}
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// walk calls f on every object of a document, in key order, so a walk is
// deterministic.
func walk(v any, f func(obj)) {
	switch v := v.(type) {
	case obj:
		f(v)
		for _, k := range slices.Sorted(maps.Keys(v)) {
			walk(v[k], f)
		}
	case []any:
		for _, c := range v {
			walk(c, f)
		}
	}
}

// counts is how often each kind occurs in a document.
func counts(doc obj) map[string]int {
	out := map[string]int{}
	walk(doc, func(n obj) {
		if k, ok := n["kind"].(string); ok {
			out[k]++
		}
	})
	return out
}

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
