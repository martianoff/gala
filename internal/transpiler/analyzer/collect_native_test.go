package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// parseWithGalop makes tb's parses use galop's parser whatever the build's
// or environment's default, until tb ends.
func parseWithGalop(tb testing.TB) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	tb.Cleanup(func() { galaparser.RequestEngine("") })
}

// collectionCorpus is the paths of the GALA sources staged as the test's
// data (those TestEnginesAgree parses), sorted, and the search path that
// resolves their imports.
func collectionCorpus(tb testing.TB) ([]string, []string) {
	stdFile, err := bazel.Runfile("std/option.gala")
	if err != nil {
		tb.Skipf("std sources not staged: %v", err)
	}
	files, err := bazel.ListRunfiles()
	if err != nil {
		tb.Fatalf("cannot list the runfiles: %v", err)
	}
	var paths []string
	for _, f := range files {
		if !strings.HasSuffix(f.ShortPath, ".gala") {
			continue
		}
		if info, err := os.Stat(f.Path); err != nil || !info.Mode().IsRegular() {
			continue // a directory named *.gala, such as a tool's .gala cache
		}
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	return paths, []string{filepath.Dir(filepath.Dir(stdFile))}
}

// newCollectionAnalyzer is a batch analyzer over searchPaths parsing with p,
// with no disk cache, so every package it imports it analyzes itself. Its
// project root is a temporary directory, so it writes nothing among the
// sources.
func newCollectionAnalyzer(tb testing.TB, p transpiler.GalaParser, searchPaths []string) *galaAnalyzer {
	a := NewBatchAnalyzer(p, searchPaths, tb.TempDir()).inner
	a.cache = nil
	return a
}

// newCollectedFile starts a's collection of the file at path, of package
// pkgName, into an empty RichAST, as analyze does before collecting a file.
func newCollectedFile(a *galaAnalyzer, pkgName, path string, docs galaparser.Docs) *fileDecls {
	richAST := &transpiler.RichAST{
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
	}
	a.currentRichAST = richAST
	a.currentOwnTypes = make(map[string]bool)
	a.currentOwnMethods = make(map[string]map[string]bool)
	return &fileDecls{richAST: richAST, pkgName: pkgName, filePath: path, absFilePath: path, docs: docs, decls: newPackageDecls(path)}
}

// analysis is what analyzing a file gives: the metadata, or the error.
type analysis struct {
	result any
}

// analyzeForCollection analyzes path, whose source is text, with a, parsing
// it with a.parser; packageFiles are the other files of its package, or nil
// to let the analyzer find library siblings in its directory. ok is false
// when the source does not parse: the transpiler stops at a syntax error,
// before analysis.
func analyzeForCollection(tb testing.TB, a *galaAnalyzer, path, text string, packageFiles []string) (an analysis, ok bool) {
	tb.Helper()
	parsed := a.parser.ParseFile(text)
	if parsed.Err() != nil {
		return analysis{}, false
	}
	return analyzeParsedForCollection(tb, a, path, parsed, packageFiles), true
}

// analyzeParsedForCollection is analyzeForCollection of parsed, a parse
// without errors.
func analyzeParsedForCollection(tb testing.TB, a *galaAnalyzer, path string, parsed *galaparser.Parsed, packageFiles []string) (an analysis) {
	tb.Helper()
	a.packageFiles = packageFiles
	a.checkedDirs = make(map[string]bool)
	// A package that failed to load stands the undefined-symbol check down
	// for every later file of the batch (see fileImportsFullyLoaded), and with
	// it the native collection. The corpus's examples import a few packages
	// the test does not stage, so each file starts with no failure recorded:
	// one that imports such a package still finds it unloaded.
	clear(a.packageLoadFailures)
	res, err := a.AnalyzeParsed(parsed, path)
	if err != nil {
		an.result = err
		return an
	}
	an.result = res
	return an
}

// collectionErrorInputs are sources whose analysis fails in the collection
// of the file's declarations, each diagnostic of it: by package, the files,
// and the file analyzed first is the first listed. Their positions cover a
// declaration's start, its name's, a field's, a case's and a default
// expression's span, after multibyte text too.
var collectionErrorInputs = []struct {
	name  string
	files [][2]string
}{
	{"type redefined", [][2]string{{"main.gala", "package main\n\ntype User struct {\n    Name string\n}\n\ntype User struct {\n    Email string\n}\n"}}},
	{"shorthand redefined", [][2]string{{"main.gala", "package main\n\nstruct P(x int)\n\nstruct P(y int)\n"}}},
	{"sealed type redefined", [][2]string{{"main.gala", "package main\n\nstruct S(x int)\n\nsealed type S {\n    case A()\n}\n"}}},
	{"opaque type redefined", [][2]string{{"main.gala", "package main\n\ntype Id struct {\n    V int\n}\n\nopaque type Id int64\n"}}},
	{"method redefined", [][2]string{{"main.gala", "package main\n\nstruct P(x int)\n\nfunc (p P) Get() int = p.x\n\nfunc (p P) Get() int = 2\n"}}},
	{"function redeclared", [][2]string{{"main.gala", "package main\n\nfunc f() int = 1\n\nfunc f() int = 2\n"}}},
	{"function redeclared in a sibling", [][2]string{
		{"a.gala", "package main\n\nfunc f() int = 1\n"},
		{"b.gala", "package main\n\nfunc f() int = 2\n"},
	}},
	{"type redefined in a sibling", [][2]string{
		{"a.gala", "package main\n\ntype User struct {\n    Name string\n}\n"},
		{"b.gala", "package main\n\ntype User struct {\n    Email string\n}\n"},
	}},
	{"method redefined in a sibling", [][2]string{
		{"a.gala", "package main\n\nstruct P(x int)\n\nfunc (p P) Get() int = p.x\n"},
		{"b.gala", "package main\n\nfunc (p P) Get() int = 2\n"},
	}},
	{"duplicate struct field", [][2]string{{"main.gala", "package main\n\ntype P struct {\n    A int\n    A string\n}\n"}}},
	{"duplicate shorthand field", [][2]string{{"main.gala", "package main\n\nstruct Q(a int, b int, a string)\n"}}},
	{"duplicate interface method", [][2]string{{"main.gala", "package main\n\ntype R interface {\n    Run() int\n    Run() string\n}\n"}}},
	{"type parameter on an interface method", [][2]string{{"main.gala", "package main\n\ntype Pair[A any] interface {\n    Zip[B any, C comparable](b B, c C) A\n}\n"}}},
	{"duplicate sealed case", [][2]string{{"main.gala", "package main\n\nsealed type Shape {\n    case Dot()\n    case Dot()\n}\n"}}},
	{"std conflict: type", [][2]string{{"main.gala", "package main\n\ntype Option struct {\n    X int\n}\n"}}},
	{"std conflict: shorthand", [][2]string{{"main.gala", "package main\n\nstruct Try(x int)\n"}}},
	{"std conflict: opaque type", [][2]string{{"main.gala", "package main\n\nopaque type Either int\n"}}},
	{"std conflict: function", [][2]string{{"main.gala", "package main\n\nfunc Some() int = 1\n"}}},
	{"default of the wrong type", [][2]string{{"main.gala", "package main\n\nfunc f(x int = \"s\") int = x\n"}}},
	{"default of the wrong type after multibyte text", [][2]string{{"main.gala", "package main\n\nfunc g(a string = \"üñï\", b int = \"äöü\") int = b\n"}}},
	{"multiline default of the wrong type", [][2]string{{"main.gala", "package main\n\nfunc h(n int = \"a\" +\n    \"b\") int = n\n"}}},
	{"sealed case default of the wrong type", [][2]string{{"main.gala", "package main\n\nsealed type Box {\n    case Full(n int = \"é\")\n}\n"}}},
}

// docExamples are the GALA examples of the error reference pages
// (docs/errors), as packages of files: a page's examples that name their
// files (`// a.gala`) form one package until a name repeats, and an example
// that names none is a package of its own.
func docExamples(tb testing.TB) [][][2]string {
	page, err := bazel.Runfile("docs/errors/GALA-E0011.md")
	if err != nil {
		tb.Fatalf("the error reference pages are not staged: %v", err)
	}
	pages, err := filepath.Glob(filepath.Join(filepath.Dir(page), "GALA-E*.md"))
	if err != nil || len(pages) < 50 {
		tb.Fatalf("found %d error reference pages: %v", len(pages), err)
	}
	sort.Strings(pages)
	var pkgs [][][2]string
	for _, path := range pages {
		text, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		var pkg [][2]string
		names := make(map[string]bool)
		flush := func() {
			if len(pkg) > 0 {
				pkgs = append(pkgs, pkg)
			}
			pkg, names = nil, make(map[string]bool)
		}
		for _, block := range strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "```gala\n")[1:] {
			src, _, _ := strings.Cut(block, "```")
			name := ""
			if first, _, _ := strings.Cut(src, "\n"); strings.HasPrefix(first, "// ") && strings.HasSuffix(first, ".gala") {
				name = strings.TrimPrefix(first, "// ")
			}
			if name == "" || names[name] {
				flush()
			}
			if name == "" {
				pkgs = append(pkgs, [][2]string{{"main.gala", src}})
				continue
			}
			names[name] = true
			pkg = append(pkg, [2]string{name, src})
		}
		flush()
	}
	return pkgs
}

