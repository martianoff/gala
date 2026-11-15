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
	"errors"
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
	bom           string // the file's leading byte order mark, if any, which Write keeps
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
	bom  string
}

// Package rewrites the packages in dir: its .gala files, tests excluded,
// grouped into packages as the compiler groups them. It reads them and
// computes each rewrite; it writes nothing (see Write).
func Package(dir string, cfg Config) ([]*Result, error) {
	paths, err := packageFiles(dir)
	if err != nil {
		return nil, err
	}
	// Group the files as the compiler does (analyzer.PackageSiblings): a
	// directory of standalone programs, such as examples/, is one package
	// per file.
	seen := map[string]bool{}
	var out []*Result
	var errs []error
	for _, path := range paths {
		if seen[path] {
			continue
		}
		src, _ := analyzer.ReadSource(path)
		group := []string{path}
		for _, s := range analyzer.PackageSiblings(path, src, analyzer.ReadSource) {
			if !strings.HasSuffix(s, "_test.gala") {
				group = append(group, s)
			}
		}
		sort.Strings(group)
		for _, g := range group {
			seen[g] = true
		}
		res, err := Files(group, cfg)
		switch {
		case err != nil:
			errs = append(errs, err)
		case res != nil:
			out = append(out, res)
		}
	}
	return out, errors.Join(errs...)
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
		files = append(files, sourceFile{path: path, src: text, bom: string(raw[:len(raw)-len(text)])})
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
		after, err := apply(files, res.Plan.Edits)
		if err != nil {
			return nil, err
		}
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

// apply is each file's source with the edits applied, by path. The edits
// must be sorted and must not overlap.
func apply(files []sourceFile, edits []ownership.Edit) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range files {
		var b strings.Builder
		at := 0
		for _, e := range edits {
			if e.File != f.path {
				continue
			}
			if e.Start < at || e.End < e.Start || e.End > len(f.src) {
				return nil, fmt.Errorf("%s: overlapping or out-of-range edit at byte %d: the plan is wrong", f.path, e.Start)
			}
			b.WriteString(f.src[at:e.Start])
			b.WriteString(e.Text)
			at = e.End
		}
		b.WriteString(f.src[at:])
		out[f.path] = b.String()
	}
	return out, nil
}

func changes(files []sourceFile, after map[string]string) []FileChange {
	var out []FileChange
	for _, f := range files {
		if after[f.path] != f.src {
			out = append(out, FileChange{Path: f.path, Before: f.src, After: after[f.path], bom: f.bom})
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
	var paths, srcs []string
	for _, f := range files {
		src := f.src
		if after != nil {
			src = after[f.path]
		}
		path := filepath.Join(tmp, filepath.Base(f.path))
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return []diagnostic{{Diagnostic: galaerr.Diagnostic{Message: err.Error()}}}
		}
		paths, srcs = append(paths, path), append(srcs, src)
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
		t := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
		if _, err := t.Transpile(srcs[i], path); err != nil {
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
	// The converted functions: the lines they span, and a pattern matching a
	// call of them.
	type span struct {
		f           ownership.AdoptFunc
		first, last int
		call        *regexp.Regexp
	}
	var spans []span
	for _, f := range r.Plan.Funcs {
		if !f.Converted() {
			continue
		}
		src := r.sources[f.File]
		name := f.Name[strings.LastIndex(f.Name, ".")+1:]
		spans = append(spans, span{f: f,
			first: strings.Count(src[:f.SigStart], "\n") + 1, last: strings.Count(src[:f.End], "\n") + 1,
			call: regexp.MustCompile(`(^|[^\w])` + regexp.QuoteMeta(name) + `\s*[\[(]`)})
	}
	lines := map[string][]string{}
	for _, d := range diags {
		if d.Line <= 0 {
			continue
		}
		if lines[d.file] == nil {
			lines[d.file] = strings.Split(after[d.file], "\n")
		}
		var line string
		if d.Line <= len(lines[d.file]) {
			line = lines[d.file][d.Line-1]
		}
		inside := func(s span) bool { return s.f.File == d.file && d.Line >= s.first && d.Line <= s.last }
		var hit bool
		for _, s := range spans {
			if !inside(s) && s.call.MatchString(line) {
				blame(s.f.Name, d)
				hit = true
			}
		}
		if hit {
			continue
		}
		for _, s := range spans {
			if inside(s) {
				blame(s.f.Name, d)
			}
		}
	}
	return excluded
}

// Write writes the rewritten files, keeping a leading byte order mark. It
// writes every file beside its original first and renames them over the
// originals only once all are written, so a failed write leaves the package
// as it was.
func (r *Result) Write() error {
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()
	for _, c := range r.Changes {
		info, err := os.Stat(c.Path)
		if err != nil {
			return err
		}
		tmp := c.Path + ".inoutfix"
		if err := os.WriteFile(tmp, []byte(c.bom+c.After), info.Mode()); err != nil {
			return err
		}
		temps = append(temps, tmp)
	}
	for i, c := range r.Changes {
		if err := os.Rename(temps[i], c.Path); err != nil {
			return err
		}
	}
	return nil
}

// DisplayPath is path relative to base, with forward slashes, when it lies
// under base; else path as it is.
func DisplayPath(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// Diff is a unified diff of the rewrite, with paths relative to base. No
// edit spans a line break, so the rewrite keeps every line where it was and
// each hunk is one changed line.
func (r *Result) Diff(base string) string {
	var b strings.Builder
	for _, c := range r.Changes {
		rel := DisplayPath(base, c.Path)
		fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", rel, rel)
		before, after := strings.Split(c.Before, "\n"), strings.Split(c.After, "\n")
		if len(before) != len(after) {
			panic("inoutfix: a rewrite moved a line of " + rel)
		}
		for i := range before {
			if before[i] != after[i] {
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
		// The plan's edits applied already, so these cannot fail.
		headers, _ := apply([]sourceFile{{path: f.File, src: src[:f.SigEnd]}}, edits)
		header := headers[f.File]
		out = append(out, Signature{Func: f, Before: oneLine(src[f.SigStart:f.SigEnd]), After: oneLine(header[f.SigStart:])})
	}
	return out
}

// oneLine joins a multi-line header's lines with single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
