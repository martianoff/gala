package parser

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/testcorpus"
)

// The parser golden holds what GALA makes of galop's lexer and parser to what
// it made of them when they were last checked against ANTLR's, token for
// token: galop's repository holds its lexer and parser to ANTLR's, and this
// golden holds what GALA adds on top of them (the line-break re-typing, the
// doc comments, the blank-line check, the source-text check, the coded hints
// GALA-E0040, GALA-E0042 and GALA-E0085 and their cascade suppression, the
// positions of an expression parsed at an offset, the native-tree helpers)
// and the parts of galop's output GALA reads (tokens, errors, clean and
// error-recovered trees).
//
// The cases are the files of the frozen corpus (internal/testcorpus), each
// lexed, parsed and broken several ways (goldenFile), its expressions parsed
// at their positions (goldenExpressions), and fixed inputs aimed at the edge
// cases (goldenFixedCases). Each line of the golden is `key<TAB>input<TAB>
// result`, digests of a case's input and of everything the parser gives for
// it (see digestgolden). To regenerate it, run the test with
// GALA_UPDATE_PARSER_GOLDEN set to its path in the source tree:
//
//	bazel test //internal/parser:parser_test --test_filter=TestParserGolden \
//	  --test_env=GALA_UPDATE_PARSER_GOLDEN=$PWD/internal/parser/testdata/parser.golden
//
// A change of the golden is a change of the diagnostics, doc comments or
// trees GALA reads: review it as one, and before accepting it check that
// galop's release notes (or its ANTLR parity tests) account for it.
const parserGolden = "internal/parser/testdata/parser.golden"

// TestParserGolden holds the parser's output on every case to the golden.
func TestParserGolden(t *testing.T) {
	g := digestgolden.Open(t, parserGolden, "GALA_UPDATE_PARSER_GOLDEN")
	check := func(key, input, result string) {
		g.Check(key, digestgolden.Digest(input), digestgolden.Digest(result), nil)
	}
	for _, c := range goldenFixedCases() {
		check(c.key, c.input, c.render())
	}
	files := testcorpus.Files()
	codes := map[galaerr.ErrorCode]int{}
	for _, name := range testcorpus.GalaNames() {
		src := files[name]
		check("file/"+name, src, goldenFile(src, codes))
		check("expr/"+name, src, goldenExpressions(src))
	}
	// The corpus's mutants reach each coded hint; were one never reached,
	// the golden would not hold it.
	for _, c := range []galaerr.ErrorCode{galaerr.CodeGoTypeInExpression, galaerr.CodeBareLambdaParam, galaerr.CodeStructTag} {
		if codes[c] == 0 {
			t.Errorf("no mutant of the corpus gave %s", c)
		}
	}
	g.Finish("The parser's output on the frozen corpus and on fixed inputs: see TestParserGolden.")
	t.Logf("%d cases compared", g.Compared)
}

// goldenCase is one case of the golden: its key, its input as the digest
// reads it, and what the parser gives for it.
type goldenCase struct {
	key, input string
	render     func() string
}

// goldenFixedCases are the fixed inputs: source files parsed, source files
// lexed, and expressions lexed and parsed at offsets.
func goldenFixedCases() []goldenCase {
	var cases []goldenCase
	for i, src := range goldenFixedFiles() {
		cases = append(cases, goldenCase{fmt.Sprintf("fixed/parse/%03d", i), src, func() string {
			return renderFileParse(src)
		}})
	}
	for i, src := range goldenFixedLexInputs() {
		cases = append(cases, goldenCase{fmt.Sprintf("fixed/lex/%03d", i), src, func() string {
			return renderLex(galaerr.StripBOM(src), 0, 0) + renderVisited(src)
		}})
	}
	k := 0
	for _, src := range goldenFixedExpressions() {
		for _, at := range goldenOffsets {
			line, col := at[0], at[1]
			cases = append(cases, goldenCase{fmt.Sprintf("fixed/expr/%03d", k), fmt.Sprintf("%d:%d %s", line, col, src), func() string {
				return renderLex(src, line, col) + renderExpressionAt(src, line, col) + renderExpressionTree(src)
			}})
			k++
		}
	}
	return cases
}

// goldenFixedFiles are the fixed source files: the inputs of the other
// tests, and goldenFileInputs.
func goldenFixedFiles() []string {
	return slices.Concat(positionInputs, brokenInputs, emptyLineInputs, nativeHelperInputs,
		lineBreakInputs(), goldenFileInputs)
}

// goldenFixedLexInputs are the fixed source files and goldenLexInputs.
func goldenFixedLexInputs() []string { return slices.Concat(goldenFixedFiles(), goldenLexInputs) }

