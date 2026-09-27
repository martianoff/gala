package transformer_test

import (
	"fmt"
	"go/importer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/gooracle"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// The generated-Go oracle.
//
// Every test in this package that transpiles GALA does it through
// newCheckedTranspiler, whose Transpile records each successful output. After
// the package's tests have run, TestMain puts every recorded output through
// gooracle: it must parse, must not contain a leaked internal token
// (gooracle.LeakRules), and — when all its imports resolve — must type-check.
//
// The check is on what the transpiler produced, not on what the test
// expected, so a test that pins an expected string containing a leak fails
// here even though its own assertion passes. It runs after the tests rather
// than inside Transpile because Transpile has no *testing.T: a finding is
// attributed to the calling test by its stack instead.
//
// The example corpus goes through the same oracle in
// TestGeneratedGoOracleCorpus, parse and leak check only: every example is
// already compiled by Bazel.

// checkedTranspiler is the transpiler every test in this package uses.
type checkedTranspiler struct {
	*transpiler.GalaToGoTranspiler
}

var _ transpiler.Transpiler = (*checkedTranspiler)(nil)

// newCheckedTranspiler is transpiler.NewGalaToGoTranspiler with the oracle
// attached. TestOracleCoversEveryTranspiler keeps tests from calling the
// unwrapped constructor.
func newCheckedTranspiler(p transpiler.GalaParser, a transpiler.Analyzer, tr transpiler.ASTTransformer, g transpiler.CodeGenerator) *checkedTranspiler {
	return &checkedTranspiler{transpiler.NewGalaToGoTranspiler(p, a, tr, g)}
}

// Transpile transpiles and records the output for the oracle.
func (c *checkedTranspiler) Transpile(input, filePath string) (string, error) {
	out, err := c.GalaToGoTranspiler.Transpile(input, filePath)
	if err == nil {
		oracle.record(input, filePath, out)
	}
	return out, err
}

type oracleOutput struct {
	test     string // e.g. TestAssignment
	site     string // test-file position of the Transpile call
	input    string
	filePath string
	output   string
}

type oracleRecorder struct {
	mu      sync.Mutex
	outputs []oracleOutput
}

var oracle oracleRecorder

var testFuncRE = regexp.MustCompile(`transformer_test\.(Test[A-Za-z0-9_]*)`)

func (r *oracleRecorder) record(input, filePath, output string) {
	test, site := "<unknown test>", "<unknown site>"
	pcs := make([]uintptr, 64)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	for {
		f, more := frames.Next()
		if site == "<unknown site>" && strings.HasSuffix(f.File, "_test.go") && filepath.Base(f.File) != "oracle_test.go" {
			site = fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)
		}
		if m := testFuncRE.FindStringSubmatch(f.Function); m != nil {
			test = m[1]
			break
		}
		if !more {
			break
		}
	}
	r.mu.Lock()
	r.outputs = append(r.outputs, oracleOutput{test, site, input, filePath, output})
	r.mu.Unlock()
}

// oracleAllowlist names tests whose output the oracle knowingly lets through,
// keyed by test function and gooracle rule ("parse", a LeakRules name, or
// "typecheck"). Each entry needs a reason. An entry that no longer matches
// anything fails the run, so the list only shrinks.
var oracleAllowlist = map[string]string{
	// A `val` holding a Go struct whose method has a pointer receiver:
	// `val u = url.URL{...}; u.String()` lowers to `u.Get().String()`, and
	// Immutable.Get() returns a non-addressable copy, so Go rejects it
	// ("cannot call pointer method String on url.URL"). Fixing it needs the
	// Go method set at the call site and an addressable temporary for the
	// receiver — an open transpiler bug, not a fixture problem.
	"TestGoImportedTypeStaysPartial/typecheck": "pointer-receiver Go method on a val-held struct",
}

func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if !runOracle() {
			code = 1
		}
	}
	os.Exit(code)
}

