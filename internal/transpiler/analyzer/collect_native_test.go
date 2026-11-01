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
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// parseWithGalop makes tb's parses use galop's parser whatever the build's
// or environment's default, until tb ends: only its parses come with a
// native tree, which these tests compare ANTLR's collection to.
func parseWithGalop(tb testing.TB) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	tb.Cleanup(func() { galaparser.RequestEngine("") })
}

// antlrOnly parses as its parser does but gives no native-tree handle, so an
// analyzer using it collects every file on ANTLR's tree.
type antlrOnly struct{ p transpiler.GalaParser }

func (o antlrOnly) ParseFile(input string) *galaparser.Parsed {
	parsed := o.p.ParseFile(input)
	parsed.Native = nil
	return parsed
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

// analysis is what analyzing a file gives: the metadata, or the error, and
// the tree its declarations were collected on (see collectTrace).
type analysis struct {
	result any
	trace  collectTrace
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
	// The file's own trace, which onCollect reports alone: no trace of an
	// imported package's file, which might be this one, stands in for it
	// when the analysis fails before the collection.
	a.onCollect = func(t collectTrace) { an.trace = t }
	defer func() { a.onCollect = nil }()
	a.packageFiles = packageFiles
	a.checkedDirs = make(map[string]bool)
	// A package that failed to load stands the undefined-symbol check down
	// for every later file of the batch (see fileImportsFullyLoaded), and with
	// it the native collection. The corpus's examples import a few packages
	// the test does not stage, so each file starts with no failure recorded:
	// one that imports such a package still finds it unloaded.
	clear(a.packageLoadFailures)
	res, err := a.AnalyzeParsed(parsed, path)
	if an.trace.filePath != "" && an.trace.filePath != path {
		tb.Errorf("%s: analyze reported the collection of %s", path, an.trace.filePath)
	}
	if err != nil {
		an.result = err
		return an
	}
	res.Tree = nil // each analysis has its own parse
	an.result = res
	return an
}

// checkTrace fails tb unless an's file was collected on the tree it should
// have been: on its native tree exactly when its parse came with a handle
// (wantHandle, which a galop parse without syntax errors has), whether or not
// the undefined-symbol check runs on it, so a converter or bridge that fails
// quietly cannot turn the comparison into ANTLR's against ANTLR's.
func checkTrace(tb testing.TB, name string, an analysis, wantHandle bool) bool {
	tb.Helper()
	switch t := an.trace; {
	case t.filePath == "":
		// Analysis stopped before the collection: its result is an error.
		if _, isErr := an.result.(error); isErr {
			return true
		}
		tb.Errorf("%s: analyze reported no collection", name)
	case t.hasHandle != wantHandle:
		tb.Errorf("%s: the parse came with a handle: %v, want %v", name, t.hasHandle, wantHandle)
	case t.native != t.hasHandle:
		tb.Errorf("%s: collected on the native tree: %v, want %v (whether the parse came with a handle)", name, t.native, t.hasHandle)
	default:
		return true
	}
	return false
}

// TestCollectionsAgree holds the collection of the compiled file's
// declarations on its native tree (collect_native.go) to the collection on
// ANTLR's: every GALA source staged as data, analyzed as the CLI analyzes a
// file, with its library siblings and imports, gives the same metadata, or
// the same error, when its declarations are collected on its native tree as
// when they are collected on ANTLR's. Each file's collection must have taken
// the tree it should (checkTrace), and most must have taken the native one.
func TestCollectionsAgree(t *testing.T) {
	parseWithGalop(t)
	paths, searchPaths := collectionCorpus(t)
	p := galaparser.NewAntlrGalaParser()
	nativeA := newCollectionAnalyzer(t, p, searchPaths)
	antlrA := newCollectionAnalyzer(t, antlrOnly{p}, searchPaths)
	// The reads of the files besides the one analyzed: its siblings' and the
	// files of the packages it imports (the proof the sibling and import reads
	// took their tree). Each must take the tree its analyzer reads.
	otherReads := map[string]int{}
	var subject string
	checkReads := func(onNative bool) func(treeRead) {
		return func(r treeRead) {
			if r.filePath == subject {
				return
			}
			if r.native != onNative {
				t.Errorf("%s: the read of %s's %s took the native tree: %v, want %v", subject, r.filePath, r.what, r.native, onNative)
			}
			if onNative {
				otherReads[r.what]++
				if r.antlr != nil {
					if diff := firstDiff(reflect.ValueOf(r.got), reflect.ValueOf(r.antlr()), "", map[visit]bool{}); diff != "" {
						t.Errorf("%s: the read of %s's %s differs between the trees at %s", subject, r.filePath, r.what, diff)
					}
				}
			}
		}
	}
	nativeA.onTreeRead, nativeA.onPackageRead = checkReads(true), checkReads(true)
	antlrA.onTreeRead, antlrA.onPackageRead = checkReads(false), checkReads(false)
	analyzed, native := 0, 0
	for _, path := range paths {
		subject = path
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		n, ok := analyzeForCollection(t, nativeA, path, string(text), nil)
		if !ok {
			continue
		}
		a, _ := analyzeForCollection(t, antlrA, path, string(text), nil)
		analyzed++
		if !checkTrace(t, path+" (native run)", n, true) || !checkTrace(t, path+" (ANTLR run)", a, false) {
			continue
		}
		if n.trace.native {
			native++
		}
		if diff := firstDiff(reflect.ValueOf(n.result), reflect.ValueOf(a.result), "", map[visit]bool{}); diff != "" {
			t.Errorf("%s: the native collection differs from ANTLR's at %s", path, diff)
		}
	}
	// The repository's sources analyze, and nearly all of them load their
	// imports, so the check runs and they are collected natively; floors
	// catch the sources not being staged or the native path going unused.
	if analyzed < 200 || native < analyzed*3/4 {
		t.Fatalf("analyzed %d sources, %d of them collected on the native tree", analyzed, native)
	}
	t.Logf("analyzed %d sources, %d of them collected on the native tree; reads of the other files: %v", analyzed, native, otherReads)
	// Every kind of read of a sibling or an imported file was made, often.
	for _, what := range []string{"imports", "top-level names", "declarations", "val types", "package clause"} {
		if otherReads[what] < 20 {
			t.Errorf("%d native reads of other files' %s", otherReads[what], what)
		}
	}
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

// TestCollectionDiagnosticsAgree holds the collection's diagnostics on the
// native tree to ANTLR's: each file of each source of collectionErrorInputs
// and of every example of the error reference pages, analyzed with the other
// files of its package, gives the same metadata, or the same error with the
// same code, message, position and span, whichever tree its declarations
// are collected on. Every input of collectionErrorInputs must fail, and be
// collected on its native tree.
func TestCollectionDiagnosticsAgree(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	p := galaparser.NewAntlrGalaParser()
	nativeA := newCollectionAnalyzer(t, p, searchPaths)
	antlrA := newCollectionAnalyzer(t, antlrOnly{p}, searchPaths)

	type input struct {
		name  string
		files [][2]string
		// mustFail says the input exists to fail in the collection, on its
		// native tree.
		mustFail bool
	}
	var inputs []input
	for _, in := range collectionErrorInputs {
		inputs = append(inputs, input{in.name, in.files, true})
	}
	for i, files := range docExamples(t) {
		inputs = append(inputs, input{fmt.Sprintf("error reference example %d (%s)", i, files[0][0]), files, false})
	}

	compared, failed := 0, 0
	for _, in := range inputs {
		dir := t.TempDir()
		var paths []string
		for _, f := range in.files {
			path := filepath.Join(dir, f[0])
			if err := os.WriteFile(path, []byte(f[1]), 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, path)
		}
		for i, path := range paths {
			var siblings []string
			for j, other := range paths {
				if j != i {
					siblings = append(siblings, other)
				}
			}
			name := in.name + ": " + in.files[i][0]
			n, ok := analyzeForCollection(t, nativeA, path, in.files[i][1], siblings)
			if !ok {
				if in.mustFail {
					t.Errorf("%s: does not parse", name)
				}
				continue
			}
			a, _ := analyzeForCollection(t, antlrA, path, in.files[i][1], siblings)
			compared++
			if !checkTrace(t, name+" (native run)", n, true) || !checkTrace(t, name+" (ANTLR run)", a, false) {
				continue
			}
			if _, isErr := n.result.(error); isErr {
				failed++
			}
			if in.mustFail && i == 0 {
				if _, isErr := n.result.(error); !isErr {
					t.Errorf("%s: analysis succeeded; the input must fail in the collection", name)
				}
				if !n.trace.native {
					t.Errorf("%s: not collected on the native tree", name)
				}
			}
			if diff := firstDiff(reflect.ValueOf(n.result), reflect.ValueOf(a.result), "", map[visit]bool{}); diff != "" {
				t.Errorf("%s: the native collection differs from ANTLR's at %s", name, diff)
			}
		}
	}
	if compared < 100 || failed < len(collectionErrorInputs) {
		t.Fatalf("compared %d files, %d of them failing; the inputs are not all read", compared, failed)
	}
	t.Logf("compared %d files, %d of them failing", compared, failed)
}

// TestCollectionTakesCleanParses: a parse with syntax errors, or none, gives
// no native tree to collect on, so its error-recovered tree is collected on
// ANTLR's tree; a clean parse gives one.
func TestCollectionTakesCleanParses(t *testing.T) {
	parseWithGalop(t)
	p := galaparser.NewAntlrGalaParser()
	broken := p.ParseFile("package main\n\nfunc f( = 1\n")
	if len(broken.Errors) == 0 {
		t.Fatal("the input must have syntax errors")
	}
	if broken.CleanNative() != nil {
		t.Fatal("a parse with syntax errors has a handle to collect on")
	}
	if nativeFileOf(nil).ok() {
		t.Fatal("a nil handle has a native tree")
	}
	clean := p.ParseFile("package main\n\nfunc f() int = 1\n").CleanNative()
	if clean == nil {
		t.Fatal("a clean parse has no handle to collect on")
	}
	if !nativeFileOf(clean).ok() {
		t.Fatal("a clean parse has no native tree")
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

// typeCollector collects the types of a parsed file into fd, on one twin's
// tree.
type typeCollector func(*galaAnalyzer, *galaparser.Parsed, *fileDecls) error

// typeCollectors are the two twins of the type collection, by tree.
var typeCollectors = map[string]typeCollector{
	"antlr": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		return a.collectFileTypes(p.Tree.(*grammar.SourceFileContext), fd)
	},
	"native": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		nf := nativeFileOf(p.CleanNative())
		if !nf.ok() {
			return fmt.Errorf("a clean parse has no native tree")
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
// initializers take each shape inferPackageValInitType reads, and the shapes
// it turns down, so TestValInferenceAgrees compares both twins on every
// branch, whatever the corpus holds.
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

// initializersInferred is how many initializers of sf's package-level
// bindings extractPackageVals infers: one per name a declaration with no type
// binds, other than `_`, when it gives each name an initializer of its own.
func initializersInferred(sf *grammar.SourceFileContext) int {
	n := 0
	for _, td := range sf.AllTopLevelDeclaration() {
		var (
			idList   grammar.IIdentifierListContext
			typ      grammar.ITypeContext
			exprList grammar.IExpressionListContext
			tp       grammar.ITuplePatternContext
		)
		switch {
		case td.ValDeclaration() != nil:
			vc := td.ValDeclaration().(*grammar.ValDeclarationContext)
			idList, typ, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
		case td.VarDeclaration() != nil:
			vc := td.VarDeclaration().(*grammar.VarDeclarationContext)
			idList, typ, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
		default:
			continue
		}
		if tp != nil || typ != nil || idList == nil || exprList == nil {
			continue
		}
		names := idList.(*grammar.IdentifierListContext).AllIdentifier()
		if len(names) != len(exprList.(*grammar.ExpressionListContext).AllExpression()) {
			continue
		}
		for _, id := range names {
			if id.GetText() != "_" {
				n++
			}
		}
	}
	return n
}

// valShapesSrc holds initializers that both twins of the package-level
// initializer inference once typed alike and wrongly, which
// TestValInferenceAgrees cannot catch.
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
	{"Matched", `3match{case3=>"three"case_=>"other"}`, "", "string"},	// A signed literal takes its literal's type, whatever its base.
	{"Mask", "-0x10", "int", "int"},
	{"HexE", "-0xE", "int", "int"},
	{"Bin", "-0b101", "int", "int"},
	{"Oct", "+0o17", "int", "int"},
	{"Exp", "-1e3", "float64", "float64"},
}

// TestValInitTypeShapes: the initializer inference of valShapesSrc's
// bindings on the native tree gives each the type valShapes holds (ANTLR's
// twin is held to it by TestValInferenceAgrees, whose corpus has these
// shapes), and on either tree the binding records its own type — never a
// wrong one in place of one the inference cannot tell.
func TestValInitTypeShapes(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	shapeType := func(typ transpiler.Type) string {
		if typ == nil || typ.IsNil() {
			return ""
		}
		return typ.String()
	}
	for _, tree := range []string{"native", "antlr"} {
		t.Run(tree, func(t *testing.T) {
			var p transpiler.GalaParser = galaparser.NewAntlrGalaParser()
			if tree == "antlr" {
				p = antlrOnly{p}
			}
			a := newCollectionAnalyzer(t, p, searchPaths)
			path := filepath.Join(t.TempDir(), "shapes.gala")
			if err := os.WriteFile(path, []byte(valShapesSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			inferred := map[string]string{}
			a.onInferInit = func(e native.ExpressionNode, pkgName string, richAST *transpiler.RichAST, got transpiler.Type) {
				inferred[e.GetText()] = shapeType(got)
			}
			an, ok := analyzeForCollection(t, a, path, valShapesSrc, []string{})
			a.onInferInit = nil
			if !ok {
				t.Fatal("valShapesSrc does not parse")
			}
			if an.trace.native != (tree == "native") {
				t.Fatalf("collected on the native tree: %v, want %v", an.trace.native, tree == "native")
			}
			rich, ok := an.result.(*transpiler.RichAST)
			if !ok {
				t.Fatalf("analyze: %v", an.result)
			}
			for _, s := range valShapes {
				if tree == "native" {
					if got, ok := inferred[s.init]; !ok {
						t.Errorf("%s: %q was not inferred on the native tree", s.name, s.init)
					} else if got != s.inferred {
						t.Errorf("%s: %q is inferred as %q on the native tree, want %q", s.name, s.init, got, s.inferred)
					}
				}
				pv := rich.PackageVals[s.name]
				if pv == nil {
					t.Fatalf("%s is not recorded", s.name)
				}
				if got := shapeType(pv.Type); got != s.recorded {
					t.Errorf("%s records %q, want %q", s.name, got, s.recorded)
				}
			}
		})
	}
}

// TestValInferenceAgrees holds the inference of package-level initializers on
// the native tree (inferPackageValInitTypeNative) to the inference on
// ANTLR's (inferPackageValInitType). As each initializer of a file collected
// on its native tree is inferred, the test infers the same initializer's
// ANTLR context in the same state, and the two types must be equal. A file
// collected on its native tree must report every initializer its
// declarations have inferred (initializersInferred), and a file collected on
// ANTLR's none, so no file takes the ANTLR inference unnoticed. The corpus is
// TestCollectionsAgree's plus valInferenceInputs. The inference reports no
// diagnostics; TestCollectionDiagnosticsAgree covers the collection's.
func TestValInferenceAgrees(t *testing.T) {
	parseWithGalop(t)
	paths, searchPaths := collectionCorpus(t)
	a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), searchPaths)
	type source struct {
		path, text string
		// handwritten says the source is one of valInferenceInputs, which
		// must analyze on the native tree.
		handwritten bool
	}
	var sources []source
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source{path, string(text), false})
	}
	for i, text := range valInferenceInputs {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("input%d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "main.gala")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source{path, text, true})
	}

	files, inferred, typed, otherInferred := 0, 0, 0, 0
	for _, src := range sources {
		parsed := a.parser.ParseFile(src.text)
		if parsed.Err() != nil {
			if src.handwritten {
				t.Errorf("%s: does not parse: %v", src.path, parsed.Err())
			}
			continue
		}
		reported, reportedTyped := 0, 0
		subjectTree, _ := parsed.Native.Tree()
		a.onInferInit = func(e native.ExpressionNode, pkgName string, richAST *transpiler.RichAST, got transpiler.Type) {
			// The initializers of the subject are counted; those of its
			// siblings and of the files of its imports, inferred on their
			// own native trees, are compared too.
			handle := parsed.Native
			if e.Tree() == subjectTree {
				reported++
				if !got.IsNil() {
					reportedTyped++
				}
			} else {
				handle = cachedHandleOf(a, e.GalaNodeRef)
				otherInferred++
			}
			pos := nativePos(e.GalaNodeRef)
			ctx, ok := handle.Ctx(e.GalaNodeRef).(*grammar.ExpressionContext)
			if !ok {
				t.Errorf("%s:%d:%d: the initializer has no expression context", src.path, pos.Line, pos.Column)
				return
			}
			if want := a.inferPackageValInitType(ctx, pkgName, richAST); !reflect.DeepEqual(got, want) {
				t.Errorf("%s:%d:%d: %q is inferred as %#v on the native tree, %#v on ANTLR's", src.path, pos.Line, pos.Column, ctx.GetText(), got, want)
			}
		}
		an := analyzeParsedForCollection(t, a, src.path, parsed, nil)
		a.onInferInit = nil
		if !checkTrace(t, src.path, an, true) {
			continue
		}
		_, failed := an.result.(error)
		if src.handwritten && (failed || !an.trace.native) {
			t.Errorf("%s: must analyze on the native tree: native %v, result %v", src.path, an.trace.native, an.result)
		}
		want := 0
		if an.trace.native {
			want = initializersInferred(parsed.Tree.(*grammar.SourceFileContext))
		}
		// An analysis that fails may stop before the last declaration.
		if reported == want || failed && reported < want {
			files++
			inferred, typed = inferred+reported, typed+reportedTyped
			continue
		}
		t.Errorf("%s: %d initializers inferred on the native tree, want %d (collected natively: %v)", src.path, reported, want, an.trace.native)
	}
	if files < 200 || inferred < 80 || typed < inferred/2 || otherInferred < 80 {
		t.Fatalf("%d files, %d initializers inferred on the native tree, %d of them typed, %d of other files", files, inferred, typed, otherInferred)
	}
	t.Logf("%d files, %d initializers inferred on the native tree, %d of them typed, %d of other files", files, inferred, typed, otherInferred)
}

// cachedHandleOf is the native-tree handle a's parsed-file cache holds for
// the tree of n, or nil.
func cachedHandleOf(a *galaAnalyzer, n native.GalaNodeRef) *galaparser.Native {
	a.parsedFileCacheMu.Lock()
	defer a.parsedFileCacheMu.Unlock()
	for _, e := range a.parsedFileCache {
		if tree, err := e.parsed.CleanNative().Tree(); err == nil && tree == n.Tree() {
			return e.parsed.Native
		}
	}
	return nil
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

// BenchmarkCollect collects the declarations of every staged source, on
// ANTLR's tree ("antlr") and on the native tree converted beforehand
// ("native"), as the file being analyzed ("file": analyze's sections 1 to
// 2b). "convert" is the conversion to the native tree itself, which a file
// collected on its native tree pays once.
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
			sf := parsed.Tree.(*grammar.SourceFileContext)
			out = append(out, source{path, sf.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText(), parsed})
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
	for _, mode := range []string{"antlr", "native"} {
		b.Run(mode+"/file", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, s := range sources {
					fd := newCollectedFile(a, s.pkgName, s.path, s.parsed.Docs)
					sf := s.parsed.Tree.(*grammar.SourceFileContext)
					if mode == "native" {
						nf := nativeFileOf(s.parsed.Native)
						if a.collectFileTypesNative(nf, fd) == nil && a.collectFileFuncsNative(nf, fd) == nil {
							a.fileTypeAliasesNative(nf, fd.pkgName)
							collectFileEmbedsNative(nf, fd.richAST)
							a.extractPackageValsNative(nf, fd.pkgName, fd.richAST, fd.docs, fd.absFilePath)
						}
						continue
					}
					if a.collectFileTypes(sf, fd) == nil && a.collectFileFuncs(sf, fd) == nil {
						a.fileTypeAliases(sf, fd.pkgName)
						collectFileEmbeds(sf, fd.richAST)
						a.extractPackageVals(sf, fd.pkgName, fd.richAST, fd.docs, fd.absFilePath)
					}
				}
			}
		})
	}
}
