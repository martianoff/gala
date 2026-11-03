// Package noantlr holds a package to galop's native tree in a test: none of
// its sources imports the packages of ANTLR's tree, and its Bazel library does
// not depend on them.
package noantlr

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Packages are the Go packages of ANTLR's tree: its runtime, the generated
// parser, and galop's converter from ANTLR's tree to the native one.
var Packages = []string{
	"github.com/antlr4-go/antlr/v4",
	"martianoff/gala/internal/parser/grammar",
	"martianoff/gala/internal/parser/galop",
}

// labels are the Bazel labels of Packages.
var labels = []string{
	"@com_github_antlr4_go_antlr_v4//:antlr",
	"//internal/parser/grammar",
	"//internal/parser/galop",
}

// Check fails t when a non-test Go source of the working directory imports
// one of Packages, or when the go_library named library in its BUILD.bazel
// depends on one. The test stages the sources and BUILD.bazel as data;
// minSources is a floor on the sources read, which catches them not being
// staged.
func Check(t *testing.T, library string, minSources int) {
	t.Helper()
	srcs, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	read := 0
	for _, src := range srcs {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, src, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		read++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, banned := range Packages {
				if path == banned {
					t.Errorf("%s imports %s", src, path)
				}
			}
		}
	}
	if read < minSources {
		t.Fatalf("read %d sources of %s, want at least %d: they are not staged", read, library, minSources)
	}

	build, err := os.ReadFile("BUILD.bazel")
	if err != nil {
		t.Fatal(err)
	}
	lib := regexp.MustCompile(`(?s)go_library\(\s*name = "` + regexp.QuoteMeta(library) + `",.*?\n\)`).Find(build)
	if lib == nil {
		t.Fatalf("no go_library named %s in BUILD.bazel", library)
	}
	for _, label := range labels {
		if strings.Contains(string(lib), `"`+label+`"`) {
			t.Errorf("the %s library depends on %s", library, label)
		}
	}
}
