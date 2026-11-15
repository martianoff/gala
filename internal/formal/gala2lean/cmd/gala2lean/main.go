// Command gala2lean translates GALA source files into a Lean 4 module (see
// the gala2lean package) and writes it to stdout. A development tool only:
// it is not part of the gala CLI.
//
//	bazel run //internal/formal/gala2lean/cmd/gala2lean -- -main "$PWD/main.gala" > Main.lean
//
// With -main the module ends with a `main` that runs the program, so
// `lean --run Main.lean` (with the prelude, formal/Gala/Gala2Lean/Prelude.lean,
// built and on LEAN_PATH) prints what the program prints and, on stderr, its
// outcome. A program outside the translated subset is reported on stderr,
// each construct named, with exit status 3.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
	"martianoff/gala/internal/formal/gala2lean"
)

func main() {
	ns := flag.String("namespace", "GalaProgram", "the Lean namespace of the program's definitions")
	withMain := flag.Bool("main", false, "end the module with a main that runs the program")
	library := flag.Bool("library", false, "translate a package without a main")
	stats := flag.Bool("stats", false, "print how many functions translate instead of the Lean")
	module := flag.String("module", "", "the root of the program's module (its gala.mod): the packages of the module it imports are translated with it")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: gala2lean [-namespace N] [-main] [-module DIR] <file.gala>...")
		os.Exit(2)
	}
	doc, err := export(flag.Args())
	if err != nil {
		fail(err)
	}
	var deps []gala2lean.Package
	if *module != "" {
		if deps, err = moduleDeps(doc, *module); err != nil {
			fail(err)
		}
	}
	res, err := gala2lean.Translate(doc, gala2lean.Options{Namespace: *ns, Main: *withMain, Library: *library, Deps: deps})
	var u *gala2lean.UnsupportedError
	if *stats {
		failed := errors.As(err, &u)
		if err != nil && !failed {
			fail(err)
		}
		s := gala2lean.CountTranslated(doc, err)
		fmt.Printf("functions %d translated %d methods %d\n", s.Funcs, s.Translated, s.Methods)
		return
	}
	if errors.As(err, &u) {
		for _, it := range u.Items {
			fmt.Fprintln(os.Stderr, "gala2lean: not translated:", it)
		}
		os.Exit(3)
	}
	if err != nil {
		fail(err)
	}
	if _, err := os.Stdout.WriteString(res.Lean); err != nil {
		fail(err)
	}
}


// export reads and exports the .gala files paths.
func export(paths []string) (*coreexport.Program, error) {
	srcs := make([]coreexport.Source, 0, len(paths))
	for _, p := range paths {
		text, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, coreexport.Source{Path: p, Text: string(text)})
	}
	return coreexport.Export(srcs, coreexport.Options{LiteralType: analyzerhints.LiteralType})
}

// moduleDeps is the packages of the module rooted at root that p imports,
// directly or through each other: an import path under the module's path
// (gala.mod's `module` line) is the directory below root, without its
// _test.gala files.
func moduleDeps(p *coreexport.Program, root string) ([]gala2lean.Package, error) {
	mod, err := os.ReadFile(filepath.Join(root, "gala.mod"))
	if err != nil {
		return nil, err
	}
	modPath := gala2lean.ModulePath(string(mod))
	if modPath == "" {
		return nil, fmt.Errorf("%s: no module line", filepath.Join(root, "gala.mod"))
	}
	return gala2lean.ModuleDeps(p, modPath, func(path string) (*coreexport.Program, error) {
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, modPath+"/")))
		// filepath.Glob sorts, the order the gala CLI builds a package's
		// files in, and so Go initializes their variables in.
		files, err := filepath.Glob(filepath.Join(dir, "*.gala"))
		if err != nil {
			return nil, err
		}
		files = slices.DeleteFunc(files, func(f string) bool { return strings.HasSuffix(f, "_test.gala") })
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no .gala files for %s", dir, path)
		}
		return export(files)
	})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gala2lean:", err)
	os.Exit(1)
}
