package doccheck

import (
	"errors"
	"flag"
	"fmt"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"

	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestDocSnippetsCompile verifies the ```gala code blocks in the language
// documentation and on the website.
//
// Nothing used to check them, and it showed: the reference manual's own
// `divmod` example and a §Maps snippet did not compile. Every block now falls
// into exactly one of these classes:
//
//   - A program (it has a `package` clause) must transpile, and the generated
//     Go must parse.
//   - A fragment (no `package` clause) is wrapped in a minimal harness — as
//     top-level declarations, as the body of main, or split between the two —
//     and must pass in at least one of them. Fragments routinely leave their
//     imports to the prose, so when the compiler names the package that
//     declares an undefined symbol, the harness adds that import and retries.
//   - A block that is not self-contained (it refers to names defined in the
//     surrounding prose, elides code with `...`, or sketches syntax) is marked
//     with `<!-- doc-check: fragment -->` on the line before its fence. A
//     marked block must still FAIL every harness: once it compiles, the marker
//     is stale and has to go so the block is checked from then on.
//   - A block that demonstrates a diagnostic is marked
//     `<!-- doc-check: error GALA-Exxxx -->` and must fail with that code.
//     On the error-code pages (docs/errors, website/docs/errors) a block may
//     fail with the page's own code without a marker.
//   - A block that exposes an open transpiler bug is listed in knownBugs
//     below, by name. It must keep failing; when the bug is fixed the entry
//     fails the test and has to be removed.
//
// "Compiles" here means transpiles cleanly to Go that parses; the generated Go
// is not type-checked, which would need every snippet built as a package.
//
// Run with `--test_arg=-mark=<path to your checkout>` to insert the fragment
// marker before every unmarked failing fragment and drop it from marked ones
// that now compile; `--test_arg=-dump=<file>` writes each reported block with
// its errors. Review what -mark marks: a fragment that fails because the docs
// are wrong should be fixed, not marked.
func TestDocSnippetsCompile(t *testing.T) {
	root := repoRoot(t)
	blocks := collectBlocks(t, root)
	require.NotEmpty(t, blocks, "no ```gala blocks found; check the data dependencies of this test")

	results := checkAll(root, blocks)

	var problems []string
	var toMark, toUnmark []block
	var failing []int
	stats := map[string]int{}
	bugHit := map[string]bool{}
	for i, b := range blocks {
		r := results[i]
		loc := fmt.Sprintf("%s:%d", b.file, b.line)
		if bug, ok := matchKnownBug(b); ok {
			bugHit[bug.name] = true
			stats["known bug"]++
			if r.ok {
				problems = append(problems, fmt.Sprintf("%s: known bug %q no longer reproduces — "+
					"remove its knownBugs entry so the block is checked normally", loc, bug.name))
			}
			continue
		}
		switch {
		case b.directive == "fragment":
			stats["marked fragment"]++
			if r.ok {
				problems = append(problems, fmt.Sprintf("%s: marked `doc-check: fragment` but compiles "+
					"(%s) — remove the marker", loc, r.harness))
				toUnmark = append(toUnmark, b)
			}
		case strings.HasPrefix(b.directive, "error "):
			stats["expected error"]++
			want := strings.TrimSpace(strings.TrimPrefix(b.directive, "error "))
			if r.ok {
				problems = append(problems, fmt.Sprintf("%s: expected %s but the block compiles", loc, want))
			} else if !r.hasCode(want) {
				problems = append(problems, fmt.Sprintf("%s: expected %s, got: %s", loc, want, r.summary()))
				failing = append(failing, i)
			}
		case b.directive != "":
			problems = append(problems, fmt.Sprintf("%s: unknown doc-check directive %q", loc, b.directive))
		case r.ok:
			if b.isProgram() {
				stats["program"]++
			} else {
				stats["fragment ("+r.harness+")"]++
			}
		case b.pageCode != "" && r.hasCode(b.pageCode):
			stats["error-page repro"]++
		default:
			if !b.isProgram() {
				toMark = append(toMark, b)
			}
			problems = append(problems, fmt.Sprintf("%s: %s", loc, r.summary()))
			failing = append(failing, i)
		}
	}
	for _, bug := range knownBugs {
		if !bugHit[bug.name] {
			problems = append(problems, fmt.Sprintf("known bug %q matches no block in %s — "+
				"the block moved or changed; update or remove the entry", bug.name, bug.file))
		}
	}

	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-32s %d", k, stats[k])
	}
	t.Logf("%-32s %d", "total", len(blocks))

	if *dumpFlag != "" {
		var sb strings.Builder
		for _, i := range failing {
			b := blocks[i]
			fmt.Fprintf(&sb, "==== %s:%d [%s]\n%s---- %s\n\n", b.file, b.line, b.directive, b.text, results[i].summary())
		}
		require.NoError(t, os.WriteFile(*dumpFlag, []byte(sb.String()), 0o644))
	}
	if *markFlag != "" {
		updateMarkers(t, *markFlag, toMark, toUnmark)
	}
	if len(problems) > 0 {
		t.Errorf("%d documentation code block(s) need attention:\n  %s\n\n"+
			"Fix the snippet, or mark it (see the TestDocSnippetsCompile doc comment).",
			len(problems), strings.Join(problems, "\n  "))
	}
}

