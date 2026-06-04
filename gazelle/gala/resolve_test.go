package gala

import (
	"reflect"
	"sort"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// indexFromLibs builds a RuleIndex containing gala_library rules so Resolve can
// translate in-repo imports to their labels.
func indexFromLibs(c *config.Config, gl *galaLang, libs map[string]string) *resolve.RuleIndex {
	ix := resolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) resolve.Resolver { return gl })
	for pkg, importpath := range libs {
		name := pkg
		if i := lastSlash(pkg); i >= 0 {
			name = pkg[i+1:]
		}
		r := rule.NewRule("gala_library", name)
		r.SetAttr("importpath", importpath)
		f := rule.EmptyFile(pkg+"/BUILD.bazel", pkg)
		r.Insert(f)
		ix.AddRule(c, r, f)
	}
	ix.Finish()
	return ix
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func resolveDeps(t *testing.T, c *config.Config, gl *galaLang, ix *resolve.RuleIndex, kind, pkg string, gi *galaImports) []string {
	t.Helper()
	r := rule.NewRule(kind, "target")
	from := label.New("", pkg, "target")
	gl.Resolve(c, ix, nil, r, gi, from)
	got := r.AttrStrings("deps")
	sort.Strings(got)
	return got
}

func TestResolveLibraryDeps(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	ix := indexFromLibs(c, gl, map[string]string{
		"collection_immutable": "martianoff/gala/collection_immutable",
		"go_interop":           "martianoff/gala/go_interop",
		"std":                  "martianoff/gala/std",
	})
	// regex library: imports regexp(skip), std(implicit/skip), collection_immutable, go_interop.
	gi := &galaImports{
		imports: []string{
			"regexp",
			"martianoff/gala/std",
			"martianoff/gala/collection_immutable",
			"martianoff/gala/go_interop",
		},
		self: "martianoff/gala/regex",
	}
	got := resolveDeps(t, c, gl, ix, "gala_library", "regex", gi)
	want := []string{"//collection_immutable", "//go_interop"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestResolveTestDeps(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	ix := indexFromLibs(c, gl, map[string]string{
		"regex": "martianoff/gala/regex",
	})
	// test in regex/ imports test(implicit/skip) and the sibling regex library.
	gi := &galaImports{
		imports: []string{"martianoff/gala/test", "martianoff/gala/regex"},
		self:    "",
	}
	got := resolveDeps(t, c, gl, ix, "gala_test", "regex", gi)
	want := []string{":regex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestResolveExternalStdlib(t *testing.T) {
	gl := &galaLang{runner: fakeRunner}
	c := testConfig()
	// Consumer repo: prefix differs from the stdlib namespace, and the stdlib
	// packages are not in-repo, so they map to @gala//<pkg>.
	gc := getGalaConfig(c)
	gc.Prefix = "github.com/me/app"
	ix := resolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) resolve.Resolver { return gl })
	ix.Finish()
	gi := &galaImports{
		imports: []string{
			"fmt",
			"martianoff/gala/std", // implicit, skipped
			"martianoff/gala/collection_immutable",
			"github.com/me/app/util",
		},
		self: "github.com/me/app/widget",
	}
	got := resolveDeps(t, c, gl, ix, "gala_library", "widget", gi)
	want := []string{"//util", "@gala//collection_immutable"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestImportsIndexing(t *testing.T) {
	gl := &galaLang{}
	lib := rule.NewRule("gala_library", "regex")
	lib.SetAttr("importpath", "martianoff/gala/regex")
	specs := gl.Imports(nil, lib, nil)
	if len(specs) != 1 || specs[0].Lang != "gala" || specs[0].Imp != "martianoff/gala/regex" {
		t.Errorf("Imports = %v", specs)
	}
	// binaries and tests are not importable.
	bin := rule.NewRule("gala_binary", "app")
	if specs := gl.Imports(nil, bin, nil); specs != nil {
		t.Errorf("binary Imports = %v, want nil", specs)
	}
}
