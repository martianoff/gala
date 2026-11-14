package ownership_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/ownership"
)

// searchPaths finds std: in the runfiles, or above the working directory.
func searchPaths(t *testing.T) []string {
	t.Helper()
	if p, err := bazel.Runfile("std/option.gala"); err == nil {
		return []string{filepath.Dir(filepath.Dir(p))}
	}
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return []string{dir}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("std not found")
		}
		dir = parent
	}
}

// module writes a GALA module, example.com/own, with the files given by
// their path in it, and returns its directory.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["gala.mod"] = "module example.com/own\n\ngala dev\n"
	for name, src := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	}
	return root
}

// analyze analyzes the package in dir of module root, as the coverage tool
// does: the analyzer's metadata for it, then the ownership analysis of its
// files.
func analyze(t *testing.T, root, dir string) (*ownership.Report, *transpiler.RichAST) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "*.gala"))
	require.NoError(t, err)
	sort.Strings(paths)
	p := parser.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, append([]string{root}, searchPaths(t)...), paths, root)
	var files []ownership.File
	var rich *transpiler.RichAST
	for _, path := range paths {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		parsed := p.ParseFile(string(src))
		require.NoError(t, parsed.Err())
		if rich == nil {
			rich, err = a.AnalyzeParsed(parsed, path)
			require.NoError(t, err)
		}
		tree, err := parsed.CleanNative().Tree()
		require.NoError(t, err)
		files = append(files, ownership.File{Path: filepath.Base(path), Root: native.SourceFileNode{GalaNodeRef: tree.Root()}})
	}
	return ownership.Analyze(rich.PackageName, files, rich, ownership.Options{HandWrittenGo: ownership.HasHandWrittenGo(filepath.Join(root, filepath.FromSlash(dir)))}), rich
}