var dumpFlag = flag.String("dump", "", "write every block reported as a problem, with its errors, to this file")

var workersFlag = flag.Int("workers", 4, "blocks checked concurrently; each worker owns its own transpiler")

var markFlag = flag.String("mark", "", "repository checkout in which to insert `<!-- doc-check: fragment -->` "+
	"before every unmarked failing fragment, and to remove it from marked fragments that now compile")

// knownBug names a documentation block that is correct GALA but does not
// compile because of an open transpiler bug.
type knownBug struct {
	name string
	file string
	// contains is a line unique to the block within file.
	contains string
	reason   string
}

var knownBugs = []knownBug{}

func matchKnownBug(b block) (knownBug, bool) {
	for _, k := range knownBugs {
		if k.file == b.file && strings.Contains(b.text, k.contains) {
			return k, true
		}
	}
	return knownBug{}, false
}

// docGlobs lists the pages whose ```gala blocks are checked, relative to the
// repository root.
var docGlobs = []string{
	"docs/GALA.MD",
	"docs/EXAMPLES.MD",
	"docs/GALA_BEST_PRACTICES.MD",
	"docs/TYPE_INFERENCE.MD",
	"docs/errors/GALA-E*.md",
	"website/*.md",
	"website/docs/*.md",
	"website/docs/errors/*.md",
	"website/features/*.md",
}

type block struct {
	file          string // slash-separated, relative to the repository root
	line          int    // 1-based line of the opening fence
	indent        string // indentation of the fence, stripped from the content
	text          string
	directive     string // text of a preceding `<!-- doc-check: ... -->` comment
	directiveLine int    // 1-based line of that comment
	pageCode      string // GALA-Exxxx for blocks on an error-code page
}

func (b block) isProgram() bool { return packageClause.MatchString(b.text) }

var (
	fenceOpen     = regexp.MustCompile("^([ \t]*)```gala[ \t]*$")
	directiveLine = regexp.MustCompile(`^[ \t]*<!--[ \t]*doc-check:[ \t]*(.*?)[ \t]*-->[ \t]*$`)
	packageClause = regexp.MustCompile(`(?m)^package\s+\w+`)
	pageCodeName  = regexp.MustCompile(`(?i)^gala-(e\d{4})\.md$`)
	errorCode     = regexp.MustCompile(`GALA-E\d{4}`)
	funcMain      = regexp.MustCompile(`(?m)^func\s+main\s*\(`)
	topLevelStart = regexp.MustCompile(`^(func|type|sealed|struct|interface|import|const|package)\b|^(val|var)\s`)
	importLine    = regexp.MustCompile(`^import\b`)
)

func collectBlocks(t *testing.T, root string) []block {
	t.Helper()
	var files []string
	for _, g := range docGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		require.NoError(t, err)
		files = append(files, matches...)
	}
	sort.Strings(files)

	var blocks []block
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		rel, err := filepath.Rel(root, f)
		require.NoError(t, err)
		rel = filepath.ToSlash(rel)
		pageCode := ""
		if m := pageCodeName.FindStringSubmatch(filepath.Base(f)); m != nil {
			pageCode = "GALA-" + strings.ToUpper(m[1])
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			m := fenceOpen.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			b := block{file: rel, line: i + 1, indent: m[1], pageCode: pageCode}
			for j := i - 1; j >= 0; j-- {
				if strings.TrimSpace(lines[j]) == "" {
					continue
				}
				if d := directiveLine.FindStringSubmatch(lines[j]); d != nil {
					b.directive, b.directiveLine = d[1], j+1
				}
				break
			}
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "```" {
					break
				}
				body = append(body, strings.TrimPrefix(lines[j], b.indent))
			}
			b.text = strings.Join(body, "\n") + "\n"
			blocks = append(blocks, b)
			i = j
		}
	}
	return blocks
}

type result struct {
	ok      bool
	harness string
	errs    []error // one per harness attempted, in order
}

// summary gives each harness's error on its own line, shortened: a MultiError
// leads with a count line, so its first real message is kept too.
func (r result) summary() string {
	if len(r.errs) == 0 {
		return "no harness applied"
	}
	var out []string
	for _, err := range r.errs {
		lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
		if len(lines) > 2 {
			lines = lines[:2]
		}
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		msg := strings.Join(lines, " ")
		if len(msg) > 240 {
			msg = msg[:240] + "…"
		}
		out = append(out, msg)
	}
	return strings.Join(out, "\n      ")
}

