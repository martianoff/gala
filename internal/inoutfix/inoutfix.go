// Package inoutfix is `gala fix inout`: it rewrites the `Ptr[T]` receivers
// and parameters that ownership phase 1 finds borrowed to `inout T`
// (docs/OWNERSHIP_DESIGN.MD, section 4, "A safe codemod").
//
// The plan comes from ownership.PlanInout. The rewritten package is then
// transpiled, and every function the compiler rejects a use of is left as
// it was, until the package compiles: the plan cannot see every call site
// (a caller that passes a call result, or calls the method on a `val`).
// A function that keeps its `Ptr` is reported with the reason.
//
// The rewrite is idempotent: a converted binding is `inout`, which the plan
// never considers again.
package inoutfix

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/ownership"
	"martianoff/gala/internal/transpiler/transformer"
)

// Config configures a rewrite.
type Config struct {
	// SearchPaths resolve the package's imports, as the transpiler's do.
	SearchPaths []string
	// ProjectRoot holds gala.mod, for the analysis cache; "" detects it.
	ProjectRoot string
	// Public converts public functions too (ownership.AdoptFunc.Public),
	// which changes the package's API.
	Public bool
	// NoVerify skips compiling the rewritten package.
	NoVerify bool
}

// maxRounds bounds the compile-and-exclude rounds. Each round excludes at
// least one function, or all of them.
const maxRounds = 50

// FileChange is one rewritten file.
type FileChange struct {
	Path          string
	Before, After string
}

// Result is the rewrite of one package.
type Result struct {
	Dir     string
	Package string
	// Plan is the final plan: the one whose rewrite compiles.
	Plan *ownership.AdoptPlan
	// Changes are the files the rewrite changes.
	Changes []FileChange
	// Excluded are the functions left as they were because the rewritten
	// package did not compile, with the compiler's message.
	Excluded map[string]string

	sources map[string]string // by path, as parsed (no BOM)
}

// sourceFile is one parsed file of the package.
type sourceFile struct {
	path string
	src  string
}

// Package rewrites the package in dir: its .gala files, tests excluded. It
// reads them and computes the rewrite; it writes nothing (see Write). A
// directory without .gala files gives a nil Result.
func Package(dir string, cfg Config) (*Result, error) {
	paths, err := packageFiles(dir)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return Files(paths, cfg)
}

// Files rewrites the package of the given .gala files, all in one
// directory: a program of its own, such as one of the examples, when it is
// one file.
func Files(paths []string, cfg Config) (*Result, error) {
	dir := filepath.Dir(paths[0])
	p := parser.NewAntlrGalaParser()
	a := analyzer.NewBatchAnalyzer(p, cfg.SearchPaths, cfg.ProjectRoot)
	a.SetPackageFiles(paths)
	var files []sourceFile
	var srcs []ownership.File
	var rich *transpiler.RichAST
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text := galaerr.StripBOM(string(raw))
		files = append(files, sourceFile{path: path, src: text})
		parsed := p.ParseFile(text)
		if err := parsed.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if rich == nil {
			if rich, err = a.AnalyzeParsed(parsed, path); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		tree, err := parsed.CleanNative().Tree()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		srcs = append(srcs, ownership.File{Path: path, Root: native.SourceFileNode{GalaNodeRef: tree.Root()}})
	}
	res := &Result{Dir: dir, Package: rich.PackageName, Excluded: map[string]string{}, sources: map[string]string{}}
	for _, f := range files {
		res.sources[f.path] = f.src
	}
	opts := ownership.Options{HandWrittenGo: ownership.HasHandWrittenGo(dir)}
	for round := 0; ; round++ {
		res.Plan = ownership.PlanInout(rich.PackageName, srcs, rich, opts,
			ownership.AdoptConfig{Public: cfg.Public, Exclude: res.Excluded})
		after := apply(files, res.Plan.Edits)
		if len(res.Plan.Edits) == 0 || cfg.NoVerify {
			res.Changes = changes(files, after)
			return res, nil
		}
		if round == 0 {
			// The package as it is must compile where it is verified, or
			// no failure of the rewrite could be told from the setup's.
			if diags := verify(files, nil, cfg); len(diags) > 0 {
				return nil, fmt.Errorf("%s does not compile as it is, so its rewrite cannot be checked: %s", dir, diags[0].String())
			}
		}
		diags := verify(files, after, cfg)
		if len(diags) == 0 {
			res.Changes = changes(files, after)
			return res, nil
		}
		if round == maxRounds || !res.exclude(diags, after) {
			// Give up on the rest: leave every remaining function as it is.
			for _, f := range res.Plan.Funcs {
				if f.Converted() {
					res.Excluded[f.Name] = "the rewritten package does not compile: " + diags[0].String()
				}
			}
		}
	}
}