// TestCollectionTakesEveryParse: a clean parse and an error-recovered one
// both give a native tree to collect on (the error-recovered tree is what an
// editor's buffer is analyzed on mid-edit, see Parsed.RecoveredNative); no
// handle gives none, an error.
func TestCollectionTakesEveryParse(t *testing.T) {
	parseWithGalop(t)
	p := galaparser.NewAntlrGalaParser()
	broken := p.ParseFile("package main\n\nfunc f( = 1\n")
	if len(broken.Errors) == 0 {
		t.Fatal("the input must have syntax errors")
	}
	if broken.CleanNative() != nil {
		t.Fatal("a parse with syntax errors has a clean handle")
	}
	if _, err := nativeFileOf(broken.RecoveredNative()); err != nil {
		t.Fatalf("an error-recovered parse has no native tree: %v", err)
	}
	if _, err := nativeFileOf(nil); err == nil {
		t.Fatal("a nil handle has a native tree")
	}
	if _, err := nativeFileOf(p.ParseFile("package main\n\nfunc f() int = 1\n").CleanNative()); err != nil {
		t.Fatalf("a clean parse has no native tree: %v", err)
	}
}

// TestRecollectedTypeParams: collecting a file's types again reuses each
// type's entry, and both twins give it the type parameters of the declaration
// collected last, rather than appending them to the ones it holds.
func TestRecollectedTypeParams(t *testing.T) {
	parseWithGalop(t)
	const generic = `package box

type Box[T comparable] struct {
    V T
}

struct Pair[A any, B comparable](a A, b B)
`
	type typeParams struct {
		params      []string
		constraints map[string]string
	}
	genericWant := map[string]typeParams{
		"box.Box":  {[]string{"T"}, map[string]string{"T": "comparable"}},
		"box.Pair": {[]string{"A", "B"}, map[string]string{"A": "any", "B": "comparable"}},
	}
	cases := []struct {
		name  string
		again string // the source collected the second time
		want  map[string]typeParams
	}{
		{"same declarations", generic, genericWant},
		{"edited declarations", `package box

type Box[K any] struct {
    V K
}

struct Pair(a int, b int)
`, map[string]typeParams{
			"box.Box":  {[]string{"K"}, map[string]string{"K": "any"}},
			"box.Pair": {nil, nil},
		}},
	}
	for _, c := range cases {
		for name, collect := range typeCollectors {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
				fd := collectTwice(t, a, collect, generic, c.again, nil)
				for key, w := range c.want {
					meta := fd.richAST.Types[key]
					if meta == nil {
						t.Fatalf("%s was not collected", key)
					}
					if !reflect.DeepEqual(meta.TypeParams, w.params) {
						t.Errorf("%s: TypeParams = %v, want %v", key, meta.TypeParams, w.params)
					}
					if !reflect.DeepEqual(meta.TypeParamConstraints, w.constraints) {
						t.Errorf("%s: TypeParamConstraints = %v, want %v", key, meta.TypeParamConstraints, w.constraints)
					}
				}
			})
		}
	}
}