// hasCode reports whether some harness failed with code. A demonstration
// usually compiles only in one shape — `xs.Map(x => x * 2)` is a statement,
// so only the main-body harness reaches the diagnostic it shows.
func (r result) hasCode(code string) bool {
	for _, err := range r.errs {
		if errCode(err) == code {
			return true
		}
	}
	return false
}

func errCode(err error) string {
	var se *galaerr.SemanticError
	if errors.As(err, &se) && se.Code != "" {
		return string(se.Code)
	}
	return errorCode.FindString(err.Error())
}

// harnesses returns the candidate compilation units for a block, most direct
// first.
func harnesses(b block) []struct{ name, src string } {
	if b.isProgram() {
		return []struct{ name, src string }{{"program", b.text}}
	}
	imports, rest := splitImports(b.text)
	header := "package main\n\n" + imports
	mainStub := ""
	if !funcMain.MatchString(rest) {
		mainStub = "\nfunc main() {}\n"
	}
	decls, stmts := splitTopLevel(rest)
	out := []struct{ name, src string }{
		{"top-level", header + rest + mainStub},
		{"main body", header + "func main() {\n" + rest + "}\n"},
	}
	if decls != "" && stmts != "" && !funcMain.MatchString(decls) {
		out = append(out, struct{ name, src string }{"split", header + decls + "\nfunc main() {\n" + stmts + "}\n"})
	}
	return out
}

// splitImports moves the block's import lines (single-line and grouped) ahead
// of everything else.
func splitImports(text string) (string, string) {
	var imports, rest []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if !importLine.MatchString(l) {
			rest = append(rest, l)
			continue
		}
		imports = append(imports, l)
		if strings.HasSuffix(strings.TrimSpace(l), "(") {
			for i++; i < len(lines); i++ {
				imports = append(imports, lines[i])
				if strings.TrimSpace(lines[i]) == ")" {
					break
				}
			}
		}
	}
	if len(imports) == 0 {
		return "", text
	}
	return strings.Join(imports, "\n") + "\n\n", strings.Join(rest, "\n")
}

// splitTopLevel separates column-0 declarations (with their indented bodies)
// from the statements around them.
func splitTopLevel(text string) (string, string) {
	var decls, stmts []string
	inDecl := false
	for _, l := range strings.Split(text, "\n") {
		switch {
		case l == "":
			if inDecl {
				decls = append(decls, l)
			} else {
				stmts = append(stmts, l)
			}
		case l[0] != ' ' && l[0] != '\t' && l[0] != '}' && l[0] != ')':
			inDecl = topLevelStart.MatchString(l) && !strings.HasPrefix(l, "val ") && !strings.HasPrefix(l, "var ")
			if inDecl {
				decls = append(decls, l)
			} else {
				stmts = append(stmts, l)
			}
		default:
			if inDecl {
				decls = append(decls, l)
			} else {
				stmts = append(stmts, l)
			}
		}
	}
	return strings.TrimSpace(strings.Join(decls, "\n")), strings.TrimSpace(strings.Join(stmts, "\n")) + "\n"
}

func checkAll(root string, blocks []block) []result {
	results := make([]result, len(blocks))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := *workersFlag
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = check(root, blocks[i])
			}
		}()
	}
	for i := range blocks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func check(root string, b block) (r result) {
	for _, h := range harnesses(b) {
		var err error
		if b.isProgram() {
			err = compile(root, h.src)
		} else {
			err = compileAddingImports(root, h.src, 3)
		}
		if err == nil {
			return result{ok: true, harness: h.name}
		}
		r.errs = append(r.errs, fmt.Errorf("[%s] %w", h.name, err))
	}
	return r
}

var (
	undefinedName   = regexp.MustCompile(`undefined: (\w+)`)
	notImportedHint = regexp.MustCompile(`'(\w+)' is not imported in this file`)
	galaPackageHint = regexp.MustCompile(`declared in (?:the GALA package|these GALA packages, none of which this file imports:) "([^"]+)"`)
)

// goStdPackages are the Go standard-library packages doc fragments use
// without importing, keyed by the name they are referenced by.
var goStdPackages = map[string]string{
	"bufio": "bufio", "bytes": "bytes", "context": "context", "errors": "errors",
	"filepath": "path/filepath", "fmt": "fmt", "http": "net/http", "io": "io",
	"math": "math", "os": "os", "rand": "math/rand", "regexp": "regexp",
	"runtime": "runtime", "sort": "sort", "strconv": "strconv", "strings": "strings",
	"sync": "sync", "time": "time", "unicode": "unicode", "utf8": "unicode/utf8",
}

