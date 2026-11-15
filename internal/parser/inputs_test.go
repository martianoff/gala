package parser

import (
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/parser/native"
)

// The inputs the parser's tests share, and how they break and compare them.

// parseGalop parses input as a source file, for a test that asserts on what
// the parse reports.
func parseGalop(t *testing.T, input string) *Parsed {
	t.Helper()
	return NewAntlrGalaParser().ParseFile(input)
}

// gatherSources returns the .gala files staged as data of parser_test; under
// plain `go test` (tools/fuzz.sh), which stages no runfiles, the repository's
// own .gala files.
func gatherSources(t testing.TB) []string {
	t.Helper()
	var paths []string
	if os.Getenv("TEST_SRCDIR") == "" {
		paths = repoSources(t)
	} else {
		files, err := bazel.ListRunfiles()
		if err != nil {
			t.Fatalf("cannot list the runfiles: %v", err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.ShortPath, ".gala") {
				paths = append(paths, f.Path)
			}
		}
	}
	sort.Strings(paths)
	if len(paths) < 200 {
		t.Fatalf("found %d .gala files in the runfiles; the sources are not staged", len(paths))
	}
	return paths
}

// repoSources is every .gala file of the repository the test runs in, for
// gatherSources under plain `go test`: the files under the module root, but
// for hidden directories (the IDE's, agents' worktrees, vendored caches) and
// Bazel's output links.
func repoSources(tb testing.TB) []string {
	tb.Helper()
	root, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			tb.Fatal("cannot find the module root (go.mod)")
		}
		root = parent
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-") || name == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(name, ".gala") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return paths
}

// sourceTexts is the text of every staged source (see gatherSources).
func sourceTexts(tb testing.TB) []string {
	tb.Helper()
	var texts []string
	for _, path := range gatherSources(tb) {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		texts = append(texts, string(data))
	}
	return texts
}

// positionInputs exercise what the lexer counts positions over: non-ASCII
// text, CRLF line ends and comments that span lines.
var positionInputs = []string{
	"package main\n\n/* a block comment\n   over three lines, with ünïcödé ✓\n*/\nfunc main() {\n    val s = \"héllo wörld ✓ 日本\" // trailing — comment\n    Println(s)\n}\n",
	"package main\r\n\r\n/* block\r\n   comment */\r\nfunc main() {\r\n    val s = \"ä\"\r\n    Println(s + s\"${s}é\")\r\n}\r\n",
	"package main\n\n// ünïcödé line comment\nfunc f(a int) int = a /* inline\n multi-line ✓ */ + 1\n",
}

// expressionAtInputs are expressions placed by ParseExpressionCheckedAt, among
// them ones that span lines, where the starting column applies to the first
// line only (a multi-line default value).
var expressionAtInputs = []string{
	"a + b",
	"Array(1,\n    2,\n    3)",
	"Config(\n    name = \"ünïcödé\",\n    retries = 3,\n)",
	"xs.Map((x) => x *\n    2)",
	"s\"${a}\\n${b}\"",
}

// breakSource drops a few bytes of src somewhere: mostly a syntax error, at
// times still valid code.
func breakSource(r *rand.Rand, src string) string {
	i := r.Intn(len(src) + 1)
	j := min(len(src), i+1+r.Intn(8))
	return src[:i] + src[j:]
}

// interpolation matches the ${...} parts of interpolated strings, whose text
// the transpiler re-parses as expressions.
var interpolation = regexp.MustCompile(`\$\{([^{}"]+)\}`)

// diffAt shows where want and got first differ: the line number, and each
// side's text around the first differing byte of that line (tree text is one
// long line, so a whole line can be too long to read).
func diffAt(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	// i is at most either length; show from up to 120 bytes before it, on its
	// line, to up to 240 bytes after it, on the same line.
	from := max(strings.LastIndexByte(want[:i], '\n')+1, i-120)
	window := func(s string) string {
		end := min(len(s), i+240)
		if nl := strings.IndexByte(s[i:end], '\n'); nl >= 0 {
			end = i + nl
		}
		return fmt.Sprintf("%q", s[from:end])
	}
	line := strings.Count(want[:i], "\n") + 1
	return fmt.Sprintf("first difference at line %d, byte %d\n  want: %s\n  got:  %s", line, i, window(want), window(got))
}

// mutation is a way the parser golden breaks a source file: it returns src
// changed, given its tokens, or "" when it does not apply.
type mutation struct {
	name string
	run  func(r *rand.Rand, src string, toks []native.GalaToken) string
}

