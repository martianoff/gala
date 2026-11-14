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

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
	"martianoff/gala/internal/formal/gala2lean"
)

func main() {
	ns := flag.String("namespace", "GalaProgram", "the Lean namespace of the program's definitions")
	withMain := flag.Bool("main", false, "end the module with a main that runs the program")
	library := flag.Bool("library", false, "translate a package without a main")
	stats := flag.Bool("stats", false, "print how many functions translate instead of the Lean")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: gala2lean [-namespace N] [-main] <file.gala>...")
		os.Exit(2)
	}
	srcs := make([]coreexport.Source, 0, flag.NArg())
	for _, p := range flag.Args() {
		text, err := os.ReadFile(p)
		if err != nil {
			fail(err)
		}
		srcs = append(srcs, coreexport.Source{Path: p, Text: string(text)})
	}
	doc, err := coreexport.Export(srcs, coreexport.Options{LiteralType: analyzerhints.LiteralType})
	if err != nil {
		fail(err)
	}
	res, err := gala2lean.Translate(doc, gala2lean.Options{Namespace: *ns, Main: *withMain, Library: *library})
	var u *gala2lean.UnsupportedError
	if *stats {
		failed := errors.As(err, &u)
		if err != nil && !failed {
			fail(err)
		}
		printStats(doc, u, failed)
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

// printStats prints one line: the functions and methods declared, and how
// many of them gala2lean translates (a method never is: Core JSON leaves
// methods out).
func printStats(doc *coreexport.Program, u *gala2lean.UnsupportedError, failed bool) {
	funcs, methods := 0, 0
	names := map[string]bool{}
	for _, f := range doc.Files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *coreexport.Func:
				funcs++
				names[d.Name] = true
			case *coreexport.Unsupported:
				// A generic declaration may be a type, so only methods
				// count.
				if d.What == "method" {
					methods++
				}
			}
		}
	}
	untranslated := map[string]bool{}
	if failed {
		for _, it := range u.Items {
			if names[it.Decl] {
				untranslated[it.Decl] = true
			}
		}
	}
	fmt.Printf("functions %d translated %d methods %d\n", funcs+methods, funcs-len(untranslated), methods)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gala2lean:", err)
	os.Exit(1)
}
