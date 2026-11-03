package analyzer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"martianoff/gala/internal/digestgolden"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/testcorpus"
	"martianoff/gala/internal/transpiler"
)

// The analysis golden holds the analyzer to what it gave when it had a
// collection on ANTLR's tree next to the one on the native tree, and the two
// agreed on every input below (TestCollectionsAgree and its siblings, run on
// the same inputs when the golden was written). Each input is analyzed as the
// CLI analyzes a file, and the golden records what the analysis holds of that
// file alone (subjectAnalysis): the types, methods, functions and package
// vals it declares and its package-level facts, or the error, as a digest of a
// canonical dump (dumper) in which every path is relative to the corpus root
// or to the test's own directories.
//
// The inputs are frozen: each source of the corpus (internal/testcorpus), and
// each file of each error reference example (from the corpus's copy of the
// pages) and of each handwritten package below, analyzed with the other files
// of its package. An edit of std or of an example changes no line.
//
// Each line is `key<TAB>input<TAB>result`: the input's name, a digest of the
// texts of its package's files and the digest of the analysis. An input the
// golden does not list, one whose texts changed, and a line no input has fail
// the test. To regenerate the golden, run the test with
// GALA_UPDATE_ANALYSIS_GOLDEN set to the golden's path in the source tree:
//
//	bazel test //internal/transpiler/analyzer:collect_native_test \
//	  --test_filter=TestAnalysisGolden \
//	  --test_env=GALA_UPDATE_ANALYSIS_GOLDEN=$PWD/internal/transpiler/analyzer/testdata/analysis.golden
//
// A change of the golden is a change of what the analyzer records: review it
// as one.
const analysisGolden = "internal/transpiler/analyzer/testdata/analysis.golden"

// goldenInput is one analysis the golden records: the subject at path, of a
// package whose other files are packageFiles (nil to let the analyzer find
// library siblings in the subject's directory), and the files whose texts the
// input digest covers.
type goldenInput struct {
	key          string
	path         string
	packageFiles []string
	files        []string
}

// goldenInputs is every analysis the golden records, the search path the
// corpus resolves its imports on, and the directory the handwritten packages
// are written under.
func goldenInputs(t *testing.T) ([]goldenInput, []string, string) {
	paths, searchPaths := collectionCorpus(t)
	root := searchPaths[0]
	var inputs []goldenInput
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		// The analyzer reads a library's siblings from its directory.
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.gala"))
		inputs = append(inputs, goldenInput{key: "corpus/" + filepath.ToSlash(rel), path: path, files: siblings})
	}
	written := t.TempDir()
	addPackage := func(key string, files [][2]string) {
		dir := filepath.Join(written, fmt.Sprintf("p%03d", len(inputs)), "pkg")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var pkgPaths []string
		for _, f := range files {
			path := filepath.Join(dir, f[0])
			if err := os.WriteFile(path, []byte(f[1]), 0o644); err != nil {
				t.Fatal(err)
			}
			pkgPaths = append(pkgPaths, path)
		}
		for i, path := range pkgPaths {
			var packageFiles []string
			if len(pkgPaths) > 1 {
				packageFiles = append(append(packageFiles, pkgPaths[:i]...), pkgPaths[i+1:]...)
			}
			inputs = append(inputs, goldenInput{key: key + ": " + files[i][0], path: path, packageFiles: packageFiles, files: pkgPaths})
		}
	}
	for i, files := range docExamples(t) {
		addPackage(fmt.Sprintf("error reference example %d", i), files)
	}
	for _, in := range collectionErrorInputs {
		addPackage("collection: "+in.name, in.files)
	}
	for _, in := range fileCheckInputs {
		addPackage("file checks: "+in.name, in.files)
	}
	for _, in := range undefinedInputs {
		addPackage("undefined: "+in.name, in.files)
	}
	for i, text := range valInferenceInputs {
		addPackage(fmt.Sprintf("val inference %d", i), [][2]string{{"main.gala", text}})
	}
	return inputs, searchPaths, written
}

// inputDigest is the digest of in's files, by base name and text.
func inputDigest(t *testing.T, in goldenInput) string {
	files := append([]string(nil), in.files...)
	if len(files) == 0 {
		files = []string{in.path}
	}
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s\x00%s\x00", filepath.Base(f), digestgolden.NormalizeNewlines(string(text)))
	}
	return digestgolden.Digest(b.String())
}