// typeCollector collects the types of a parsed file into fd.
type typeCollector func(*galaAnalyzer, *galaparser.Parsed, *fileDecls) error

// typeCollectors are the collections of a file's types, by name.
var typeCollectors = map[string]typeCollector{
	"native": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		nf, err := nativeFileOf(p.CleanNative())
		if err != nil {
			return err
		}
		return a.collectFileTypesNative(nf, fd)
	},
}

// parseClean parses src, failing tb when it does not parse.
func parseClean(tb testing.TB, src string) *galaparser.Parsed {
	tb.Helper()
	parsed := galaparser.NewAntlrGalaParser().ParseFile(src)
	if parsed.Err() != nil {
		tb.Fatal(parsed.Err())
	}
	return parsed
}

// collectOnce collects with a the types of src, the file of package box at
// path, into a fresh RichAST.
func collectOnce(tb testing.TB, a *galaAnalyzer, collect typeCollector, path, src string) *fileDecls {
	tb.Helper()
	parsed := parseClean(tb, src)
	fd := newCollectedFile(a, "box", path, parsed.Docs)
	if err := collect(a, parsed, fd); err != nil {
		tb.Fatalf("collection of %s: %v", path, err)
	}
	return fd
}

// collectTwice collects the types of first, the file box.gala of package box,
// then of again as the same file under another spelling, so the second
// collection reuses the entries of the first rather than reporting them
// redefined. between, when not nil, runs on the first collection's result.
// The returned fileDecls has the second spelling as its filePath.
func collectTwice(tb testing.TB, a *galaAnalyzer, collect typeCollector, first, again string, between func(*fileDecls)) *fileDecls {
	tb.Helper()
	dir := tb.TempDir()
	fd := collectOnce(tb, a, collect, filepath.Join(dir, "box.gala"), first)
	if between != nil {
		between(fd)
	}
	parsed := parseClean(tb, again)
	fd.filePath = dir + string(filepath.Separator) + "." + string(filepath.Separator) + "box.gala"
	fd.docs = parsed.Docs
	if err := collect(a, parsed, fd); err != nil {
		tb.Fatalf("second collection: %v", err)
	}
	return fd
}