// goldenFixedExpressions are the fixed expressions, each placed at every
// one of goldenOffsets.
func goldenFixedExpressions() []string {
	return slices.Concat(expressionAtInputs, goldenExpressionInputs)
}

// goldenOffsets are where the fixed expressions are placed in a file: line
// and column, line <= 0 for none.
var goldenOffsets = [][2]int{{0, 0}, {0, 7}, {-3, 5}, {1, 0}, {2, 3}, {4, 9}, {12, 30}, {400, 2}}

// goldenFileInputs are source files around what GALA reads off galop's
// output: invalid UTF-8 (read as U+FFFD), conjured tokens and the EOF token
// in a recovered tree, an illegal character, unterminated literals, the
// re-typed line breaks, the coded hints and a missing blank line.
var goldenFileInputs = []string{
	"package main\n\n// Two placeho\x94 sum via FoldLeft.\nfunc f() int = 1\n",
	"package main\n\n// A dash \xe2\x80 cut.\nval s = \"a\x94b\"\nval = \n",
	"package main\n\nval a\x94b = 1\nval ✓\x94 = 2\n",
	"package main\n\n// cut at the end \xe2\x82",
	"\xef\xbb\xbfpackage main\n\n// a \x94\nval x = 1 // (\n",
	"\xef\xbb\xbfpackage main\n\nfunc main() {\n    Println(1)\n}\n",
	"package main\n\nval s = \"a\uFEFFb\" + \"\x00\"\n",
	"main\n\nval x = 1\n",
	"packe main\n\nval x = 1\n",
	"packag\n\nval x = 1\n",
	"package\n",
	"package",
	"s\"${a}\"",
	"val\n",
	"package main\n\nfunc main() {\n    Println(s\"v=${1 +}\")\n}\n",
	"package main\n\nval x = 'ab\n",
	"package main\n\nval x = \"unterminated\n",
	"package main\n\n/* unterminated",
	"package main\n\nval x = f\n(1)\nval y = a\n*b\nval z = c\n&d\n",
	"package main\r\n\r\nval s = \"ä\"\r\n",
	"package main\n\nfunc f() {\n    val x = foo[[]byte](1)\n    Println(x)\n}\n",
	"package main\n\nval m = EmptyHashMap[string, []byte]()\nval n = Make[map[string]int](1)\n",
	"package main\n\nfunc main() {\n    val f = x => x + 1\n}\n",
	"package main\n\nfunc main() {\n    val f = x /* note */ => x + 1\n    val g = y => y\n}\n",
	"package main\n\nstruct P(Name string \"json:\\\"name\\\"\", Age int `json:\"age\"`)\n",
	"package main\n\ntype T struct {\n    Name string `json:\"name\"`\n    Age int \"age\"\n}\n",
	"package main\n\nsealed type S {\n    case A(x int \"tag\")\n}\n",
	"package main\n\nval f func int = g\n",
	"package main\n\nfunc f() {\n    val x = 1\n    (\n}\n",
	"package main\n\nfunc f() {\n    val x = 1\n    *}\n",
	"package main\n// Doc of x.\nval x = 1\n",
	"package main\n\nimport \"fmt\"\nfunc f() = fmt.Println(1)\n",
	"package main\n\nimport (\n    \"fmt\"\n)\n// Doc of f.\nfunc f() = fmt.Println(\"ünïcödé\")\n",
	"package main\n\n/* Deprecated. */ // Use Bar instead.\nfunc Foo() int = 1\n\n//go:noinline\n// +build ignore\n// Bar is documented.\nfunc Bar() int = 2\n",
}

// goldenLexInputs are lexed only: comments and literals next to a re-typed
// token, and invalid UTF-8 in each kind of token.
var goldenLexInputs = []string{
	lineStartInput("Println(\"zero\") /* a\nb */ (1, 2)"),
	lineStartInput("val a = b\n* p"),
	lineStartInput("val a = b\n*/* c */p"),
	lineStartInput("val a = b\n*//c\np"),
	lineStartInput("val a = b\n*"),
	lineStartInput("if (a) x else y\n*p = 1"),
	"package main\n\n/* surface\x94 a\n   successful */\nval x = 1 // trailing \xff\xfe\n",
	"package main\n\nval s = \"a\x94b\" + `c\xe2\x82d`\n",
	"\xef\xbb\xbfpackage main\n\n// after a BOM \x94\nval x = 1\n",
	"\xef\xbb\xbfpackage main\n\n// a \x94\nval s = \"a\x94b\" // (\n",
}