// compileAddingImports compiles a harnessed fragment. Fragments routinely
// leave their imports to the surrounding prose, so when the compiler names
// the package(s) declaring an undefined symbol, each is tried as an added
// import, up to depth imports deep. A program must import what it uses and
// never comes through here.
func compileAddingImports(root, src string, depth int) error {
	err := compile(root, src)
	if err == nil || depth == 0 {
		return err
	}
	// Report the error from past the added imports, when there is one: it is
	// the one that says what is actually wrong with the fragment.
	for _, imp := range missingImports(err) {
		if strings.Contains(src, imp) {
			continue
		}
		withImport := strings.Replace(src, "package main\n", "package main\n\n"+imp+"\n", 1)
		deeper := compileAddingImports(root, withImport, depth-1)
		if deeper == nil {
			return nil
		}
		err = deeper
	}
	return err
}

// galaStdPackages are the GALA standard-library packages doc fragments
// reference by qualified name without importing.
var galaStdPackages = map[string]bool{
	"collection_immutable": true, "collection_mutable": true, "concurrent": true,
	"fs": true, "go_interop": true, "io": true, "json": true, "lazy": true,
	"regex": true, "stream": true, "subprocess": true, "time_utils": true,
	"validation": true, "yaml": true,
}

// missingImports returns the import lines that could define the undefined
// symbol err reports: the GALA packages the compiler's hint names, or the Go
// standard-library package of that name.
func missingImports(err error) []string {
	msg := err.Error()
	if m := notImportedHint.FindStringSubmatch(msg); m != nil {
		return []string{`import . "martianoff/gala/` + m[1] + `"`}
	}
	if m := galaPackageHint.FindStringSubmatch(msg); m != nil {
		var out []string
		for _, q := range quoted.FindAllStringSubmatch(msg[strings.Index(msg, m[0]):], -1) {
			if strings.Contains(q[1], "/") {
				out = append(out, `import . "`+q[1]+`"`)
			}
		}
		return out
	}
	if m := undefinedName.FindStringSubmatch(msg); m != nil {
		// A name like `io` or `strings` may be either; try both.
		var out []string
		if galaStdPackages[m[1]] {
			out = append(out, `import "martianoff/gala/`+m[1]+`"`)
		}
		if path, ok := goStdPackages[m[1]]; ok {
			out = append(out, `import "`+path+`"`)
		}
		return out
	}
	return nil
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

// compile transpiles src and parses the generated Go.
func compile(root, src string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("transpiler panic: %v", p)
		}
	}()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{root})
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	out, err := transpiler.NewGalaToGoTranspiler(p, a, tr, g).Transpile(src, "main.gala")
	if err != nil {
		return err
	}
	if _, err := goparser.ParseFile(token.NewFileSet(), "main.go", out, 0); err != nil {
		return fmt.Errorf("generated Go does not parse: %w", err)
	}
	return nil
}

// updateMarkers inserts the fragment directive before each block in mark and
// deletes it from each block in unmark.
func updateMarkers(t *testing.T, root string, mark, unmark []block) {
	t.Helper()
	type edit struct {
		b      block
		insert bool
	}
	byFile := map[string][]edit{}
	for _, b := range mark {
		byFile[b.file] = append(byFile[b.file], edit{b, true})
	}
	for _, b := range unmark {
		byFile[b.file] = append(byFile[b.file], edit{b, false})
	}
	for file, edits := range byFile {
		path := filepath.Join(root, filepath.FromSlash(file))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		nl := "\n"
		if strings.Contains(string(data), "\r\n") {
			nl = "\r\n"
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		// Edit bottom-up so earlier line numbers stay valid.
		sort.Slice(edits, func(i, j int) bool { return edits[i].b.line > edits[j].b.line })
		for _, e := range edits {
			if !e.insert {
				idx := e.b.directiveLine - 1
				lines = append(lines[:idx], lines[idx+1:]...)
				continue
			}
			idx := e.b.line - 1
			marker := e.b.indent + "<!-- doc-check: fragment -->"
			ins := []string{marker}
			if idx > 0 && strings.TrimSpace(lines[idx-1]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[idx-1]), "<") {
				// Separate the marker from the preceding paragraph so markdown
				// does not fold it into it.
				ins = []string{"", marker}
			}
			lines = append(lines[:idx], append(ins, lines[idx:]...)...)
		}
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, nl)), 0o644))
		t.Logf("updated %d fragment marker(s) in %s", len(edits), file)
	}
}

// repoRoot finds the repository root: under Bazel through the runfiles tree,
// otherwise by walking up to go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	if p, err := bazel.Runfile("std/option.gala"); err == nil {
		return filepath.Dir(filepath.Dir(p))
	}
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "cannot locate the repository root")
		dir = parent
	}
}
