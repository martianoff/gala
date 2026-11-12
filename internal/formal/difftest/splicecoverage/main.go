// Command splicecoverage writes a differential run's coverage section into
// formal/COVERAGE.md, between its difftest markers, and leaves the rest of the
// file as it is. Run it with bazel run, on the coverage_section.md the
// differential test wrote to its undeclared outputs:
//
//	bazel run //internal/formal/difftest/splicecoverage -- /abs/path/coverage_section.md
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"martianoff/gala/internal/formal/difftest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "splicecoverage:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: splicecoverage <coverage_section.md>")
	}
	root := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if root == "" {
		return fmt.Errorf("run it with bazel run, which sets BUILD_WORKSPACE_DIRECTORY")
	}
	section, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	path := filepath.Join(root, "formal", "COVERAGE.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := difftest.Splice(string(doc), string(section))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