// TestRecollectedTypeChangesForm: a type whose declaration changes form
// between two collections of its file (a shorthand struct, a block struct, an
// interface) gets, from both twins, the entry a first collection of its new
// declaration gives, with no flag, field or interface method left from the
// old form. The methods declared on a struct, and the DefinedIn an interface
// does not set, survive the re-collection.
func TestRecollectedTypeChangesForm(t *testing.T) {
	parseWithGalop(t)
	forms := []struct{ name, src string }{
		{"shorthand", "package box\n\nstruct Cfg[T any](Name string, Tries int = 3, Extra T)\n"},
		{"block", "package box\n\ntype Cfg struct {\n    var Name string\n    Size int\n}\n"},
		{"interface", "package box\n\ntype Cfg interface {\n    Name() string\n    Tries(n int) int\n}\n"},
	}
	// A method another file declares on Cfg, added between the collections.
	declared := &transpiler.MethodMetadata{Name: "Describe", Package: "box", DefinedIn: "other.gala"}
	for _, from := range forms {
		for _, to := range forms {
			if from.name == to.name {
				continue
			}
			for name, collect := range typeCollectors {
				t.Run(from.name+"_to_"+to.name+"/"+name, func(t *testing.T) {
					keepsMethods := from.name != "interface"
					var firstDefinedIn string
					a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
					fd := collectTwice(t, a, collect, from.src, to.src, func(fd *fileDecls) {
						meta := fd.richAST.Types["box.Cfg"]
						firstDefinedIn = meta.DefinedIn
						if keepsMethods {
							meta.Methods[declared.Name] = declared
						}
					})
					fresh := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
					want := collectOnce(t, fresh, collect, fd.filePath, to.src).richAST.Types["box.Cfg"]
					if keepsMethods {
						want.Methods[declared.Name] = declared
					}
					// An interface declaration does not record DefinedIn, so
					// the one recorded before stays.
					if to.name == "interface" {
						want.DefinedIn = firstDefinedIn
					}
					if got := fd.richAST.Types["box.Cfg"]; !reflect.DeepEqual(got, want) {
						t.Errorf("re-collected entry:\n got %+v\nwant %+v", *got, *want)
					}
				})
			}
		}
	}
}

