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
	if test.Kind() != "gala_test" || test.Name() != "regexlike_test" {
		t.Errorf("rule1 = %s %q, want gala_test regexlike_test", test.Kind(), test.Name())
	}
	if got := attrStrings(test, "srcs"); !reflect.DeepEqual(got, []string{"regex_test.gala"}) {
		t.Errorf("test srcs = %v", got)
	}
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
