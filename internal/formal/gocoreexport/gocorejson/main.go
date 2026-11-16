// Command gocorejson exports Go source files to GoCore JSON (see ../SCHEMA.md)
// and writes the document to stdout: the main package's files, then each
// imported package's, given as importpath=directory (every .go file there
// but tests). It makes the fixtures of the Lean model GoCore (formal/tests)
// and is a development tool only: it is not part of the gala CLI.
//
//	bazel run //internal/formal/gocoreexport/gocorejson -- main.go martianoff/gala/std=/path/to/std
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"martianoff/gala/internal/formal/gocoreexport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gocorejson:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gocorejson [-goroot=dir] <main.go>... [importpath=dir]...")
	}
	mainPkg := gocoreexport.PackageSource{Path: "main"}
	var pkgs []gocoreexport.PackageSource
	goroot := ""
	for _, a := range args {
		// -goroot=dir: the standard library's runnable files come from dir/src
		// (gocoreexport.AddStdlib), as the differential run adds them.
		if r, ok := strings.CutPrefix(a, "-goroot="); ok {
			if r == "" {
				return fmt.Errorf("-goroot= without a directory")
			}
			goroot = r
			continue
		}
		path, dir, isPkg := strings.Cut(a, "=")
		if !isPkg {
			src, err := read(a)
			if err != nil {
				return err
			}
			mainPkg.Files = append(mainPkg.Files, src)
			continue
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return err
		}
		pkg := gocoreexport.PackageSource{Path: path}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := read(f)
			if err != nil {
				return err
			}
			pkg.Files = append(pkg.Files, src)
		}
		pkgs = append(pkgs, pkg)
	}
	all, err := gocoreexport.AddStdlib(goroot, append([]gocoreexport.PackageSource{mainPkg}, pkgs...))
	if err != nil {
		return err
	}
	prog, err := gocoreexport.Export(all)
	if err != nil {
		return err
	}
	data, err := gocoreexport.Marshal(prog)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func read(path string) (gocoreexport.Source, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return gocoreexport.Source{}, err
	}
	return gocoreexport.Source{Name: filepath.Base(path), Text: string(text)}, nil
}
