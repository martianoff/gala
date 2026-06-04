package gala

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// fakeImports maps a file name to the imports the fake helper should report.
var fakeImports = map[string]rawFile{
	"regex.gala": {
		File:    "regex.gala",
		Package: "regex",
		Imports: []rawImport{
			{Path: "regexp"},
			{Path: "martianoff/gala/std", Dot: true},
			{Path: "martianoff/gala/collection_immutable", Dot: true},
			{Path: "martianoff/gala/go_interop"},
		},
	},
	"regex_test.gala": {
		File:    "regex_test.gala",
		Package: "main",
		Imports: []rawImport{
			{Path: "martianoff/gala/test", Dot: true},
			{Path: "martianoff/gala/regex"},
		},
	},
	"math.gala": {File: "math.gala", Package: "mathlib"},
	"main.gala": {
		File:    "main.gala",
		Package: "main",
		Imports: []rawImport{{Path: "martianoff/gala/collection_immutable", Dot: true}},
	},
	"core.gala": {File: "core.gala", Package: "core"},
	"coll.gala": {File: "coll.gala", Package: "coll"},
	"perf_test.gala": {
		File:    "perf_test.gala",
		Package: "main",
		Imports: []rawImport{{Path: "martianoff/gala/collection_immutable", Dot: true}},
	},
	"alpha_test.gala": {
		File:    "alpha_test.gala",
		Package: "main",
		Imports: []rawImport{
			{Path: "martianoff/gala/test", Dot: true},
			{Path: "martianoff/gala/multitest"},
		},
	},
	"beta_test.gala": {
		File:    "beta_test.gala",
		Package: "main",
		Imports: []rawImport{
			{Path: "martianoff/gala/test", Dot: true},
			{Path: "martianoff/gala/collection_immutable", Dot: true},
		},
	},
}

// fakeRunner is an importRunner that emits the JSON contract for the requested
// files from fakeImports, so tests never touch a real "gala" binary.
func fakeRunner(helper, dir string, files []string) ([]byte, error) {
	out := make([]rawFile, 0, len(files))
	for _, f := range files {
		if rf, ok := fakeImports[f]; ok {
			out = append(out, rf)
		} else {
			out = append(out, rawFile{File: f})
		}
	}
	return json.Marshal(out)
}

func testConfig() *config.Config {
	c := config.New()
	c.Exts[languageName] = newGalaConfig()
	return c
}

func genArgs(c *config.Config, rel string, files []string) language.GenerateArgs {
	return language.GenerateArgs{
		Config:       c,
		Dir:          filepath.Join("testdata", rel),
		Rel:          rel,
		RegularFiles: files,
	}
}

func attrStrings(r *rule.Rule, key string) []string {
	v := r.AttrStrings(key)
	sort.Strings(v)
	return v
}

func TestGenerateLibraryAndTest(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	res := gl.GenerateRules(genArgs(c, "regexlike", []string{"regex.gala", "regex_test.gala"}))

	if len(res.Gen) != 2 {
		t.Fatalf("got %d rules, want 2 (library + test)", len(res.Gen))
	}
	lib := res.Gen[0]
	if lib.Kind() != "gala_library" || lib.Name() != "regexlike" {
		t.Errorf("rule0 = %s %q, want gala_library regexlike", lib.Kind(), lib.Name())
	}
	if got := lib.AttrString("importpath"); got != "martianoff/gala/regexlike" {
		t.Errorf("importpath = %q", got)
	}
	if got := attrStrings(lib, "srcs"); !reflect.DeepEqual(got, []string{"regex.gala"}) {
		t.Errorf("lib srcs = %v", got)
	}
	test := res.Gen[1]
	// One gala_test per file, named after the file stem.
	if test.Kind() != "gala_test" || test.Name() != "regex_test" {
		t.Errorf("rule1 = %s %q, want gala_test regex_test", test.Kind(), test.Name())
	}
	if got := attrStrings(test, "srcs"); !reflect.DeepEqual(got, []string{"regex_test.gala"}) {
		t.Errorf("test srcs = %v", got)
	}
}

