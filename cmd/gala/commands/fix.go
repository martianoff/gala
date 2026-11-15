package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/inoutfix"
)

var (
	fixDryRun   bool
	fixPublic   bool
	fixNoVerify bool
	fixReport   bool
	fixSearch   string
)

var fixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Rewrite GALA code to use newer language features",
	Long:  `Rewrite GALA code to use newer language features. Each fix is a subcommand.`,
}

var fixInoutCmd = &cobra.Command{
	Use:   "inout [dir | file.gala ...]",
	Short: "Rewrite borrowed Ptr[T] receivers and parameters to inout T",
	Long: `Rewrite the Ptr[T] receivers and parameters that the ownership analysis finds
borrowed (only read through, written through and lent on, never stored,
compared, captured or passed to Go) to inout T, together with their uses:
*p becomes p, and p passed on to another converted parameter becomes &p.
Call sites need no change: &x and a Ptr both fill an inout parameter.

The rewritten package is compiled, and a function whose rewrite the compiler
rejects keeps its Ptr. Running the fix again changes nothing.

A public function (an exported function, or an exported method, of a package
other than main) keeps its Ptr unless --public is given: converting it changes
the package's API. So does a function used as a function value, or one of a
package with hand-written Go.

A dir ending in /... includes its subdirectories, and a .gala file is a
program of its own (as each of the examples is). The default is ".".

Examples:
  gala fix inout --dry-run ./...     # print the diff, change nothing
  gala fix inout ./parser            # rewrite one package
  gala fix inout --dry-run --public --report std/json`,
	Run: runFixInout,
}

func init() {
	fixInoutCmd.Flags().BoolVar(&fixDryRun, "dry-run", false, "Print the diff instead of writing the files")
	fixInoutCmd.Flags().BoolVar(&fixPublic, "public", false, "Convert public functions and methods too (changes the package's API)")
	fixInoutCmd.Flags().BoolVar(&fixNoVerify, "no-verify", false, "Do not compile the rewritten package")
	fixInoutCmd.Flags().BoolVar(&fixReport, "report", false, "List every Ptr receiver and parameter with its verdict, and the converted signatures")
	fixInoutCmd.Flags().StringVarP(&fixSearch, "search", "s", "", "Comma-separated extra search paths")
	fixCmd.AddCommand(fixInoutCmd)
	rootCmd.AddCommand(fixCmd)
}

func runFixInout(cmd *cobra.Command, args []string) {
	useGoExportData()
	dirs, err := fixDirs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	var total fixTotals
	failed := 0
	for _, dir := range dirs {
		cfg := inoutfix.Config{SearchPaths: fixSearchPaths(filepath.Dir(dir), cwd), Public: fixPublic, NoVerify: fixNoVerify}
		var res *inoutfix.Result
		if strings.HasSuffix(dir, ".gala") {
			// A file is a program of its own, as each of the examples is.
			res, err = inoutfix.Files([]string{dir}, cfg)
		} else {
			cfg.SearchPaths = fixSearchPaths(dir, cwd)
			res, err = inoutfix.Package(dir, cfg)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
			failed++
			continue
		}
		if res == nil {
			continue
		}
		total.add(res)
		out := cmd.OutOrStdout()
		if fixDryRun {
			fmt.Fprint(out, res.Diff(cwd))
		} else if err := res.Write(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
			failed++
			continue
		}
		if fixReport {
			printFixReport(out, res, cwd)
		}
	}
	fmt.Fprintln(cmd.ErrOrStderr(), total.String())
	if failed > 0 {
		os.Exit(1)
	}
}

// fixDirs expands the arguments to package directories: dir/... is dir and
// every directory below it, except hidden ones, testdata and bazel-*.
func fixDirs(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var out []string
	for _, arg := range args {
		root, recursive := strings.CutSuffix(filepath.ToSlash(arg), "/...")
		if root == "" {
			root = "."
		}
		if !recursive || strings.HasSuffix(root, ".gala") {
			out = append(out, filepath.Clean(root))
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-") || name == "testdata") {
				return filepath.SkipDir
			}
			out = append(out, filepath.Clean(path))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fixSearchPaths resolves a package's imports as gala doc does: the working
// directory, the package's parent, the module's dependencies and the
// standard library, then --search.
func fixSearchPaths(dir, cwd string) []string {
	abs, _ := filepath.Abs(dir)
	var extra []string
	if fixSearch != "" {
		extra = strings.Split(fixSearch, ",")
	}
	return autoResolveSearchPaths(filepath.Join(abs, "x.gala"), append([]string{cwd, filepath.Dir(abs)}, extra...))
}

// fixTotals counts the fix's verdicts over every package.
type fixTotals struct {
	packages, funcs, converted, bindings, convertedBindings, public, publicConverted, excluded int
}

func (t *fixTotals) add(r *inoutfix.Result) {
	t.packages++
	t.excluded += len(r.Excluded)
	for _, f := range r.Plan.Funcs {
		t.funcs++
		if f.Public {
			t.public++
		}
		if f.Converted() {
			t.converted++
			if f.Public {
				t.publicConverted++
			}
		}
		for _, b := range f.Bindings {
			t.bindings++
			if b.Converted {
				t.convertedBindings++
			}
		}
	}
}

func (t fixTotals) String() string {
	return fmt.Sprintf("inout: %d packages; %d of %d functions with a pointer receiver or parameter converted (%d of %d bindings); %d public (%d converted); %d left as they were because the rewrite did not compile",
		t.packages, t.converted, t.funcs, t.convertedBindings, t.bindings, t.public, t.publicConverted, t.excluded)
}

// printFixReport lists r's verdicts and converted signatures.
func printFixReport(out io.Writer, r *inoutfix.Result, cwd string) {
	rel := func(p string) string {
		if q, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(q, "..") {
			return filepath.ToSlash(q)
		}
		return p
	}
	fmt.Fprintf(out, "== %s (package %s)\n", rel(r.Dir), r.Package)
	for _, s := range r.Signatures(false) {
		tag := ""
		if s.Func.Public {
			tag = " [public]"
		}
		fmt.Fprintf(out, "converted%s %s:%d\n  - %s\n  + %s\n", tag, rel(s.Func.File), s.Func.Line, s.Before, s.After)
	}
	var kept []string
	for _, f := range r.Plan.Funcs {
		for _, b := range f.Bindings {
			if !b.Converted {
				kept = append(kept, fmt.Sprintf("kept %s:%d %s %s %s: %s", rel(f.File), f.Line, f.Name, b.Role, b.Name, b.Blocked))
			}
		}
	}
	sort.Strings(kept)
	for _, k := range kept {
		fmt.Fprintln(out, k)
	}
}

