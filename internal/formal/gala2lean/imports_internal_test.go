package gala2lean

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

// readRunfiles reads the files env names (a space-separated list of
// runfile paths), by runfile path.
func readRunfiles(t *testing.T, env string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range strings.Fields(os.Getenv(env)) {
		path, err := runfiles.Rlocation(p)
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		out[p] = strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	require.NotEmpty(t, out, "no files in %s", env)
	return out
}

// TestNoOutputInitImportsInStep checks that gala2lean and galaspec exempt
// the same imports (noOutputInitImports, Json.noOutputInitImports): both
// rest on the assumption that those packages print nothing while they
// initialize, and the two models must make it for the same packages.
func TestNoOutputInitImportsInStep(t *testing.T) {
	data := readRunfiles(t, "GALASPEC_JSON_LEAN")[os.Getenv("GALASPEC_JSON_LEAN")]
	def :=regexp.MustCompile(`(?s)def noOutputInitImports : List String :=\s*\[(.*?)\]`).FindStringSubmatch(data)
	require.NotNil(t, def, "no noOutputInitImports in formal/Gala/Json.lean")
	var lean []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(def[1], -1) {
		lean = append(lean, m[1])
	}
	require.Equal(t, lean, noOutputInitImports, "gala2lean's noOutputInitImports differs from galaspec's")
}

// TestQuietInitPackages checks the assumption noOutputInitImports rests on
// for the GALA packages in it, std and collection_immutable, whose source
// is here: no package-level initializer prints or panics. In their GALA
// files, no val or var initializer calls Print, Println or panic, directly
// or through a function of its package, nor holds a construct Core JSON
// does not show; in their Go files, there is no init function and no
// package-level initializer calls print, println, panic or fmt's printing.
func TestQuietInitPackages(t *testing.T) {
	for _, env := range []string{"STD_GALA", "COLLECTIONS_GALA"} {
		var srcs []coreexport.Source
		for path, text := range readRunfiles(t, env) {
			srcs = append(srcs, coreexport.Source{Path: path, Text: text})
		}
		p, err := coreexport.Export(srcs, coreexport.Options{LiteralType: analyzerhints.LiteralType})
		require.NoError(t, err)
		funcs := map[string]*coreexport.Func{}
		var inits []coreexport.Node
		for _, f := range p.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *coreexport.Func:
					if d.Receiver == nil {
						funcs[d.Name] = d
					}
				case *coreexport.Val:
					inits = append(inits, d.Values...)
				case *coreexport.Var:
					inits = append(inits, d.Values...)
				}
			}
		}
		seen := map[string]bool{}
		var scan func(n coreexport.Node, where string)
		scan = func(n coreexport.Node, where string) {
			walk(n, func(n coreexport.Node) {
				switch n := n.(type) {
				case *coreexport.Unsupported:
					t.Errorf("%s: %s: an initializer holds %s, which this check cannot see into", env, where, n.What)
				case *coreexport.Call:
					c, ok := n.Callee.(*coreexport.Name)
					if !ok {
						return
					}
					switch {
					case c.Name == "Print" || c.Name == "Println" || c.Name == "panic":
						t.Errorf("%s: %s: an initializer calls %s", env, where, c.Name)
					case funcs[c.Name] != nil && !seen[c.Name]:
						seen[c.Name] = true
						scan(funcs[c.Name].Body, c.Name)
					}
				}
			})
		}
		for _, n := range inits {
			scan(n, "package-level initializer")
		}
	}
	for path, text := range readRunfiles(t, "STD_GO") {
		f, err := parser.ParseFile(token.NewFileSet(), path, text, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					t.Errorf("%s: an init function", path)
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				ast.Inspect(d, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fn := call.Fun.(type) {
					case *ast.Ident:
						if fn.Name == "print" || fn.Name == "println" || fn.Name == "panic" {
							t.Errorf("%s: a package-level initializer calls %s", path, fn.Name)
						}
					case *ast.SelectorExpr:
						if x, ok := fn.X.(*ast.Ident); ok && x.Name == "fmt" && (strings.HasPrefix(fn.Sel.Name, "Print") || strings.HasPrefix(fn.Sel.Name, "Fprint")) {
							t.Errorf("%s: a package-level initializer calls fmt.%s", path, fn.Sel.Name)
						}
					}
					return true
				})
			}
		}
	}
}
