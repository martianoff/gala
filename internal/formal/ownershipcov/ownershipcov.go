// Package ownershipcov runs the ownership analysis (phase 1,
// internal/transpiler/ownership) over the repository's GALA code and writes
// the escape reasons it finds into formal/COVERAGE.md, between its ownership
// markers. It is test tooling: no part of the gala CLI links it.
package ownershipcov

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/ownership"
)

// Markers delimit the generated section of formal/COVERAGE.md.
const (
	BeginMarker = "<!-- ownership:begin -->"
	EndMarker   = "<!-- ownership:end -->"
)

// Tree is the analysis of one part of the repository: its packages'
// reports, and the packages that did not analyze.
type Tree struct {
	Name    string
	Reports []*ownership.Report
	Failed  []string
}

// notLibraries are the top-level directories that hold no GALA library.
var notLibraries = map[string]bool{
	"examples": true, "formal": true, "ide": true, "website": true, "internal": true, "tools": true,
	"docs": true, "bazel_test_fixtures": true, "cmd": true, "nix": true,
}

// Libraries analyzes every GALA library package of the repository at root:
// each directory with non-test .gala files under a top-level directory that
// is not one of notLibraries, testdata excluded.
func Libraries(root string, searchPaths ...string) (*Tree, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	t := &Tree{Name: "Libraries"}
	a := newRunner(root, searchPaths)
	for _, e := range entries {
		if !e.IsDir() || notLibraries[e.Name()] || skipDir(e.Name()) {
			continue
		}
		err := filepath.WalkDir(filepath.Join(root, e.Name()), func(dir string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if files := galaFiles(dir); len(files) > 0 {
				rel, _ := filepath.Rel(root, dir)
				rep, err := a.analyze(files)
				t.add(filepath.ToSlash(rel), rep, err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// skipDir reports whether a directory holds no library code.
func skipDir(name string) bool {
	return name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-")
}

// Examples analyzes every examples/*.gala program of the repository at root,
// each a package of its own.
func Examples(root string, searchPaths ...string) (*Tree, error) {
	t := &Tree{Name: "Examples"}
	a := newRunner(root, searchPaths)
	for _, f := range galaFiles(filepath.Join(root, "examples")) {
		rep, err := a.analyze([]string{f})
		t.add("examples/"+filepath.Base(f), rep, err)
	}
	return t, nil
}

func (t *Tree) add(name string, r *ownership.Report, err error) {
	if err != nil {
		t.Failed = append(t.Failed, name)
		return
	}
	t.Reports = append(t.Reports, r)
}

// galaFiles is dir's non-test .gala files, sorted.
func galaFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".gala") && !strings.HasSuffix(n, "_test.gala") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out
}

// runner analyzes packages with one analyzer, so that the packages they
// import are loaded once.
type runner struct {
	root string
	p    transpiler.GalaParser
	a    *analyzer.BatchAnalyzer
}

func newRunner(root string, extra []string) *runner {
	p := parser.NewAntlrGalaParser()
	return &runner{root: root, p: p, a: analyzer.NewBatchAnalyzer(p, append([]string{root}, extra...), root)}
}

// analyze is the ownership analysis of the package of files, with paths in
// the report relative to the root.
func (r *runner) analyze(files []string) (rep *ownership.Report, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("analysis panicked: %v", p)
		}
	}()
	var srcs []ownership.File
	var rich *transpiler.RichAST
	r.a.SetPackageFiles(files)
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		parsed := r.p.ParseFile(string(text))
		if err := parsed.Err(); err != nil {
			return nil, err
		}
		if rich == nil {
			if rich, err = r.a.AnalyzeParsed(parsed, f); err != nil {
				return nil, err
			}
		}
		tree, err := parsed.CleanNative().Tree()
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(r.root, f)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, ownership.File{Path: filepath.ToSlash(rel), Root: native.SourceFileNode{GalaNodeRef: tree.Root()}})
	}
	return ownership.Analyze(rich.PackageName, srcs, rich, ownership.Options{HandWrittenGo: ownership.HasHandWrittenGo(filepath.Dir(files[0]))}), nil
}

// Splice replaces the generated section of doc, between BeginMarker and
// EndMarker, with section.
func Splice(doc, section string) (string, error) {
	begin := strings.Index(doc, BeginMarker)
	end := strings.Index(doc, EndMarker)
	if strings.Count(doc, BeginMarker) != 1 || strings.Count(doc, EndMarker) != 1 || end < begin {
		return "", fmt.Errorf("the document must hold %s and then %s, once each, around the generated section", BeginMarker, EndMarker)
	}
	return doc[:begin+len(BeginMarker)] + "\n" + strings.TrimRight(section, "\n") + "\n" + doc[end:], nil
}