// subjectAnalysis is what the golden records of an analysis: what it holds
// of its subject file alone. The metadata of the packages it imports and of
// its siblings are other inputs' (each is a source of the corpus too).
type subjectAnalysis struct {
	Error            error
	PackageName      string
	ImportAliases    map[string]string
	EmbedDirectives  []transpiler.EmbedDirective
	AnalysisWarnings []string
	// Types is the types the subject declares, with only the methods it
	// declares; Methods is, by `Type.name`, the methods it declares on a
	// type another file declares.
	Types     map[string]*transpiler.TypeMetadata
	Methods   map[string]*transpiler.MethodMetadata
	Functions map[string]*transpiler.FunctionMetadata
	Vals      map[string]*transpiler.PackageValMetadata
}

// subjectOf is what res, the analysis of the file at path, holds of it.
func subjectOf(res *transpiler.RichAST, path string) subjectAnalysis {
	same := func(definedIn string) bool {
		return definedIn != "" && strings.EqualFold(filepath.ToSlash(filepath.Clean(definedIn)), filepath.ToSlash(filepath.Clean(path)))
	}
	s := subjectAnalysis{
		PackageName:      res.PackageName,
		ImportAliases:    res.ImportAliases,
		EmbedDirectives:  res.EmbedDirectives,
		AnalysisWarnings: res.AnalysisWarnings,
		Types:            map[string]*transpiler.TypeMetadata{},
		Methods:          map[string]*transpiler.MethodMetadata{},
		Functions:        map[string]*transpiler.FunctionMetadata{},
		Vals:             map[string]*transpiler.PackageValMetadata{},
	}
	for key, tm := range res.Types {
		own := map[string]*transpiler.MethodMetadata{}
		for name, m := range tm.Methods {
			if m != nil && same(m.DefinedIn) {
				own[name] = m
			}
		}
		if same(tm.DefinedIn) {
			c := *tm
			c.Methods = own
			s.Types[key] = &c
			continue
		}
		for name, m := range own {
			s.Methods[key+"."+name] = m
		}
	}
	for key, fn := range res.Functions {
		if same(fn.DefinedIn) {
			s.Functions[key] = fn
		}
	}
	for key, v := range res.PackageVals {
		if same(v.DefinedIn) {
			s.Vals[key] = v
		}
	}
	return s
}

// TestAnalysisGolden analyzes every input of goldenInputs, as the CLI
// analyzes a file, and holds what each analysis holds of its subject to the
// golden.
func TestAnalysisGolden(t *testing.T) {
	parseWithGalop(t)
	inputs, searchPaths, written := goldenInputs(t)
	projectRoot := t.TempDir()
	a := NewBatchAnalyzer(galaparser.NewAntlrGalaParser(), searchPaths, projectRoot).inner
	a.cache = nil
	d := &dumper{
		norm:   testcorpus.NewNormalizer(map[string]string{searchPaths[0]: "$ROOT", written: "$WRITTEN", projectRoot: "$PROJECT"}),
		memo:   map[uintptr]string{},
		active: map[uintptr]bool{},
	}

	g := digestgolden.Open(t, analysisGolden, "GALA_UPDATE_ANALYSIS_GOLDEN")
	analyzed := 0
	var analyzing, dumping time.Duration
	for _, in := range inputs {
		text, err := os.ReadFile(in.path)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		an, ok := analyzeForCollection(t, a, in.path, string(text), in.packageFiles)
		if !ok {
			// The transpiler stops at the syntax error, before analysis.
			g.Check(in.key, inputDigest(t, in), digestgolden.Digest("syntax error"), nil)
			continue
		}
		analyzed++
		t1 := time.Now()
		var s subjectAnalysis
		if res, ok := an.result.(*transpiler.RichAST); ok {
			s = subjectOf(res, in.path)
		} else {
			s.Error = an.result.(error)
		}
		result := d.digest(reflect.ValueOf(s))
		analyzing, dumping = analyzing+t1.Sub(t0), dumping+time.Since(t1)
		g.Check(in.key, inputDigest(t, in), result, func() string {
			if s.Error != nil {
				return "error: " + d.norm.Replace(s.Error.Error())
			}
			return fmt.Sprintf("metadata: %d types, %d methods, %d functions, %d vals", len(s.Types), len(s.Methods), len(s.Functions), len(s.Vals))
		})
	}
	g.Finish("The analyzer's results; see analysisGolden in analysis_golden_test.go.")
	if !g.Updating() {
		t.Logf("%d inputs, %d analyzed, %d held to the golden; %s analyzing, %s dumping", len(inputs), analyzed, g.Compared, analyzing, dumping)
	}
	if analyzed < 800 {
		t.Fatalf("only %d of %d inputs were analyzed", analyzed, len(inputs))
	}
}