// runOracle checks every recorded output and prints a report. It returns false
// if anything failed.
func runOracle() bool {
	outputs := oracle.outputs
	if len(outputs) == 0 {
		// A -run filter that selected no transpiling test.
		return true
	}

	imp := newOracleImporter()

	type failure struct {
		o        oracleOutput
		rule     string
		findings []string
	}
	var failures []failure
	usedAllow := map[string]bool{}
	tests := map[string]bool{}
	skipReasons := map[string]int{}
	var typeChecked, softTotal int
	seen := map[string]bool{}

	fail := func(o oracleOutput, rule string, findings []string) {
		key := o.test + "/" + rule
		if _, ok := oracleAllowlist[key]; ok {
			usedAllow[key] = true
			return
		}
		failures = append(failures, failure{o, rule, findings})
	}

	for _, o := range outputs {
		tests[o.test] = true
		// Identical outputs recur across table cases and shared fixtures;
		// check each distinct output once.
		if seen[o.output] {
			continue
		}
		seen[o.output] = true

		byRule := map[string][]string{}
		var rules []string
		for _, f := range gooracle.Check("out.go", o.output) {
			if byRule[f.Rule] == nil {
				rules = append(rules, f.Rule)
			}
			byRule[f.Rule] = append(byRule[f.Rule], f.String())
		}
		for _, rule := range rules {
			fail(o, rule, byRule[rule])
		}
		if byRule["parse"] != nil {
			continue
		}

		if reason := multiFileReason(o.filePath); reason != "" {
			skipReasons[reason]++
			continue
		}
		res := imp.TypeCheck("out.go", o.output)
		switch {
		case res.Skipped != "":
			skipReasons[res.Skipped]++
		case len(res.Errors) > 0:
			fail(o, "typecheck", res.Errors)
		default:
			typeChecked++
		}
		softTotal += res.Soft
	}

	skipped := 0
	for _, n := range skipReasons {
		skipped += n
	}
	fmt.Printf("gooracle: %d Transpile outputs from %d tests; %d distinct outputs parse+leak checked; "+
		"%d type-checked clean, %d type-check skipped, %d soft (unused) errors ignored\n",
		len(outputs), len(tests), len(seen), typeChecked, skipped, softTotal)
	reasons := make([]string, 0, len(skipReasons))
	for r := range skipReasons {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Printf("gooracle:   skipped %4d  %s\n", skipReasons[r], r)
	}

	ok := true
	var stale []string
	for key := range oracleAllowlist {
		if !usedAllow[key] {
			stale = append(stale, key)
		}
	}
	// Only a full run can tell an entry is stale; a -run filter may simply
	// not have reached the allowlisted test.
	if len(stale) > 0 && !runFiltered() {
		sort.Strings(stale)
		fmt.Printf("gooracle: FAIL: allowlist entries that no longer match anything (remove them): %s\n", strings.Join(stale, ", "))
		ok = false
	}

	if len(failures) > 0 {
		ok = false
		sort.SliceStable(failures, func(i, j int) bool { return failures[i].o.site < failures[j].o.site })
		fmt.Printf("gooracle: FAIL: %d generated outputs are not valid Go\n", len(failures))
		for _, f := range failures {
			fmt.Printf("\n--- %s (%s) [%s]\n", f.o.test, f.o.site, f.rule)
			for _, s := range f.findings {
				fmt.Printf("    %s\n", s)
			}
			fmt.Printf("  input:\n%s\n  output:\n%s\n", indent(f.o.input), indent(f.o.output))
		}
	}
	return ok
}

func runFiltered() bool {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.run") || strings.HasPrefix(a, "-test.skip") {
			return true
		}
	}
	return os.Getenv("TESTBRIDGE_TEST_ONLY") != ""
}

// multiFileReason explains why an output cannot be type-checked on its own
// because it is one file of a larger package, or returns "".
func multiFileReason(filePath string) string {
	if filePath == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Dir(filePath))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if n == filepath.Base(filePath) || e.IsDir() {
			continue
		}
		if strings.HasSuffix(n, "_test.go") || strings.HasSuffix(n, "_test.gala") || strings.HasSuffix(n, ".gen.go") {
			continue
		}
		if strings.HasSuffix(n, ".gala") || strings.HasSuffix(n, ".go") {
			return "one file of a multi-file package; its siblings are not type-checked with it"
		}
	}
	return ""
}

