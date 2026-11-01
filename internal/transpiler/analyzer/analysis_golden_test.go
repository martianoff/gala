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
)

// The analysis golden holds the analyzer to what it gave when it had a
// collection on ANTLR's tree next to the one on the native tree, and the two
// agreed on every input below: the metadata of each analysis, or its error
// (code, message, hint, position and span), as a digest of a canonical dump
// (dumper).
//
// Each line is `key<TAB>input<TAB>result`: the input's name, a digest of
// what it analyzes (the subject's text and the names and texts of the other
// .gala files of its package), and the digest of the analysis. An input whose
// text changed since the golden was written is not compared, as is one the
// golden does not list: its line is stale, and refreshing the golden brings it
// back. To refresh it, run the test with GALA_UPDATE_ANALYSIS_GOLDEN set to
// the golden's path in the source tree:
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

// goldenInputs is every analysis the golden records: each corpus source in
// place, and each file of each handwritten package and error reference
// example, analyzed with the other files of its package; and the search path
// the corpus resolves its imports on.
func goldenInputs(t *testing.T) ([]goldenInput, []string) {
	paths, searchPaths := collectionCorpus(t)
	var inputs []goldenInput
	for _, path := range paths {
		rel, err := filepath.Rel(searchPaths[0], path)
		if err != nil {
			t.Fatal(err)
		}
		// The analyzer reads a library's siblings from its directory.
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.gala"))
		inputs = append(inputs, goldenInput{key: "corpus/" + filepath.ToSlash(rel), path: path, files: siblings})
	}
	addPackage := func(key string, files [][2]string) {
		dir := filepath.Join(t.TempDir(), "pkg")
		if err := os.Mkdir(dir, 0o755); err != nil {
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
	return inputs, searchPaths
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

// TestAnalysisGolden analyzes every input of goldenInputs, as the CLI
// analyzes a file, and holds each analysis to the golden.
func TestAnalysisGolden(t *testing.T) {
	parseWithGalop(t)
	inputs, searchPaths := goldenInputs(t)
	projectRoot := t.TempDir()
	a := NewBatchAnalyzer(galaparser.NewAntlrGalaParser(), searchPaths, projectRoot).inner
	a.cache = nil
	d := &dumper{
		// Every directory of the test's is under the directory of its
		// project root's.
		norm:   newPathNormalizer(searchPaths[0], filepath.Dir(projectRoot)),
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
			continue // the transpiler stops at the syntax error
		}
		analyzed++
		t1 := time.Now()
		result := d.digest(reflect.ValueOf(an.result))
		analyzing, dumping = analyzing+t1.Sub(t0), dumping+time.Since(t1)
		g.Check(in.key, inputDigest(t, in), result, func() string {
			if err, ok := an.result.(error); ok {
				return "error: " + err.Error()
			}
			return "metadata"
		})
	}
	g.Finish("The analyzer's results; see analysisGolden in analysis_golden_test.go.")
	if g.Updating() {
		return
	}
	t.Logf("analyzed %d inputs, %d of them held to the golden; %s analyzing, %s dumping", analyzed, g.Compared, analyzing, dumping)
	if g.Compared < analyzed*3/4 || g.Compared < 400 {
		t.Fatalf("only %d of %d analyses are in the golden: refresh it (see analysisGolden)", g.Compared, analyzed)
	}
}

// pathNormalizer rewrites the machine-specific prefixes of the paths an
// analysis records: the corpus root and the test's temporary directories, in
// either slash form and either case of a drive letter.
type pathNormalizer struct{ r *strings.Replacer }

func newPathNormalizer(root, tmp string) pathNormalizer {
	var pairs []string
	add := func(p, as string) {
		for _, form := range []string{p, filepath.ToSlash(p), filepath.FromSlash(p)} {
			pairs = append(pairs, form, as)
			if len(form) > 1 && form[1] == ':' {
				other := strings.ToLower(form[:1])
				if other == form[:1] {
					other = strings.ToUpper(form[:1])
				}
				pairs = append(pairs, other+form[1:], as)
			}
		}
	}
	add(tmp, "$TMP")
	add(root, "$ROOT")
	return pathNormalizer{strings.NewReplacer(pairs...)}
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
	norm   pathNormalizer
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
			entries = append(entries, kv{d.norm.r.Replace(fmt.Sprint(it.Key().Interface())), it.Value()})
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
		w.WriteString(strconv.Quote(d.norm.r.Replace(digestgolden.NormalizeNewlines(v.String()))))
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