// valInferenceInputs are packages of one file whose package-level
// initializers take each shape inferPackageValInitTypeNative reads, and the
// shapes it turns down, so the analysis golden covers every branch, whatever
// the corpus holds.
var valInferenceInputs = []string{
	`package main

import "strings"

struct Point(X int, Y int)

struct Hidden(x int)

type Pair struct {
    A int
    B string
}

func (p Point) Apply(n int) Point = Point(n, n)

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

type Box[T any] struct {
    V T
}

func id[T any](x T) T = x

func two() (int, string) = (2, "b")

func none() {
}

val Full = Point(1, 2)
val Short = Point(1)
val NoArgs = Point()
val Lit = Pair(1, "a")
val Priv = Hidden(1)
val Case = Circle(1.0)
val Generic = id(1)
val GenericBox = Box[int](1)
val Tuple = two()
val Void = none()
val Field = Full.X
val Chain = Point(1).Apply(2)
val Paren = (1)
val Unit = ()
val NegFloat = -1.5
val Plus = +2
val Not = !true
val Sum = 1 + 2
val ESum = Earlier + 1
val Mask = -0x10
val HexE = -0xE
val Exp = -1e3
val Concat = "a" + "b"
val Ref = Full
val Later = Earlier
val Earlier = 3
val Upper = strings.ToUpper("a")
val Compare = strings.Compare
val Builder = strings.Builder{}
val Nil = nil
val Raw = ` + "`raw`" + `
val Format = f"${1}%d"
val Rune = 'r'
val Interp = s"v=$Earlier"
val Eq = "a" == "b"
val Float = 1.5
val Bool = true
val Int = 7
val Str = "s"
val Count = Earlier
val Lambda = (x int) => x
val Cond = if (true) 1 else 2
val Opt = Some(1)
val Matched = 3 match {
    case 3 => "three"
    case _ => "other"
}
val (TA, TB) = (1, 2)
val X, Y = 1, "y"
val P, Q = two()
val Typed int = 4
var Mutable = Full
var VoidVar = none()
val _ = 5
`,
}

// valShapesSrc holds initializers that the package-level initializer
// inference once typed wrongly, which a golden made of its results would
// not catch.
const valShapesSrc = `package shapes

val E = 2
val ESum = E + 1
val EDiff = E - 1
val Matched = 3 match {
    case 3 => "three"
    case _ => "other"
}
val Mask = -0x10
val HexE = -0xE
val Bin = -0b101
val Oct = +0o17
val Exp = -1e3
`