// newOracleImporter resolves the Go standard library from the SDK the analyzer
// found, and every GALA package under this module from source.
func newOracleImporter() *gooracle.Importer {
	cfg := gooracle.ImporterConfig{
		ModulePath: "martianoff/gala",
		Transpile: func(src, path string) (string, error) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			return transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).Transpile(src, path)
		},
	}
	if roots := getStdSearchPath(); len(roots) > 0 {
		cfg.Root = roots[0]
	}
	// GoImporterAvailable locates the SDK and points go/build at it, which
	// the source importer reads.
	if analyzer.GoImporterAvailable() {
		cfg.Go = importer.ForCompiler(token.NewFileSet(), "source", nil)
	}
	return gooracle.NewImporter(cfg)
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    | " + l
	}
	return strings.Join(lines, "\n")
}

// checkGeneratedGo runs the oracle's parse and leak check on Go a test
// produced by calling Transform and Generate itself rather than Transpile —
// multi-file tests that analyze sibling files together. Those outputs are one
// file of a package, so they are not type-checked.
func checkGeneratedGo(t *testing.T, src string) {
	t.Helper()
	if findings := gooracle.Check("out.go", src); len(findings) > 0 {
		var b strings.Builder
		for _, f := range findings {
			fmt.Fprintf(&b, "  %s\n", f)
		}
		t.Errorf("generated Go is not valid:\n%s--- generated ---\n%s", b.String(), src)
	}
}

// unwrappedTranspilerFiles may construct transpilers without the oracle: they
// transpile the example corpus, which TestGeneratedGoOracleCorpus covers, or
// they are the oracle.
var unwrappedTranspilerFiles = map[string]string{
	"oracle_test.go":           "the oracle's own importer transpiles imported GALA packages",
	"corpus_shared_test.go":    "example corpus; checked by TestGeneratedGoOracleCorpus",
	"compilation_gate_test.go": "example corpus; checked by TestGeneratedGoOracleCorpus",
	"generated_format_test.go": "example corpus; checked by TestGeneratedGoOracleCorpus",
}

// TestOracleCoversEveryTranspiler keeps new tests on the checked constructor.
// A test calling transpiler.NewGalaToGoTranspiler directly would silently fall
// outside the oracle.
func TestOracleCoversEveryTranspiler(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, files, "test sources are not in the working directory; "+
		"the go_test target must list them in data")
	var offenders []string
	for _, f := range files {
		if _, ok := unwrappedTranspilerFiles[f]; ok {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		s := string(src)
		if strings.Contains(s, "transpiler.NewGalaToGoTranspiler(") {
			offenders = append(offenders, f+" (constructs an unchecked transpiler; use newCheckedTranspiler)")
		}
		if strings.Contains(s, ".Generate(fset, ") && !strings.Contains(s, "checkGeneratedGo(") {
			offenders = append(offenders, f+" (calls Generate without checkGeneratedGo)")
		}
	}
	require.Empty(t, offenders, "these tests produce Go the generated-Go oracle never sees")
}

// TestGeneratedGoOracleCorpus runs the oracle's parse and leak check over
// every example that transpiles on its own.
func TestGeneratedGoOracleCorpus(t *testing.T) {
	files := exampleCorpus(t)
	checked := 0
	for _, f := range files {
		if f.Err != nil {
			// Counted and capped by TestNoTypeErasureInGeneratedGo.
			continue
		}
		checked++
		if findings := gooracle.Check(f.Name+".go", f.Output); len(findings) > 0 {
			var b strings.Builder
			for _, fd := range findings {
				fmt.Fprintf(&b, "  %s\n", fd)
			}
			t.Errorf("%s: generated Go is not valid:\n%s", f.Name, b.String())
		}
	}
	t.Logf("gooracle corpus: %d of %d examples checked", checked, len(files))
	require.NotZero(t, checked)
}
