// Command galacore exports GALA source files to Core JSON (see ../SCHEMA.md),
// with the analyzer's integer literal hints, and writes the document to
// stdout. It makes the fixtures of the Lean reference interpreter
// (formal/tests) and is a development tool only: it is not part of the gala
// CLI.
//
//	bazel run //internal/formal/coreexport/galacore -- main.gala > main.json
package main

import (
	"fmt"
	"os"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "galacore:", err)
		os.Exit(1)
	}
}

func run(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: galacore <file.gala>...")
	}
	srcs := make([]coreexport.Source, 0, len(paths))
	for _, p := range paths {
		text, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		srcs = append(srcs, coreexport.Source{Path: p, Text: string(text)})
	}
	doc, err := coreexport.Export(srcs, coreexport.Options{LiteralType: analyzerhints.LiteralType})
	if err != nil {
		return err
	}
	data, err := coreexport.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}