// valShapes is, for each binding of valShapesSrc, the type the initializer
// inference gives its initializer ("" for none: the shape is not one it
// settles) and the type the binding records once the transformer's own
// inference has typed what it left untyped.
var valShapes = []struct{ name, init, inferred, recorded string }{
	// An operand named like an exponent makes no number.
	{"ESum", "E+1", "", "int"},
	{"EDiff", "E-1", "", "int"},
	// A trailing match is typed by its arms, not by its scrutinee.
	{"Matched", `3match{case3=>"three"case_=>"other"}`, "", "string"}, // A signed literal takes its literal's type, whatever its base.
	{"Mask", "-0x10", "int", "int"},
	{"HexE", "-0xE", "int", "int"},
	{"Bin", "-0b101", "int", "int"},
	{"Oct", "+0o17", "int", "int"},
	{"Exp", "-1e3", "float64", "float64"},
}

// TestValInitTypeShapes: the initializer inference of valShapesSrc's
// bindings gives each the type valShapes holds, and the binding records its
// own type — never a wrong one in place of one the inference cannot tell.
func TestValInitTypeShapes(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	shapeType := func(typ transpiler.Type) string {
		if typ == nil || typ.IsNil() {
			return ""
		}
		return typ.String()
	}
	a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), searchPaths)
	path := filepath.Join(t.TempDir(), "shapes.gala")
	if err := os.WriteFile(path, []byte(valShapesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	inferred := map[string]string{}
	a.onInferInit = func(e native.ExpressionNode, got transpiler.Type) {
		inferred[e.GetText()] = shapeType(got)
	}
	an, ok := analyzeForCollection(t, a, path, valShapesSrc, []string{})
	a.onInferInit = nil
	if !ok {
		t.Fatal("valShapesSrc does not parse")
	}
	rich, ok := an.result.(*transpiler.RichAST)
	if !ok {
		t.Fatalf("analyze: %v", an.result)
	}
	for _, s := range valShapes {
		if got, ok := inferred[s.init]; !ok {
			t.Errorf("%s: %q was not inferred", s.name, s.init)
		} else if got != s.inferred {
			t.Errorf("%s: %q is inferred as %q, want %q", s.name, s.init, got, s.inferred)
		}
		pv := rich.PackageVals[s.name]
		if pv == nil {
			t.Fatalf("%s is not recorded", s.name)
		}
		if got := shapeType(pv.Type); got != s.recorded {
			t.Errorf("%s records %q, want %q", s.name, got, s.recorded)
		}
	}
}

// visit is a pair of pointers firstDiff has compared, or is comparing.
type visit struct {
	a, b uintptr
	typ  reflect.Type
}

// firstDiff is where x and y first differ, as a path from the values
// compared, or "" when they are deeply equal (reflect.DeepEqual). Maps are
// compared in key order, so the path it reports is the same on every run.
func firstDiff(x, y reflect.Value, path string, seen map[visit]bool) string {
	if !x.IsValid() || !y.IsValid() {
		if x.IsValid() == y.IsValid() {
			return ""
		}
		return fmt.Sprintf("%s: %v vs %v", path, x, y)
	}
	if x.Type() != y.Type() {
		return fmt.Sprintf("%s: %s vs %s", path, x.Type(), y.Type())
	}
	switch x.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if x.IsNil() != y.IsNil() {
			return fmt.Sprintf("%s: nil %v vs %v", path, x.IsNil(), y.IsNil())
		}
		if x.IsNil() {
			return ""
		}
		if x.Kind() != reflect.Slice || x.Len() > 0 {
			v := visit{x.Pointer(), y.Pointer(), x.Type()}
			if seen[v] || x.Pointer() == y.Pointer() && x.Kind() != reflect.Slice {
				return ""
			}
			seen[v] = true
		}
	}
	switch x.Kind() {
	case reflect.Pointer:
		return firstDiff(x.Elem(), y.Elem(), path, seen)
	case reflect.Interface:
		if x.IsNil() || y.IsNil() {
			if x.IsNil() == y.IsNil() {
				return ""
			}
			return fmt.Sprintf("%s: nil %v vs %v", path, x.IsNil(), y.IsNil())
		}
		return firstDiff(x.Elem(), y.Elem(), path, seen)
	case reflect.Struct:
		for i := range x.NumField() {
			if d := firstDiff(x.Field(i), y.Field(i), path+"."+x.Type().Field(i).Name, seen); d != "" {
				return d
			}
		}
		return ""
	case reflect.Slice, reflect.Array:
		if x.Len() != y.Len() {
			return fmt.Sprintf("%s: length %d vs %d", path, x.Len(), y.Len())
		}
		for i := range x.Len() {
			if d := firstDiff(x.Index(i), y.Index(i), fmt.Sprintf("%s[%d]", path, i), seen); d != "" {
				return d
			}
		}
		return ""
	case reflect.Map:
		if x.Len() != y.Len() {
			return fmt.Sprintf("%s: %d entries vs %d (%v vs %v)", path, x.Len(), y.Len(), sortedKeys(x), sortedKeys(y))
		}
		for _, k := range sortedKeys(x) {
			yv := y.MapIndex(k)
			if !yv.IsValid() {
				return fmt.Sprintf("%s[%v]: only in the first", path, k)
			}
			if d := firstDiff(x.MapIndex(k), yv, fmt.Sprintf("%s[%v]", path, k), seen); d != "" {
				return d
			}
		}
		return ""
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if x.Pointer() == y.Pointer() {
			return ""
		}
		return fmt.Sprintf("%s: %s values differ", path, x.Kind())
	default:
		if x.CanInterface() && y.CanInterface() {
			if !reflect.DeepEqual(x.Interface(), y.Interface()) {
				return fmt.Sprintf("%s: %#v vs %#v", path, x.Interface(), y.Interface())
			}
			return ""
		}
		if fmt.Sprint(x) != fmt.Sprint(y) {
			return fmt.Sprintf("%s: %v vs %v", path, x, y)
		}
		return ""
	}
}

