package transformer_test

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// samePackageModule is a module whose directory dir ("." for the root) holds
// a GALA file next to a hand-written Go file. It returns the files and the
// GALA file's path.
func samePackageModule(dir, goSrc, galaSrc string) (map[string]string, string) {
	galaFile := path.Join(dir, "program.gala")
	return map[string]string{
		"go.mod":                       "module example.com/sibs\n\ngo 1.25\n",
		"gala.mod":                     "module example.com/sibs\n",
		path.Join(dir, "sibling.go"): goSrc,
		galaFile:                       galaSrc,
	}, galaFile
}

// TestSamePackageGoSiblingDeclarations covers declarations made in a
// hand-written .go file of the package being transpiled. They are part of the
// package exactly as in a library, so the transpiler must see their types — in
// `package main` too, where it used to skip the scan and pass the code through
// untyped (#613, #614, #618).
func TestSamePackageGoSiblingDeclarations(t *testing.T) {
	cases := []struct {
		name    string
		goSrc   string // package clause added per layout
		galaSrc string // package clause added per layout
		want    []string
		absent  []string
	}{
		{
			name:  "Size and ByteSize on fields of a Go-declared struct lower (#613)",
			goSrc: "type Bag struct {\n\tItems []string\n\tText  string\n}\n",
			galaSrc: "func count(b Bag) int = b.Items.Size()\n" +
				"func chars(b Bag) int = b.Text.Size()\n" +
				"func bytes(b Bag) int = b.Text.ByteSize()\n",
			want:   []string{"len(b.Items)", "utf8.RuneCountInString(b.Text)", "len(b.Text)"},
			absent: []string{".Size()", ".ByteSize()"},
		},
		{
			name: "pointer-receiver method on a val binding of a Go-declared type is called on a copy (#614)",
			goSrc: "type Greeter struct{ Name string }\n\n" +
				"func (g Greeter) Hello() string    { return \"hello \" + g.Name }\n" +
				"func (g *Greeter) Rename(s string) { g.Name = s }\n\n" +
				"func NewGreeter(n string) Greeter { return Greeter{Name: n} }\n",
			galaSrc: "func greet() string {\n" +
				"    x := NewGreeter(\"world\")\n" +
				"    x.Rename(\"there\")\n" +
				"    return x.Hello()\n" +
				"}\n",
			want:   []string{"std.AddrOfCopy(x.Get()).Rename(\"there\")"},
			absent: []string{"\tx.Get().Rename("},
		},
		{
			name: "resource.Using infers its resource from a Go-declared constructor (#618)",
			goSrc: "type Res struct{ name string }\n\n" +
				"func (r Res) Close() error { return nil }\n" +
				"func (r Res) Name() string { return r.name }\n\n" +
				"func OpenRes(p string) Res { return Res{name: p} }\n",
			galaSrc: "import \"martianoff/gala/resource\"\n\n" +
				"func nameLen() int = resource.Using(OpenRes(\"x\"), (r) => r.Name().Size())\n",
			want:   []string{"func(r Res) int", "utf8.RuneCountInString(r.Name())"},
			absent: []string{"any"},
		},
		{
			name: "unexported declarations are the package's own too",
			goSrc: "type bag struct {\n\titems []string\n\ttext  string\n}\n\n" +
				"func newBag() bag { return bag{items: []string{\"a\"}} }\n",
			galaSrc: "func count(b bag) int = b.items.Size()\n" +
				"func total() int {\n" +
				"    val b = newBag()\n" +
				"    return b.items.Size() + b.text.Size()\n" +
				"}\n",
			want:   []string{"len(b.items)", "len(b.Get().items)", "utf8.RuneCountInString(b.Get().text)"},
			absent: []string{".Size()"},
		},
		{
			// The check is that it transpiles at all: without the subject's
			// type this was "cannot infer type of matched expression".
			name: "a literal match on a Go-declared named scalar infers its type",
			goSrc: "type Status int\n\n" +
				"const Active Status = 1\n\n" +
				"func Current() Status { return Active }\n",
			galaSrc: "func describe() string = Current() match {\n" +
				"    case 1 => \"active\"\n" +
				"    case _ => \"other\"\n" +
				"}\n",
			want: []string{"Current()"},
		},
		{
			name:  "a codec field may have a Go-declared named scalar type",
			goSrc: "type Millis int64\n",
			galaSrc: "import \"martianoff/gala/json\"\n\n" +
				"struct Event(Name string, At Millis)\n\n" +
				"func encode(e Event) string = json.Codec[Event](json.SnakeCase()).Encode(e).GetOrElse(\"\")\n",
			want: []string{"Millis("},
		},
	}
	layouts := []struct {
		name, dir, pkg string
	}{
		{"package main", ".", "main"},
		{"library package", "lib", "lib"},
	}
	for _, tc := range cases {
		for _, l := range layouts {
			t.Run(tc.name+"/"+l.name, func(t *testing.T) {
				files, galaFile := samePackageModule(l.dir,
					"package "+l.pkg+"\n\n"+tc.goSrc,
					"package "+l.pkg+"\n\n"+tc.galaSrc)
				out, err := transpileInModule(t, files, galaFile)
				require.NoError(t, err)
				for _, w := range tc.want {
					assert.Contains(t, out, w)
				}
				for _, a := range tc.absent {
					assert.NotContains(t, out, a)
				}
			})
		}
	}
}

// TestSamePackageGoSiblingNoCopyReceiver covers GALA-E0053 for a type declared
// in a hand-written .go file of `package main`: a val holding a struct with a
// mutex cannot have a pointer-receiver method called on it, since the call
// would run on a copy. Without the type's Go metadata the check said nothing
// and `go build` failed on generated code instead (#614).
func TestSamePackageGoSiblingNoCopyReceiver(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\nimport \"sync\"\n\n"+
			"type Guarded struct {\n\tmu sync.Mutex\n\tN  int\n}\n\n"+
			"func (g *Guarded) Bump() { g.mu.Lock(); g.N++; g.mu.Unlock() }\n\n"+
			"func NewGuarded() Guarded { return Guarded{} }\n",
		"package main\n\nfunc main() {\n    val g = NewGuarded()\n    g.Bump()\n}\n")
	_, err := transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0053")
	assert.Contains(t, err.Error(), "sync.Mutex")
}

// TestSamePackageGoSiblingScanReadsOnlyThePackage covers the files the scan
// must leave out: a .go file of another package in the same directory, and one
// whose build constraints exclude it. Neither is part of the package `go build`
// compiles, so neither may lend it a declaration.
func TestSamePackageGoSiblingScanReadsOnlyThePackage(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\ntype Bag struct{ Items []string }\n",
		"package main\n\nfunc count(b Bag) int = b.Items.Size()\n")
	// A generator in package main, excluded by its build constraint.
	files["gen.go"] = "//go:build ignore\n\npackage main\n\ntype Other struct{ Items string }\n\nfunc MakeOther() Other { return Other{} }\n"
	// A file of another package in the same directory.
	files["tool.go"] = "package tool\n\nfunc ToolOnly() []string { return nil }\n"
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "len(b.Items)")

	// Were either result typed, `.Size()` on it would be lowered; unknown to
	// the transpiler, it is passed through as written.
	for _, call := range []string{"MakeOther().Items", "ToolOnly()"} {
		files[galaFile] = "package main\n\nfunc f() int = " + call + ".Size()\n"
		out, err := transpileInModule(t, files, galaFile)
		require.NoError(t, err)
		assert.Contains(t, out, call+".Size()", "%s is not declared in package main and must not be typed", call)
	}
}