// packageFiles is dir's .gala files, tests excluded, sorted.
func packageFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".gala") && !strings.HasSuffix(n, "_test.gala") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

// apply is each file's source with the edits applied, by path.
func apply(files []sourceFile, edits []ownership.Edit) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		var b strings.Builder
		at := 0
		for _, e := range edits {
			if e.File != f.path {
				continue
			}
			b.WriteString(f.src[at:e.Start])
			b.WriteString(e.Text)
			at = e.End
		}
		b.WriteString(f.src[at:])
		out[f.path] = b.String()
	}
	return out
}

func changes(files []sourceFile, after map[string]string) []FileChange {
	var out []FileChange
	for _, f := range files {
		if after[f.path] != f.src {
			out = append(out, FileChange{Path: f.path, Before: f.src, After: after[f.path]})
		}
	}
	return out
}

// diagnostic is one compiler error of the rewritten package.
type diagnostic struct {
	file string // the package's file
	galaerr.Diagnostic
}

func (d diagnostic) String() string {
	s := d.Message
	if d.Code != "" {
		s = d.Code + ": " + s
	}
	if d.Line > 0 {
		s = fmt.Sprintf("%s:%d: %s", filepath.Base(d.file), d.Line, s)
	}
	return s
}

// verify transpiles the package with its files' sources replaced by after
// (nil: as they are), in a temporary directory, and returns the errors.
func verify(files []sourceFile, after map[string]string, cfg Config) []diagnostic {
	tmp, err := os.MkdirTemp("", "gala-fix-inout-")
	if err != nil {
		return []diagnostic{{Diagnostic: galaerr.Diagnostic{Message: err.Error()}}}
	}
	defer os.RemoveAll(tmp)
	var paths []string
	for _, f := range files {
		src := f.src
		if after != nil {
			src = after[f.path]
		}
		path := filepath.Join(tmp, filepath.Base(f.path))
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return []diagnostic{{Diagnostic: galaerr.Diagnostic{Message: err.Error()}}}
		}
		paths = append(paths, path)
	}
	p := parser.NewAntlrGalaParser()
	a := analyzer.NewBatchAnalyzer(p, cfg.SearchPaths, cfg.ProjectRoot)
	var out []diagnostic
	for i, path := range paths {
		var siblings []string
		for j, other := range paths {
			if j != i {
				siblings = append(siblings, other)
			}
		}
		a.SetPackageFiles(siblings)
		src, _ := os.ReadFile(path)
		t := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
		if _, err := t.Transpile(string(src), path); err != nil {
			for _, d := range galaerr.Diagnostics(err) {
				out = append(out, diagnostic{file: files[i].path, Diagnostic: d})
			}
		}
	}
	return out
}