// sortedKeys is m's keys, sorted by their printed form.
func sortedKeys(m reflect.Value) []reflect.Value {
	keys := m.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	return keys
}

// BenchmarkCollect collects the declarations of every staged source on its
// native tree, converted beforehand ("native"), as the file being analyzed
// ("file": analyze's sections 1 to 2b). "convert" is the conversion to the
// native tree itself, which every file pays once.
func BenchmarkCollect(b *testing.B) {
	parseWithGalop(b)
	paths, searchPaths := collectionCorpus(b)
	type source struct {
		path, pkgName string
		parsed        *galaparser.Parsed
	}
	parseAll := func() []source {
		var out []source
		for _, path := range paths {
			text, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			parsed := galaparser.NewAntlrGalaParser().ParseFile(string(text))
			if parsed.Err() != nil {
				continue
			}
			nf, err := nativeFileOf(parsed.Native)
			if err != nil {
				b.Fatal(err)
			}
			out = append(out, source{path, galaparser.NativeText(nf.root.PackageClause().Identifier().GalaNodeRef), parsed})
		}
		return out
	}

	b.Run("convert", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			sources := parseAll()
			b.StartTimer()
			for _, s := range sources {
				s.parsed.Native.Tree()
			}
		}
	})

	sources := parseAll()
	for _, s := range sources {
		if _, err := s.parsed.Native.Tree(); err != nil {
			b.Fatal(err)
		}
	}
	a := newCollectionAnalyzer(b, galaparser.NewAntlrGalaParser(), searchPaths)
	b.Run("native/file", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, s := range sources {
				fd := newCollectedFile(a, s.pkgName, s.path, s.parsed.Docs)
				nf, err := nativeFileOf(s.parsed.Native)
				if err != nil {
					b.Fatal(err)
				}
				if a.collectFileTypesNative(nf, fd) == nil && a.collectFileFuncsNative(nf, fd) == nil {
					a.fileTypeAliasesNative(nf, fd.pkgName)
					collectFileEmbedsNative(nf, fd.richAST)
					a.extractPackageValsNative(nf, fd.pkgName, fd.richAST, fd.docs, fd.absFilePath)
				}
			}
		}
	})
}