// goldenExpressionInputs are expressions placed at the offsets: ones that
// span lines, are broken, leave tokens over, or hold invalid UTF-8.
var goldenExpressionInputs = []string{
	"a\n(b)", "x /* c\n */ *y", "\"ünï\" + `\n` + é",
	"a b", "x +", "f(1))", "", "1 @", "f(1)\n(2)", "f(1)(2)",
	"(a int) =>\n    a + 1", "(a, b) => if (a > b) a else b", "x y",
	"'\xe2'", "\"a\xe2b\"", "f(\"\xff\", x)", "\"a\x94b\" + c", "f(x) /* \x94 */ + 1",
	"xs.Map(x => x)", "foo[[]byte](1)", "\xef\xbb\xbfa + b",
}

// sourceRand is a random stream of src's own, so that what is drawn from it
// does not depend on the other cases.
func sourceRand(src string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(src))
	return rand.New(rand.NewSource(int64(h.Sum64())))
}

// corpusLexInputs are the texts the corpus file src is lexed as: as written
// and with CRLF line ends.
func corpusLexInputs(src string) []string {
	return []string{src, strings.ReplaceAll(src, "\n", "\r\n")}
}

// corpusParseInputs are the texts the corpus file src is parsed as: as
// written, with CRLF line ends, with bytes dropped twice, and broken each
// way of mutations.
func corpusParseInputs(src string) []string {
	r := sourceRand(src)
	inputs := []string{src, strings.ReplaceAll(src, "\n", "\r\n"), breakSource(r, src), breakSource(r, src)}
	ts := lexNative(src, 0, 0)
	if toks := ts.Tokens[:len(ts.Tokens)-1]; len(toks) > 0 { // without EOF
		for _, m := range mutations {
			if mutant := m.run(r, ts.Src, toks); mutant != "" && mutant != src {
				inputs = append(inputs, mutant)
			}
		}
	}
	return inputs
}

// goldenFile renders everything the parser gives for the corpus file src:
// its tokens and doc comments (corpusLexInputs), the tokens visited with and
// without a leading BOM, and its parses (corpusParseInputs). codes counts the
// coded errors the parses give.
func goldenFile(src string, codes map[galaerr.ErrorCode]int) string {
	var b strings.Builder
	for _, input := range corpusLexInputs(src) {
		b.WriteString(renderLex(input, 0, 0))
	}
	b.WriteString(renderVisited(src) + renderVisited("\xef\xbb\xbf"+src))
	for _, input := range corpusParseInputs(src) {
		fmt.Fprintf(&b, "\n=== parse %q\n", input)
		p := NewAntlrGalaParser().ParseFile(input)
		b.WriteString(renderParsed(p))
		for _, err := range p.Errors {
			if se, ok := err.(*galaerr.SemanticError); ok && se.Code != "" {
				codes[se.Code]++
			}
		}
	}
	return b.String()
}

// exprSample is an expression's text and where it sits in its file.
type exprSample struct {
	src       string
	line, col int
}

// corpusExpressionSamples are the corpus file src's expressions, as the
// transpiler re-parses them at their positions: its default values, its
// interpolated expressions and runs of its tokens, and a broken variant of
// each.
func corpusExpressionSamples(src string) []exprSample {
	r := sourceRand(src)
	var samples []exprSample
	add := func(s exprSample) {
		samples = append(samples, s, exprSample{breakSource(r, s.src), s.line, s.col})
	}
	if root, errs := native.ParseSourceFileWithRecovery(lexNative(src, 0, 0)); len(errs) == 0 {
		root.Walk(func(n native.GalaNodeRef) bool {
			if n.Kind() == native.GalaKIND_paramDefault {
				e := native.ParamDefaultNode{GalaNodeRef: n}.Expression()
				start := e.Start()
				add(exprSample{e.SourceText(), int(start.Line), int(start.Column)})
			}
			return true
		})
	}
	for _, m := range interpolation.FindAllStringSubmatch(src, -1) {
		add(exprSample{m[1], 1 + r.Intn(500), r.Intn(80)})
	}
	ts := lexNative(src, 0, 0)
	if toks := ts.Tokens[:len(ts.Tokens)-1]; len(toks) > 1 {
		for range 4 {
			i := r.Intn(len(toks))
			j := min(len(toks), i+1+r.Intn(12))
			add(exprSample{ts.Src[toks[i].Start:toks[j-1].End], 1 + r.Intn(500), r.Intn(80)})
		}
	}
	return samples
}

// goldenExpressions renders the parses of the corpus file src's expressions
// (corpusExpressionSamples).
func goldenExpressions(src string) string {
	var b strings.Builder
	for _, s := range corpusExpressionSamples(src) {
		fmt.Fprintf(&b, "\n=== %d:%d %q\n", s.line, s.col, s.src)
		b.WriteString(renderExpressionAt(s.src, s.line, s.col))
		b.WriteString(renderExpressionTree(s.src))
	}
	return b.String()
}