// skippedField reports whether the dump leaves out a field: what depends on
// the host's Go SDK (GoTypeInfo) and the parse trees, which no two analyses
// share.
func skippedField(f reflect.StructField) bool {
	switch f.Name {
	case "GoTypeInfo", "Tree", "Native":
		return true
	}
	return f.Type.Kind() == reflect.Func || f.Type.Kind() == reflect.Chan
}

// dumper writes values canonically: maps in key order, strings with their
// machine-specific path prefixes rewritten (norm), and each pointer as the
// digest of what it points to. The analyses of a batch share the metadata of
// the packages they import, so a pointer's digest is computed once (memo); a
// pointer met again while its own digest is being computed (a cycle) is
// written as a back reference. keep holds every pointer memo has a digest
// of, so that no address is reused for another value while memo holds it.
type dumper struct {
	norm   *testcorpus.Normalizer
	memo   map[uintptr]string
	active map[uintptr]bool
	keep   []reflect.Value
}

// digest is the digest of v's dump.
func (d *dumper) digest(v reflect.Value) string {
	h := sha256.New()
	w := bufio.NewWriter(h)
	d.dump(w, v)
	w.Flush()
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (d *dumper) dump(w *bufio.Writer, v reflect.Value) {
	if !v.IsValid() {
		w.WriteString("~")
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			w.WriteString("nil")
			return
		}
		p := v.Pointer()
		if d.active[p] {
			w.WriteString("^")
			return
		}
		sum, ok := d.memo[p]
		if !ok {
			d.active[p] = true
			sum = d.digest(v.Elem())
			delete(d.active, p)
			d.memo[p] = sum
			d.keep = append(d.keep, v)
		}
		w.WriteString("&" + sum)
	case reflect.Interface:
		if v.IsNil() {
			w.WriteString("nil")
			return
		}
		w.WriteString(v.Elem().Type().String())
		d.dump(w, v.Elem())
	case reflect.Struct:
		w.WriteString("{")
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if skippedField(f) {
				continue
			}
			w.WriteString(f.Name + ":")
			d.dump(w, v.Field(i))
			w.WriteString(",")
		}
		w.WriteString("}")
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			w.WriteString("nil")
			return
		}
		w.WriteString("[" + strconv.Itoa(v.Len()) + ":")
		for i := range v.Len() {
			d.dump(w, v.Index(i))
			w.WriteString(",")
		}
		w.WriteString("]")
	case reflect.Map:
		if v.IsNil() {
			w.WriteString("nil")
			return
		}
		type kv struct {
			k string
			v reflect.Value
		}
		entries := make([]kv, 0, v.Len())
		for it := v.MapRange(); it.Next(); {
			entries = append(entries, kv{d.norm.Replace(fmt.Sprint(it.Key().Interface())), it.Value()})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].k < entries[j].k })
		w.WriteString("map[" + strconv.Itoa(len(entries)) + ":")
		for _, e := range entries {
			w.WriteString(strconv.Quote(e.k) + "=")
			d.dump(w, e.v)
			w.WriteString(",")
		}
		w.WriteString("]")
	case reflect.String:
		// A checkout may end the source's lines with CRLF, and a span of
		// source text (a default value's) keeps them.
		w.WriteString(strconv.Quote(d.norm.Replace(digestgolden.NormalizeNewlines(v.String()))))
	case reflect.Bool:
		w.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		w.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		w.WriteString(v.Kind().String())
	}
}
