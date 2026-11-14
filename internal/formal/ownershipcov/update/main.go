// Command update runs the ownership analysis over the repository's libraries
// and examples and writes its section of formal/COVERAGE.md, between the
// ownership markers, leaving the rest of the file as it is:
//
//	bazel run //internal/formal/ownershipcov/update
//
// With -json FILE it also writes every function's verdict, for measurements.
// With -root DIR it reads another checkout and prints the section instead.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"martianoff/gala/internal/formal/ownershipcov"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ownershipcov:", err)
		os.Exit(1)
	}
}

func run() error {
	jsonOut := flag.String("json", "", "also write every function's verdict to this file")
	root := flag.String("root", "", "the repository to analyze; the section is printed, not spliced")
	std := flag.String("std", "", "with -root, a GALA checkout to find std and the other libraries in")
	flag.Parse()
	splice := *root == ""
	if splice {
		*root = os.Getenv("BUILD_WORKSPACE_DIRECTORY")
		if *root == "" {
			return fmt.Errorf("run it with bazel run, which sets BUILD_WORKSPACE_DIRECTORY, or pass -root")
		}
	}
	var extra []string
	if *std != "" {
		extra = append(extra, *std)
	}
	libs, err := ownershipcov.Libraries(*root, extra...)
	if err != nil {
		return err
	}
	examples, err := ownershipcov.Examples(*root, extra...)
	if err != nil {
		return err
	}
	trees := []*ownershipcov.Tree{libs, examples}
	if *jsonOut != "" {
		data, err := json.MarshalIndent(trees, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*jsonOut, data, 0o644); err != nil {
			return err
		}
	}
	section := ownershipcov.Render(trees)
	if !splice {
		fmt.Print(section)
		return nil
	}
	path := filepath.Join(*root, "formal", "COVERAGE.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := ownershipcov.Splice(string(doc), section)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
