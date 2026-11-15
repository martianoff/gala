package ownership_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/ownership"
)

// An `inout` binding is read as a value by its name and lent by `&name`:
// lent on to another `inout` parameter it stays unique.
func TestInoutBindings(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": `package own

struct Acc(var total int)

func (inout a Acc) add(n int) {
    a.total = a.total + n
}

func addTwice(inout a Acc, n int) {
    a.add(n)
    add2(&a, n)
}

func add2(inout a Acc, n int) {
    val before = a
    a = Acc(before.total + n)
}

func Fill(inout a Acc) {
    addTwice(&a, 1)
}
`})
	r, _ := analyze(t, root, "own")
	for _, fn := range []string{"Acc.add", "addTwice", "add2"} {
		b := bindingOf(t, r, fn, "a")
		require.True(t, b.Unique, "%s: %v", fn, b.Reasons)
	}
	// Exported: Go may call its GALA_ body directly, with a pointer it
	// shares, so it stays from-go, and that is its only reason.
	require.Equal(t, []string{"from-go"}, kinds(bindingOf(t, r, "Fill", "a")))
	require.True(t, funcOf(t, r, "Fill").Summary.ParamBorrowed(0))
}

// planOf is PlanInout over the one-file package dir/own.gala of module
// root.
func planOf(t *testing.T, root, dir string, cfg ownership.AdoptConfig) *ownership.AdoptPlan {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(dir), "own.gala")
	p := parser.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, append([]string{root}, searchPaths(t)...), []string{path}, root)
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed := p.ParseFile(string(src))
	require.NoError(t, parsed.Err())
	rich, err := a.AnalyzeParsed(parsed, path)
	require.NoError(t, err)
	tree, err := parsed.CleanNative().Tree()
	require.NoError(t, err)
	files := []ownership.File{{Path: "own.gala", Root: native.SourceFileNode{GalaNodeRef: tree.Root()}}}
	return ownership.PlanInout(rich.PackageName, files, rich, ownership.Options{}, cfg)
}

const adoptSrc = `package own

struct Acc(var total int)

func (a Ptr[Acc]) add(n int) {
    a.total = a.total + n
}

func addAll(a Ptr[Acc], n int) {
    a.add(n)
    keepPtr(a)
}

func keepPtr(a Ptr[Acc]) {
    a.add(1)
}

func viaVar(var a Ptr[Acc]) {
    a.add(1)
}

func nullable(a *Acc) {
    a.total = 0
}

var saved = None[Ptr[Acc]]()

func save(a Ptr[Acc]) {
    saved = Some(a)
}

func lendsToSave(a Ptr[Acc]) {
    save(a)
}

func show(a Ptr[Acc]) string = s"${*a}"

func Public(a Ptr[Acc]) {
    a.add(2)
}
`

func verdicts(p *ownership.AdoptPlan) map[string]string {
	out := map[string]string{}
	for _, f := range p.Funcs {
		for _, b := range f.Bindings {
			out[f.Name] = b.Blocked
		}
	}
	return out
}

func TestPlanInout(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": adoptSrc})
	v := verdicts(planOf(t, root, "own", ownership.AdoptConfig{}))
	require.Equal(t, "", v["Acc.add"])
	require.Equal(t, "", v["addAll"])
	require.Equal(t, "", v["keepPtr"])
	require.Contains(t, v["viaVar"], "not declared as a plain `Ptr[T]`")
	require.Contains(t, v["nullable"], "may be nil")
	require.Contains(t, v["save"], "escapes: stored")
	require.Contains(t, v["lendsToSave"], "escapes")
	require.Contains(t, v["Public"], "public API")
	require.Contains(t, v["show"], "interpolated string")

	// Public converts the public function too; Exclude keeps a function
	// and, through the closure, what lends to it.
	v = verdicts(planOf(t, root, "own", ownership.AdoptConfig{Public: true, Exclude: map[string]string{"keepPtr": "excluded"}}))
	require.Equal(t, "", v["Public"])
	require.Equal(t, "excluded", v["keepPtr"])
	require.Contains(t, v["addAll"], "lent to parameter 1 of keepPtr, which stays a pointer")
	require.Equal(t, "", v["Acc.add"])
}

// TestPlanInoutEdits checks the edits of one function: the declaration,
// a dereference and a lend.
func TestPlanInoutEdits(t *testing.T) {
	src := `package own

struct Acc(var total int)

func inner(a Ptr[Acc]) {
    a.total = a.total + 1
}

func outer(a Ptr[Acc]) int {
    inner(a)
    val copy = *a
    copy.total
}
`
	root := module(t, map[string]string{"own/own.gala": src})
	p := planOf(t, root, "own", ownership.AdoptConfig{})
	out, at := "", 0
	for _, e := range p.Edits {
		out += src[at:e.Start] + e.Text
		at = e.End
	}
	out += src[at:]
	require.Equal(t, `package own

struct Acc(var total int)

func inner(inout a Acc) {
    a.total = a.total + 1
}

func outer(inout a Acc) int {
    inner(&a)
    val copy = a
    copy.total
}
`, out)
}

// A package named test is a library like any other: its exported functions
// are public API.
func TestPlanInoutPackageNamedTest(t *testing.T) {
	root := module(t, map[string]string{"own/own.gala": `package test

struct Acc(var total int)

func Reset(a Ptr[Acc]) {
    a.total = 0
}
`})
	require.Contains(t, verdicts(planOf(t, root, "own", ownership.AdoptConfig{}))["Reset"], "public API")
}