func TestGenerateOneTestPerFile(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	res := gl.GenerateRules(genArgs(c, "multitest",
		[]string{"core.gala", "alpha_test.gala", "beta_test.gala"}))

	// Expect: 1 library + 2 separate per-file gala_test rules.
	if len(res.Gen) != 3 {
		t.Fatalf("got %d rules, want 3 (library + 2 per-file tests)", len(res.Gen))
	}
	if res.Gen[0].Kind() != "gala_library" {
		t.Errorf("rule0 = %s, want gala_library", res.Gen[0].Kind())
	}

	// Collect the test rules by name and check each carries only its own src
	// and its own import payload.
	tests := map[string]*rule.Rule{}
	payloads := map[string]*galaImports{}
	for i, r := range res.Gen {
		if r.Kind() == "gala_test" {
			tests[r.Name()] = r
			payloads[r.Name()] = res.Imports[i].(*galaImports)
		}
	}
	if len(tests) != 2 {
		t.Fatalf("got %d gala_test rules, want 2: %v", len(tests), keys(tests))
	}
	for name, wantSrc := range map[string]string{
		"alpha_test": "alpha_test.gala",
		"beta_test":  "beta_test.gala",
	} {
		r, ok := tests[name]
		if !ok {
			t.Errorf("missing per-file test rule %q", name)
			continue
		}
		if got := attrStrings(r, "srcs"); !reflect.DeepEqual(got, []string{wantSrc}) {
			t.Errorf("%s srcs = %v, want [%s]", name, got, wantSrc)
		}
	}

	// Per-file deps must not be unioned: alpha imports multitest, beta imports
	// collection_immutable — neither should see the other's import.
	if got := payloads["alpha_test"].imports; !contains(got, "martianoff/gala/multitest") || contains(got, "martianoff/gala/collection_immutable") {
		t.Errorf("alpha_test imports leaked across files: %v", got)
	}
	if got := payloads["beta_test"].imports; !contains(got, "martianoff/gala/collection_immutable") || contains(got, "martianoff/gala/multitest") {
		t.Errorf("beta_test imports leaked across files: %v", got)
	}
}

func TestGenerateSkipsBenchmarkMainTest(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	res := gl.GenerateRules(genArgs(c, "benchlike",
		[]string{"coll.gala", "perf_test.gala"}))

	// perf_test.gala is package main with main() — a benchmark binary, not a
	// framework test. It must NOT produce a gala_test (which would collide with
	// the hand-wired gala_binary). Only the library should be generated.
	if len(res.Gen) != 1 {
		t.Fatalf("got %d rules, want 1 (library only): %v", len(res.Gen), ruleKinds(res.Gen))
	}
	if res.Gen[0].Kind() != "gala_library" {
		t.Errorf("rule0 = %s, want gala_library", res.Gen[0].Kind())
	}
	for _, r := range res.Gen {
		if r.Kind() == "gala_test" {
			t.Errorf("unexpected gala_test %q generated for a benchmark main", r.Name())
		}
	}
}

func ruleKinds(rules []*rule.Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Kind() + ":" + r.Name()
	}
	return out
}

func keys(m map[string]*rule.Rule) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestGenerateLibraryNoImports(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	res := gl.GenerateRules(genArgs(c, "mathlike", []string{"math.gala"}))
	if len(res.Gen) != 1 {
		t.Fatalf("got %d rules, want 1", len(res.Gen))
	}
	r := res.Gen[0]
	if r.Kind() != "gala_library" || r.AttrString("importpath") != "martianoff/gala/mathlike" {
		t.Errorf("got %s importpath=%q", r.Kind(), r.AttrString("importpath"))
	}
}

func TestGenerateBinary(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	res := gl.GenerateRules(genArgs(c, "binlike", []string{"main.gala"}))
	if len(res.Gen) != 1 {
		t.Fatalf("got %d rules, want 1", len(res.Gen))
	}
	r := res.Gen[0]
	if r.Kind() != "gala_binary" || r.Name() != "binlike" {
		t.Errorf("got %s %q, want gala_binary binlike", r.Kind(), r.Name())
	}
	if r.AttrString("importpath") != "" {
		t.Errorf("binary should not have importpath, got %q", r.AttrString("importpath"))
	}
}

func TestGeneratePrefixDirective(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	getGalaConfig(c).Prefix = "github.com/me/app"
	res := gl.GenerateRules(genArgs(c, "mathlike", []string{"math.gala"}))
	if got := res.Gen[0].AttrString("importpath"); got != "github.com/me/app/mathlike" {
		t.Errorf("importpath = %q, want github.com/me/app/mathlike", got)
	}
}