// exclude excludes the functions the errors diags of the rewrite after
// blame, and reports whether it excluded any. An error on a line that calls
// a converted function blames the callee (a call site the plan cannot see);
// else an error inside a converted function blames that function.
func (r *Result) exclude(diags []diagnostic, after map[string]string) bool {
	excluded := false
	blame := func(name string, d diagnostic) {
		if _, ok := r.Excluded[name]; !ok {
			r.Excluded[name] = "the compiler rejects the rewrite: " + d.String()
			excluded = true
		}
	}
	for _, d := range diags {
		if d.Line <= 0 {
			continue
		}
		line := lineText(after[d.file], d.Line)
		var hit bool
		for _, f := range r.Plan.Funcs {
			if f.Converted() && !r.inside(f, d) && callsName(line, f.Name) {
				blame(f.Name, d)
				hit = true
			}
		}
		if hit {
			continue
		}
		for _, f := range r.Plan.Funcs {
			if f.Converted() && r.inside(f, d) {
				blame(f.Name, d)
			}
		}
	}
	return excluded
}

// inside reports whether diagnostic d lies in f's declaration.
func (r *Result) inside(f ownership.AdoptFunc, d diagnostic) bool {
	if f.File != d.file {
		return false
	}
	src := r.sources[f.File]
	first := strings.Count(src[:f.SigStart], "\n") + 1
	last := strings.Count(src[:f.End], "\n") + 1
	return d.Line >= first && d.Line <= last
}

// callsName reports whether line calls the function or method name
// (`Type.method` calls method).
func callsName(line, name string) bool {
	name = name[strings.LastIndex(name, ".")+1:]
	return regexp.MustCompile(`(^|[^\w])` + regexp.QuoteMeta(name) + `\s*[\[(]`).MatchString(line)
}

// lineText is line n (1-based) of src.
func lineText(src string, n int) string {
	lines := strings.Split(src, "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	return lines[n-1]
}

// Write writes the rewritten files, keeping a leading byte order mark.
func (r *Result) Write() error {
	for _, c := range r.Changes {
		raw, err := os.ReadFile(c.Path)
		if err != nil {
			return err
		}
		out := c.After
		if text := string(raw); galaerr.StripBOM(text) != text {
			out = text[:len(text)-len(galaerr.StripBOM(text))] + out
		}
		info, err := os.Stat(c.Path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(c.Path, []byte(out), info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// Diff is a unified diff of the rewrite, with paths relative to base. The
// rewrite never adds or removes a line, so each hunk is one changed line.
func (r *Result) Diff(base string) string {
	var b strings.Builder
	for _, c := range r.Changes {
		rel := c.Path
		if base != "" {
			if p, err := filepath.Rel(base, c.Path); err == nil && !strings.HasPrefix(p, "..") {
				rel = p
			}
		}
		rel = filepath.ToSlash(rel)
		fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", rel, rel)
		before, after := strings.Split(c.Before, "\n"), strings.Split(c.After, "\n")
		for i := range before {
			if i < len(after) && before[i] != after[i] {
				fmt.Fprintf(&b, "@@ -%d +%d @@\n-%s\n+%s\n", i+1, i+1, before[i], after[i])
			}
		}
	}
	return b.String()
}

// Signature is a converted function's header before and after the rewrite.
type Signature struct {
	Func          ownership.AdoptFunc
	Before, After string
}

// Signatures are the headers of the functions the rewrite converts, public
// ones only when public.
func (r *Result) Signatures(public bool) []Signature {
	var out []Signature
	for _, f := range r.Plan.Funcs {
		if !f.Converted() || public && !f.Public {
			continue
		}
		src := r.sources[f.File]
		var edits []ownership.Edit
		for _, e := range r.Plan.Edits {
			if e.File == f.File && e.Start >= f.SigStart && e.End <= f.SigEnd {
				edits = append(edits, e)
			}
		}
		header := apply([]sourceFile{{path: f.File, src: src[:f.SigEnd]}}, edits)[f.File]
		out = append(out, Signature{Func: f, Before: oneLine(src[f.SigStart:f.SigEnd]), After: oneLine(header[f.SigStart:])})
	}
	return out
}

// oneLine joins a multi-line header's lines with single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
