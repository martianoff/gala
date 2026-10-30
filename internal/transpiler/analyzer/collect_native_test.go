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
	"martianoff/gala/internal/transpiler"
)

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
		if strings.HasSuffix(f.ShortPath, ".gala") {
			paths = append(paths, f.Path)
		}
	}
	sort.Strings(paths)
	return paths, []string{filepath.Dir(filepath.Dir(stdFile))}
}

// newCollectionAnalyzer is a batch analyzer over searchPaths parsing with p,
// with no disk cache, so every package it imports it analyzes itself.
func newCollectionAnalyzer(p transpiler.GalaParser, searchPaths []string) *galaAnalyzer {
	a := NewBatchAnalyzer(p, searchPaths, searchPaths[0]).inner
	a.cache = nil
	return a
}

// analyzeAll analyzes each of paths in turn with a, parsing it with a.parser, and
// returns what each analysis gives, by path: the metadata, or the error.
func analyzeAll(tb testing.TB, a *galaAnalyzer, paths []string) map[string]any {
	out := make(map[string]any, len(paths))
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		parsed := a.parser.ParseFile(string(text))
		if parsed.Err() != nil {
			continue // the transpiler stops at a syntax error, before analysis
		}
		a.packageFiles = nil
		a.checkedDirs = make(map[string]bool)
		res, err := a.AnalyzeParsed(parsed, path)
		if err != nil {
			out[path] = err.Error()
			continue
		}
		res.Tree = nil // each analysis has its own parse
		dropAmbiguousCompanions(res)
		out[path] = res
	}
	return out
}

// dropAmbiguousCompanions deletes from res.CompanionObjects each entry under
// a bare name that companions of two or more packages share (json's and
// yaml's AsIs): discoverCompanionObjects files each under its bare name in
// map order, so which one the bare name keeps varies from run to run,
// whichever tree the declarations were collected on. Each stays under its
// qualified name.
func dropAmbiguousCompanions(res *transpiler.RichAST) {
	pkgs := make(map[string]map[string]bool)
	for _, c := range res.CompanionObjects {
		if pkgs[c.Name] == nil {
			pkgs[c.Name] = make(map[string]bool)
		}
		pkgs[c.Name][c.Package] = true
	}
	for name, in := range pkgs {
		if len(in) > 1 {
			delete(res.CompanionObjects, name)
		}
	}
}

// TestCollectionsAgree holds the collection of declarations on the native
// tree (collect_native.go) to the collection on ANTLR's: every GALA source
// staged as data, analyzed as the CLI analyzes a file, with its library
// siblings and imports, gives the same metadata — or the same error — when
// every file is collected on its native tree (the siblings and imported
// files too, through nativeSiblings) as when every file is collected on
// ANTLR's.
func TestCollectionsAgree(t *testing.T) {
	paths, searchPaths := collectionCorpus(t)
	p := galaparser.NewAntlrGalaParser()
	native := newCollectionAnalyzer(p, searchPaths)
	native.nativeSiblings = true
	nativeRes := analyzeAll(t, native, paths)
	antlrRes := analyzeAll(t, newCollectionAnalyzer(antlrOnly{p}, searchPaths), paths)

	analyzed := 0
	for _, path := range paths {
		n, a := nativeRes[path], antlrRes[path]
		if n == nil && a == nil {
			continue
		}
		analyzed++
		if diff := firstDiff(reflect.ValueOf(n), reflect.ValueOf(a), "", map[visit]bool{}); diff != "" {
			t.Errorf("%s: the native collection differs from ANTLR's at %s", path, diff)
		}
	}
	// The repository's sources analyze; a floor catches them not being staged.
	if analyzed < 200 {
		t.Fatalf("analyzed %d sources; the repository's are not staged", analyzed)
	}
}

// TestCollectionTakesCleanParses: a parse with syntax errors, or none, gives
// no native tree to collect on, so its error-recovered tree is collected on
// ANTLR's tree; a clean parse gives one.
func TestCollectionTakesCleanParses(t *testing.T) {
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
// ("native"): as the file being analyzed ("file": analyze's sections 1 to 2b)
// and as a sibling ("sibling": extractSiblingFullMetadata). "convert" is the
// conversion to the native tree itself, which a file collected on its native
// tree pays once.
func BenchmarkCollect(b *testing.B) {
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
	a := newCollectionAnalyzer(galaparser.NewAntlrGalaParser(), searchPaths)
	newFile := func(s source) *fileDecls {
		richAST := &transpiler.RichAST{
			Types:            make(map[string]*transpiler.TypeMetadata),
			Functions:        make(map[string]*transpiler.FunctionMetadata),
			Packages:         make(map[string]string),
			CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
		}
		a.currentRichAST = richAST
		a.currentOwnTypes = make(map[string]bool)
		a.currentOwnMethods = make(map[string]map[string]bool)
		return &fileDecls{richAST: richAST, pkgName: s.pkgName, filePath: s.path, absFilePath: s.path, docs: s.parsed.Docs, decls: newPackageDecls(s.path)}
	}
	for _, mode := range []string{"antlr", "native"} {
		b.Run(mode+"/file", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, s := range sources {
					fd := newFile(s)
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
		b.Run(mode+"/sibling", func(b *testing.B) {
			for _, s := range sources {
				kept := &galaparser.Parsed{Tree: s.parsed.Tree, Docs: s.parsed.Docs}
				if mode == "native" {
					kept.Native = s.parsed.Native
				}
				a.parsedFileCache[a.parsedFileCacheKey(s.path)] = &parsedFileEntry{parsed: kept}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, s := range sources {
					fd := newFile(s)
					_ = a.extractSiblingFullMetadata(s.parsed.Tree.(*grammar.SourceFileContext), fileQualifiers{}, fd.pkgName, fd.richAST, s.path, fd.decls)
				}
			}
		})
	}
}