// renderFileParse prints the parse of src as a source file: its doc
// comments, its errors in full, and its native tree, clean or
// error-recovered, with what the native-tree helpers read off each node.
func renderFileParse(src string) string {
	return renderParsed(NewAntlrGalaParser().ParseFile(src))
}

// renderParsed is renderFileParse of the parse p.
func renderParsed(p *Parsed) string {
	out := renderDiagnostics(p.Docs, p.Errors)
	tree, err := p.RecoveredNative().Tree()
	switch {
	case err != nil:
		return out + "\nno tree"
	case len(p.Errors) == 0:
		out += "\nclean tree\n"
	default:
		out += "\nrecovered tree\n"
	}
	return out + tree.Root().ToStringTree() + "\n" + dumpNative(tree) + renderHelpers(tree)
}

// renderHelpers prints, for every node of tree, what the native-tree helpers
// read off it (type_text_native.go): where it starts (NativeDocKeyOf), its
// text when it spans one token (NativeText), and for a type or an alias its
// spelling and pointer element.
func renderHelpers(tree *native.GalaTree) string {
	var b strings.Builder
	for i := range tree.Nodes {
		n := tree.Node(i)
		fmt.Fprintf(&b, "\n#%d %v start %q", i, NativeDocKeyOf(n), NativeStartText(n))
		if n.StopIndex() == n.StartIndex() {
			fmt.Fprintf(&b, " text %q", NativeText(n))
		}
		switch n.Kind() {
		case native.GalaKIND_type:
			typ := native.TypeNode{GalaNodeRef: n}
			elem, ok := NativePointerElem(typ)
			fmt.Fprintf(&b, " type %q ptr #%d pointer #%d %t", NativeTypeText(typ), nodeIndex(NativePtrElem(typ).GalaNodeRef), nodeIndex(elem.GalaNodeRef), ok)
		case native.GalaKIND_typeAlias:
			fmt.Fprintf(&b, " alias %q", NativeAliasText(native.TypeAliasNode{GalaNodeRef: n}))
		}
	}
	return b.String()
}

// nodeIndex is n's index, -1 for no node.
func nodeIndex(n native.GalaNodeRef) int {
	if !n.Ok() {
		return -1
	}
	return n.Index()
}

// renderLex prints src lexed at line/col (lexNative): its tokens, hidden ones
// included, its lexical errors and its doc comments.
func renderLex(src string, line, col int) string {
	ts := lexNative(src, line, col)
	return fmt.Sprintf("\n=== lex %d:%d\n%s\n%s", line, col, strings.Join(printNative(ts), "\n"),
		renderDiagnostics(nativeDocComments(ts), nil))
}

// renderVisited prints the texts VisitNativeTokens visits in src.
func renderVisited(src string) string {
	var visited []string
	VisitNativeTokens(src, func(text string) { visited = append(visited, text) })
	return fmt.Sprintf("\nvisited %q", visited)
}

// renderExpressionAt prints src parsed as exactly one expression at line/col
// (ParseExpressionCheckedAt): its tree, with positions, or its error.
func renderExpressionAt(src string, line, col int) string {
	expr, err := ParseExpressionCheckedAt(src, line, col, "test expression")
	var errs []error
	if err != nil {
		errs = []error{err}
	}
	return "\nchecked " + renderNativeExpression(expr, errs)
}

// renderExpressionTree prints src parsed as an expression
// (ParseExpressionTree): its tree, error-recovered when it has errors, and
// its errors.
func renderExpressionTree(src string) string {
	expr, errs := ParseExpressionTree(src)
	return "\ntree " + renderNativeExpression(expr, errs)
}

// renderNativeExpression prints an expression's native tree (its LISP-like
// form and dumpNative) and errors.
func renderNativeExpression(expr native.ExpressionNode, errs []error) string {
	var tree string
	if expr.Ok() {
		tree = expr.ToStringTree() + "\n" + dumpNative(expr.Tree())
	}
	return tree + renderDiagnostics(nil, errs)
}

// renderDiagnostics prints doc comments, by where their declarations start,
// and errors, each with every field (its code, hint and span included), as
// the CLI, `gala build --json` and the language server render them.
func renderDiagnostics(docs Docs, errs []error) string {
	var b strings.Builder
	keys := make([]DocKey, 0, len(docs))
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Line < keys[j].Line || keys[i].Line == keys[j].Line && keys[i].Column < keys[j].Column
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "\ndoc %d:%d: %q", k.Line, k.Column, docs[k])
	}
	for _, err := range errs {
		fmt.Fprintf(&b, "\nerror: %v (%#v)", err, err)
	}
	return b.String()
}