// mutationTokens is what the insert mutation puts between two tokens: the
// brackets and separators recovery conjures and skips most, and the shapes
// the coded hints look for (a Go slice type, an arrow, a string where a
// struct tag would go).
var mutationTokens = []string{
	"(", ")", "{", "}", "[", "]", ",", "=", "=>", ".", ":", "val", "case", "if", "else", "match",
	"[]byte", "map[string]int", `"json:\"x\""`, "`raw`", "+", "1", "x", "\n",
}

// strayCharacters are what the stray-character mutations insert: characters
// GALA's lexer has no token for, and ones that open a literal or a comment
// that may never close.
var strayCharacters = []string{"$", "#", "\\", "\"", "'", "`", "/*", "@", "?", "~"}

// pickIndex is one of the indexes i in [0, n) where at(i) holds, drawn by r,
// or false when there is none.
func pickIndex(r *rand.Rand, n int, at func(i int) bool) (int, bool) {
	var found []int
	for i := range n {
		if at(i) {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		return 0, false
	}
	return found[r.Intn(len(found))], true
}

// mutations are the ways the parser golden breaks a file: token edits,
// which keep the rest of the file lexing as before, byte drops and stray
// characters, which can break a literal or comment open (lexical errors), a
// cut multi-byte character (invalid UTF-8), and three that aim at the coded
// hints: a type argument made a Go slice type (GALA-E0040), a lambda's
// parameter unparenthesized (GALA-E0042), and a tag after a struct field
// (GALA-E0085).
var mutations = []mutation{
	{"delete a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + src[t.End:]
	}},
	{"duplicate a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.End] + " " + t.Text(src) + src[t.End:]
	}},
	{"swap two tokens", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		if len(toks) < 2 {
			return ""
		}
		i := r.Intn(len(toks) - 1)
		a, b := toks[i], toks[i+1]
		return src[:a.Start] + b.Text(src) + src[a.End:b.Start] + a.Text(src) + src[b.End:]
	}},
	{"insert a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + mutationTokens[r.Intn(len(mutationTokens))] + " " + src[t.Start:]
	}},
	{"drop bytes", func(r *rand.Rand, src string, _ []native.GalaToken) string {
		return breakSource(r, src)
	}},
	{"stray character", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		at := int(t.Start) + r.Intn(int(t.End-t.Start)+1)
		return src[:at] + strayCharacters[r.Intn(len(strayCharacters))] + src[at:]
	}},
	{"stray character, then a token edit", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// Two edits on one line: the lexical error just after a syntax error,
		// where the order they are reported in depends on how far the parser
		// has read.
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + mutationTokens[r.Intn(len(mutationTokens))] + " " + t.Text(src) +
			strayCharacters[r.Intn(len(strayCharacters))] + src[t.End:]
	}},
	{"cut a multi-byte character, then a token edit", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// Invalid UTF-8, which the lexer reads as U+FFFD, with a syntax error
		// after it: the recovered tree's offsets index the decoded text.
		i, ok := pickIndex(r, len(src), func(i int) bool { return src[i] >= 0xC0 })
		if !ok {
			return ""
		}
		t := toks[len(toks)-1-r.Intn(min(len(toks), 20))]
		if int(t.Start) <= i+1 {
			return src[:i] + src[i+1:]
		}
		return src[:i] + src[i+1:t.Start] + "( " + src[t.Start:]
	}},
	{"Go slice type argument", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// A '[' followed by an identifier: a type argument list.
		i, ok := pickIndex(r, len(toks)-1, func(i int) bool {
			return toks[i].Text(src) == "[" && toks[i+1].Type == native.GalaIDENTIFIER
		})
		if !ok {
			return ""
		}
		t := toks[i+1]
		return src[:t.Start] + "[]byte" + src[t.End:]
	}},
	{"bare lambda parameter", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// `(x) =>`: drop the parentheses.
		i, ok := pickIndex(r, len(toks)-3, func(i int) bool {
			return toks[i].Text(src) == "(" && toks[i+1].Type == native.GalaIDENTIFIER &&
				toks[i+2].Text(src) == ")" && toks[i+3].Text(src) == "=>"
		})
		if !ok {
			return ""
		}
		return src[:toks[i].Start] + toks[i+1].Text(src) + src[toks[i+2].End:]
	}},
	{"struct tag", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// After an identifier ending a line inside a struct-like declaration:
		// a field's type.
		i, ok := pickIndex(r, len(toks)-1, func(i int) bool {
			return toks[i].Type == native.GalaIDENTIFIER && toks[i+1].Line > toks[i].Line
		})
		if !ok {
			return ""
		}
		t := toks[i]
		return src[:t.End] + ` "json:\"f\""` + src[t.End:]
	}},
}