func funcOf(t *testing.T, r *ownership.Report, name string) ownership.Func {
	t.Helper()
	for _, f := range r.Funcs {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no function %s in %v", name, r.Funcs)
	return ownership.Func{}
}

func bindingOf(t *testing.T, r *ownership.Report, fn, name string) ownership.Binding {
	t.Helper()
	for _, b := range funcOf(t, r, fn).Bindings {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("%s has no pointer binding %s", fn, name)
	return ownership.Binding{}
}

// kinds is the reason kinds of b, sorted and without duplicates.
func kinds(b ownership.Binding) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range b.Reasons {
		if !seen[string(r.Kind)] {
			seen[string(r.Kind)] = true
			out = append(out, string(r.Kind))
		}
	}
	sort.Strings(out)
	return out
}

const counterSrc = `package own

type counter struct {
    var n int
    var name string
}

func newCounter() Ptr[counter] = &counter{n: 0, name: "c"}

func (c Ptr[counter]) inc() {
    c.n = c.n + 1
}

func (c Ptr[counter]) get() int = c.n

func (c Ptr[counter]) incTwice() {
    c.inc()
    c.inc()
}

func run() int {
    val c = newCounter()
    c.incTwice()
    c.n += 2
    Println(s"count ${c.n} of ${c.name}")
    c.get()
}

func rec(c Ptr[counter], k int) {
    if k > 0 {
        c.inc()
        rec(c, k - 1)
    }
}

struct holder(C Ptr[counter])

func id(c Ptr[counter]) Ptr[counter] = c
func viaId(c Ptr[counter]) int = id(c).n
func keep(c Ptr[counter]) holder = holder(c)
func capture(c Ptr[counter]) func() int = () => c.get()
func (c Ptr[counter]) reset() {
    c.n = 0
}
func methodValue(c Ptr[counter]) func() = c.reset
func alias(c Ptr[counter]) int {
    val d = c
    d.get()
}
func compare(a Ptr[counter], b Ptr[counter]) bool = a == b
func both(a Ptr[counter], b Ptr[counter]) int = a.n + b.n
func twice(c Ptr[counter]) int = both(c, c)
func printed(c Ptr[counter]) {
    Println(c)
}
func nullable(c *counter) int = c.n
func viaValue(c Ptr[counter], f func(Ptr[counter])) {
    f(c)
}
func readsIntoValue(c Ptr[counter], f func(int) int) int = f(c.n)
func notFresh() int {
    var c = newCounter()
    c.get()
}
func ping(c Ptr[counter], k int) {
    if k > 0 {
        pong(c, k - 1)
    }
}
func pong(c Ptr[counter], k int) {
    if k == 0 {
        id(c)
    } else {
        ping(c, k)
    }
}
func fresh() Ptr[counter] {
    val c = &counter(n = 1, name = "f")
    c.inc()
    c
}
func notFreshResult(c Ptr[counter]) Ptr[counter] = if (c.n > 0) c else newCounter()
func addressOfField(c Ptr[counter]) *int = &c.n
func idBlock(c Ptr[counter]) Ptr[counter] {
    c
}
func aliasViaBlock() int {
    val a = newCounter()
    val b = idBlock(a)
    b.n
}
func parenReceiver(c Ptr[counter]) {
    (*c).inc()
}
func (c Ptr[counter]) Exported() int = c.n
`

func TestSummariesAndReasons(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": counterSrc})
	r, _ := analyze(t, root, "own")

	borrowedRecv := []string{"counter.inc", "counter.get", "counter.incTwice"}
	for _, name := range borrowedRecv {
		f := funcOf(t, r, name)
		require.NotNil(t, f.Summary, name)
		require.True(t, f.Summary.ReceiverBorrowed, "%s: receiver should be borrowed", name)
		require.True(t, f.Unique(), "%s: %v", name, f.Bindings)
	}
	require.True(t, funcOf(t, r, "newCounter").Summary.ReturnsFresh)
	require.True(t, funcOf(t, r, "fresh").Summary.ReturnsFresh)
	require.False(t, funcOf(t, r, "notFreshResult").Summary.ReturnsFresh)
	require.False(t, funcOf(t, r, "id").Summary.ReturnsFresh)
	require.False(t, funcOf(t, r, "idBlock").Summary.ReturnsFresh)

	run := bindingOf(t, r, "run", "c")
	require.True(t, run.Unique, "run's c: %v", run.Reasons)

	// Recursion alone does not make a parameter escape: the greatest fixed
	// point keeps it borrowed.
	require.True(t, funcOf(t, r, "rec").Summary.ParamBorrowed(0))

	cases := []struct {
		fn, binding string
		want        []string
	}{
		{"id", "c", []string{"returned"}},
		{"viaId", "c", []string{"escaping-callee"}},
		{"keep", "c", []string{"stored"}},
		{"capture", "c", []string{"captured"}},
		{"methodValue", "c", []string{"captured"}},
		{"alias", "c", []string{"aliased"}},
		{"compare", "a", []string{"compared"}},
		{"compare", "b", []string{"compared"}},
		{"twice", "c", []string{"passed-twice"}},
		{"printed", "c", []string{"passed-to-go"}},
		{"nullable", "c", []string{"nullable"}},
		{"viaValue", "c", []string{"unknown-callee"}},
		{"notFresh", "c", []string{"origin"}},
		{"ping", "c", []string{"escaping-callee"}},
		{"pong", "c", []string{"escaping-callee"}},
		{"notFreshResult", "c", []string{"returned"}},
		{"addressOfField", "c", []string{"other"}},
		{"idBlock", "c", []string{"returned"}},
		{"aliasViaBlock", "b", []string{"origin"}},
		{"parenReceiver", "c", []string{"other"}},
		{"counter.Exported", "c", []string{"from-go"}},
	}
	for _, tc := range cases {
		t.Run(tc.fn+"/"+tc.binding, func(t *testing.T) {
			b := bindingOf(t, r, tc.fn, tc.binding)
			require.False(t, b.Unique)
			require.Equal(t, tc.want, kinds(b), "%v", b.Reasons)
		})
	}
	// Reading a field into a call of a function value lends nothing.
	require.True(t, bindingOf(t, r, "readsIntoValue", "c").Unique)
	// A nullable parameter is still borrowed: only its uniqueness needs a
	// non-nil pointer.
	require.True(t, funcOf(t, r, "nullable").Summary.ParamBorrowed(0))
}

const shapeSrc = `package own

import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/go_interop"
)

type tree struct {
    var kids Array[node]
    var label Option[string]
}

struct node(name string, weight int)

type bytesBuf struct {
    var data []byte
}

type linked struct {
    var next Ptr[linked]
}

type fn struct {
    var f func() int
}

func useTree() int {
    val t = &tree{kids: EmptyArray[node](), label: None[string]()}
    t.kids = t.kids.Append(node("a", 1))
    t.kids.Size()
}

func useBuf() int {
    val b = &bytesBuf{data: ToBytes("abc")}
    b.data.Size()
}

func useLinked(l Ptr[linked]) bool = true
func useFn(f Ptr[fn]) bool = true
`

func TestShape(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": shapeSrc})
	r, _ := analyze(t, root, "own")
	tree := bindingOf(t, r, "useTree", "t")
	require.True(t, tree.Unique, "%v", tree.Reasons)
	for _, fn := range []string{"useBuf", "useLinked", "useFn"} {
		f := funcOf(t, r, fn)
		require.Len(t, f.Bindings, 1)
		require.Equal(t, []string{"shape"}, kinds(f.Bindings[0]), "%s: %v", fn, f.Bindings[0].Reasons)
	}
}

const libSrc = `package lib

type Counter struct {
    var N int
}

func NewCounter() Ptr[Counter] = &Counter{N: 0}

func Touch(c Ptr[Counter]) {
    c.N = c.N + 1
}

func Keep(c Ptr[Counter]) Ptr[Counter] = c

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}
`

const appSrc = `package main

import "example.com/own/lib"

func good() int {
    val c = lib.NewCounter()
    lib.Touch(c)
    c.Bump()
    c.N
}

func bad() int {
    val c = lib.NewCounter()
    lib.Keep(c)
    c.N
}

func main() {
    Println(good() + bad())
}
`

// TestAcrossPackages: an imported package's summaries come from its
// metadata, which the analyzer records when it loads the package.
func TestAcrossPackages(t *testing.T) {
	root := module(t, map[string]string{"lib/lib.gala": libSrc, "app/main.gala": appSrc})
	r, rich := analyze(t, root, "app")
	require.NotNil(t, rich.Functions["lib.Touch"].Borrow)
	require.True(t, rich.Functions["lib.Touch"].Borrow.ParamBorrowed(0))
	require.False(t, rich.Functions["lib.Keep"].Borrow.ParamBorrowed(0))
	require.True(t, rich.Functions["lib.NewCounter"].Borrow.ReturnsFresh)
	require.True(t, rich.Types["lib.Counter"].Methods["Bump"].Borrow.ReceiverBorrowed)

	good := bindingOf(t, r, "good", "c")
	require.True(t, good.Unique, "%v", good.Reasons)
	bad := bindingOf(t, r, "bad", "c")
	require.Equal(t, []string{"escaping-callee"}, kinds(bad), "%v", bad.Reasons)
}

const goBoundarySrc = `package own

import (
    "bytes"
    "container/list"
    "fmt"
)

type box struct {
    var n int
}

type bytesHolder struct {
    var data []byte
}

func takeAny(x any) bool = true
func takeStringer(x fmt.Stringer) bool = true

// GALA -> Go.
func toGoFunc(b Ptr[box]) string = fmt.Sprint(b)
func toAny(b Ptr[box]) bool = takeAny(b)
func toGoStruct(b Ptr[box]) int {
    val e = list.Element{Value: b}
    1
}
func referenceToGo(h Ptr[bytesHolder]) []byte = bytes.ToUpper(h.data)
func fieldValueToGo(b Ptr[box]) string = fmt.Sprint(b.n)

// Go -> GALA.
func fromGoResult() int {
    val l = list.New()
    l.Len()
}
func fromGoField(e Ptr[list.Element]) bool {
    val n = e.Next()
    true
}
func callback(b Ptr[box]) {
    b.n = 1
}
func registers() func(Ptr[box]) = callback

// A borrowed pointer that stays in GALA.
func local() int {
    val b = &box{n: 1}
    b.n = 2
    b.n
}
`

// TestGoBoundary: every flow of a pointer into Go escapes, and a pointer that
// comes from Go is never unique, in both directions.
func TestGoBoundary(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": goBoundarySrc})
	r, _ := analyze(t, root, "own")
	cases := []struct {
		fn, binding string
		want        string
	}{
		{"toGoFunc", "b", "passed-to-go"},
		{"toAny", "b", "passed-to-go"},
		{"toGoStruct", "b", "passed-to-go"},
		{"referenceToGo", "h", "passed-to-go"},
		{"fromGoResult", "l", "from-go"},
		{"fromGoField", "n", "from-go"},
		{"callback", "b", "from-go"},
	}
	for _, tc := range cases {
		t.Run(tc.fn, func(t *testing.T) {
			b := bindingOf(t, r, tc.fn, tc.binding)
			require.False(t, b.Unique)
			require.Contains(t, kinds(b), tc.want, "%v", b.Reasons)
		})
	}
	// A copy of an int field passed to Go reaches nothing inside the pointee.
	require.True(t, bindingOf(t, r, "fieldValueToGo", "b").Unique, "%v", bindingOf(t, r, "fieldValueToGo", "b").Reasons)
	require.True(t, bindingOf(t, r, "local", "b").Unique)
	// The callback's receiver is still borrowed by its body: being called
	// from Go makes it not unique, not escaping.
	require.True(t, funcOf(t, r, "callback").Summary.ParamBorrowed(0))
}

// TestHandWrittenGo: in a package with a hand-written .go file, Go may call
// any function, so no parameter is unique; a fresh local still is.
func TestHandWrittenGo(t *testing.T) {
	root := module(t, map[string]string{
		"own/own.gala":  "package own\n\ntype box struct {\n    var n int\n}\n\nfunc touch(b Ptr[box]) {\n    b.n = 1\n}\n\nfunc local() int {\n    val b = &box{n: 1}\n    touch(b)\n    b.n\n}\n",
		"own/helper.go": "package own\n\nfunc helper() *box { b := &box{}; touch(b); return b }\n",
	})
	r, _ := analyze(t, root, "own")
	touch := bindingOf(t, r, "touch", "b")
	require.Equal(t, []string{"from-go"}, kinds(touch), "%v", touch.Reasons)
	require.True(t, funcOf(t, r, "touch").Summary.ParamBorrowed(0))
	require.True(t, bindingOf(t, r, "local", "b").Unique)
}

// TestReasonsArePositioned: every reason points into the file.
func TestReasonsArePositioned(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": counterSrc})
	r, _ := analyze(t, root, "own")
	lines := strings.Count(counterSrc, "\n") + 1
	for _, f := range r.Funcs {
		for _, b := range f.Bindings {
			for _, reason := range b.Reasons {
				require.Equal(t, "own.gala", reason.File)
				require.True(t, reason.Line >= 1 && reason.Line <= lines && reason.Col >= 1, "%s: %v", f.Name, reason)
			}
		}
	}
}
