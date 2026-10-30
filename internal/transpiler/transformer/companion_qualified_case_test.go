package transformer_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestQualifiedCompanionCase: json and yaml both declare a sealed Naming with
// a case AsIs, and a file dot-imports json beside importing yaml by name (or
// under an alias). A case pattern means its own package's case: yaml's by
// `case yaml.AsIs()` or `case y.AsIs()`, json's by a bare `case AsIs()`.
//
// Where the subject's type is unknown (a Go package nothing resolves), the
// match takes its type from the cases (inferMatchedTypeFromCases), so the
// qualified case decides it; on master that lookup dropped the qualifier and
// took whichever AsIs the analyzer had filed bare, in map order. Two packages
// that share a name (a/kind and b/kind, one recorded under a key) are told
// apart through their aliases the same way. A bare case resolves with the
// precedence of every bare type name: std's Success before a dot import's.
// A default lowered in another package resolves its bare case as the file
// it was written in does, not through the bare companion of the package
// lowering it. (Two dot imports exporting one name are rejected, GALA-E0032,
// so they never tie.) Each run is a fresh analyzer.
func TestQualifiedCompanionCase(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/naming\n\ngala dev\n")
	for _, dir := range []string{"a/kind", "b/kind"} {
		write(dir+"/kind.gala", "package kind\n\nsealed type Kind {\n    case Same()\n    case Diff()\n}\n")
	}
	write("c/kinda/kinda.gala", "package kinda\n\nsealed type KindA {\n    case Same()\n    case DiffA()\n}\n")
	write("e/res/res.gala", "package res\n\nsealed type Res {\n    case Success(V int)\n    case Fail()\n}\n")
	// lib's default names a bare case its own file dot-imports; the main
	// package that lowers it has a case of that name of its own.
	write("lib/lib.gala", `package lib

import (
    . "example.com/naming/c/kinda"
    "example.com/nowhere/gonaming"
)

func Describe(label string = gonaming.Current() match {
    case Same() => "kinda same"
    case _ => "kinda other"
}) string = label
`)
	// jsonAsIs matches json's case, which the dot import leaves unqualified.
	jsonAsIs := regexp.MustCompile(`[^.]AsIs\{\}\.Unapply\(`)
	tests := []struct {
		name string
		src  string
		want []string
		// wantRe are patterns the output must match.
		wantRe []*regexp.Regexp
	}{
		{
			name: "typed subjects",
			src: `package main

import (
    . "martianoff/gala/json"
    "martianoff/gala/yaml"
)

func yamlName(n yaml.Naming) string = n match {
    case yaml.AsIs() => "yaml as-is"
    case _ => "yaml other"
}

func jsonName(n Naming) string = n match {
    case AsIs() => "json as-is"
    case _ => "json other"
}

func main() {
    Println(yamlName(yaml.AsIs()), jsonName(AsIs()))
}
`,
			want:   []string{"yaml.AsIs{}.Unapply("},
			wantRe: []*regexp.Regexp{jsonAsIs},
		},
		{
			name: "a subject typed by a qualified case",
			src: `package main

import (
    . "martianoff/gala/json"
    "martianoff/gala/yaml"
    "example.com/nowhere/gonaming"
)

func describe() string = gonaming.Current() match {
    case yaml.AsIs() => "yaml as-is"
    case _ => "yaml other"
}

func main() {
    Println(describe(), AsIs())
}
`,
			want: []string{"func(obj yaml.Naming) string", "yaml.AsIs{}.Unapply(obj)"},
		},
		{
			name: "a subject typed by a case qualified with an alias",
			src: `package main

import (
    . "martianoff/gala/json"
    y "martianoff/gala/yaml"
    "example.com/nowhere/gonaming"
)

func describe() string = gonaming.Current() match {
    case y.AsIs() => "yaml as-is"
    case _ => "yaml other"
}

func main() {
    Println(describe(), AsIs())
}
`,
			want: []string{"func(obj y.Naming) string", "y.AsIs{}.Unapply(obj)"},
		},
		{
			name: "a subject typed by a case of one of two packages that share a name",
			src: `package main

import (
    ka "example.com/naming/a/kind"
    kb "example.com/naming/b/kind"
    "example.com/nowhere/gonaming"
)

func describe() string = gonaming.Current() match {
    case kb.Same() => "b same"
    case _ => "b other"
}

func main() {
    Println(describe(), ka.Same())
}
`,
			want: []string{"func(obj kb.Kind) string", "kb.Same{}.Unapply(obj)"},
		},
		{
			name: "a bare case of std and of a dot import: std's",
			src: `package main

import (
    . "example.com/naming/e/res"
    "example.com/nowhere/gonaming"
)

func describe() string = gonaming.Current() match {
    case Success(v) => s"ok $v"
    case _ => "other"
}

func main() {
    Println(describe(), Fail())
}
`,
			want: []string{"func(obj std.Try["},
		},
		{
			name: "a default lowered in a package with a bare case of the name",
			src: `package main

import "example.com/naming/lib"

sealed type Local {
    case Same()
    case Other()
}

func main() {
    Println(lib.Describe(), Same(), Other())
}
`,
			want: []string{"func(obj kinda.KindA) string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for run := 0; run < 20; run++ {
				out, err := transpileCrossPkg(t, root, tt.src)
				require.NoError(t, err, "run %d", run)
				for _, w := range tt.want {
					if !strings.Contains(out, w) {
						t.Fatalf("run %d: output lacks %q:\n%s", run, w, out)
					}
				}
				for _, re := range tt.wantRe {
					if !re.MatchString(out) {
						t.Fatalf("run %d: output does not match %s:\n%s", run, re, out)
					}
				}
			}
		})
	}
}
