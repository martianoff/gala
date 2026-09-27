package fuzz_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
)

// Encoding mutations FuzzSourceEncoding can apply, as bits of its mode byte.
const (
	encodePrependBOM    = 1 << iota // a leading UTF-8 byte order mark
	encodeLineComments              // `// <non-ASCII>` appended to every line
	encodeBlockComments             // `/*<non-ASCII>*/` before some tokens
	encodeStringRunes               // a non-ASCII rune in each string literal, raw vs escaped
	encodeAll           = 1<<iota - 1
)

// encodingRunes are inserted by the mutations: Latin-1, BMP symbols, CJK, an
// astral-plane rune (two UTF-16 units), and a combining mark.
var encodingRunes = []rune{'é', '✓', '日', '𝄞', 'Ω', '\u0301', 'ß', '🙂'}

var encodingSnippets = []string{
	"package main\n\nfunc main() {\n    val greeting = \"hello\"\n    Println(s\"$greeting, ${greeting.Size()} runes\")\n}\n",
	"package main\n\n// Doc comment.\nfunc add(a int, b int) int = a + b\n\nfunc main() {\n    Println(add(1, 2))\n}\n",
	"package main\n\nfunc main() {\n    val x = 3\n    val r = x match {\n        case 1 => \"one\"\n        case _ => f\"n=$x%03d\"\n    }\n    Println(r)\n}\n",
	"package main\n\nfunc main() {\n    val bad = undefinedName + 1\n    Println(bad)\n}\n",
	"package main\n\nfunc main() {\n    Println(\"(\\d)\")\n}\n",
	"package main\n\nfunc main() {\n    val raw = `line one\nline two`\n    Println(raw)\n}\n",
}

// FuzzSourceEncoding checks that the source's encoding is not semantics: a
// leading byte order mark, non-ASCII text in comments, and a non-ASCII rune
// written raw rather than as a \u escape inside a string all leave the program
// meaning the same thing. Two spellings of one program must therefore both be
// accepted — with the same Go, compared after dropping comments (the //line
// directives) and normalising each string literal to its value — or both be
// rejected with the same diagnostics on the same lines. Every diagnostic must
// also be positioned inside its own input, which is what catches byte-offset
// versus code-point-offset confusion.
func FuzzSourceEncoding(f *testing.F) {
	seeds := append(slices.Clone(encodingSnippets), exampleSeeds(f)...)
	for i, s := range seeds {
		f.Add(s, uint8(encodeAll), uint16(i))
		f.Add(s, uint8(1+i%encodeAll), uint16(i*7919))
	}
	warmUp(f)
	f.Fuzz(func(t *testing.T, src string, mode uint8, salt uint16) {
		if !utf8.ValidString(src) {
			return // an encoding question of its own; FuzzTranspileNoCrash covers it
		}
		left, right, ok := encodingVariants(src, mode, salt)
		if !ok {
			return
		}
		lres := transpile(t, left)
		lfile := checkOutcome(t, left, lres)
		rres := transpile(t, right)
		rfile := checkOutcome(t, right, rres)

		switch {
		case lres.Err != nil && rres.Err != nil:
			lcodes, rcodes := diagnosticCodes(lres.Err), diagnosticCodes(rres.Err)
			// After a syntax error the tree is ANTLR's error recovery, and
			// the parser's blank-line check reads the text between recovered
			// tokens, where an inserted comment can make the difference
			// between "no gap to inspect" and "a gap without a blank line".
			// The leading diagnostic must still agree.
			if hasSyntaxError(lcodes) && hasSyntaxError(rcodes) {
				lcodes, rcodes = lcodes[:1], rcodes[:1]
			}
			if !slices.Equal(lcodes, rcodes) {
				t.Fatalf("re-encoding changed the diagnostics: %v vs %v\nleft:\n%q\nright:\n%q\nleft error: %v\nright error: %v",
					lcodes, rcodes, left, right, lres.Err, rres.Err)
			}
		case lres.Err != nil || rres.Err != nil:
			t.Fatalf("re-encoding changed acceptance\nleft:\n%q\nerror: %v\nright:\n%q\nerror: %v", left, lres.Err, right, rres.Err)
		default:
			lgo, rgo := normalizedGo(t, lfile), normalizedGo(t, rfile)
			if lgo != rgo {
				t.Fatalf("re-encoding changed the generated Go (%s)\nleft:\n%q\nright:\n%q", firstDiff(lgo, rgo), left, right)
			}
		}
	})
}

func hasSyntaxError(codes []string) bool {
	return slices.ContainsFunc(codes, func(c string) bool { return strings.HasPrefix(c, "syntax@") })
}

// firstDiff describes the first line on which two texts differ.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("first difference at generated line %d:\n  left:  %s\n  right: %s", i+1, x, y)
		}
	}
	return "no line differs"
}

// insertion adds text at a code-point index of the BOM-less source, to the
// left spelling, the right spelling, or both.
type insertion struct {
	at          int
	left, right string
}

// encodingVariants returns two spellings of src that must mean the same
// program. It reports false when the mode selects nothing to change.
func encodingVariants(src string, mode uint8, salt uint16) (string, string, bool) {
	mode &= encodeAll
	body := galaerr.StripBOM(src)
	runes := []rune(body)
	pick := func(i int) string { return string(encodingRunes[(i+int(salt))%len(encodingRunes)]) }

	var ins []insertion
	// Comments and string edits are only meaningful where the lexer's view of
	// the text is exact. Where it is not — a stray character, an unterminated
	// literal — text added after the bad spot is lexed as part of the error.
	toks := lexTokens(body)
	covered := tokenCoverage(runes, toks)
	if mode&(encodeLineComments|encodeBlockComments|encodeStringRunes) != 0 && lexesCleanly(runes, covered) {
		if mode&encodeLineComments != 0 {
			for i, r := range runes {
				// Blank lines stay blank: GALA requires an empty line after
				// the package clause and the imports, and a comment-only line
				// is not empty.
				// A newline inside a token (a multi-line raw string or block
				// comment) is not a line end a comment may follow.
				if r != '\n' || covered[i] || blankLineEndingAt(runes, i) {
					continue
				}
				at := i
				if at > 0 && runes[at-1] == '\r' {
					at--
				}
				ins = append(ins, insertion{at: at, right: " // " + pick(i) + pick(i+1)})
			}
		}
		if mode&encodeBlockComments != 0 {
			for i, tok := range toks {
				if tok.GetChannel() == antlr.TokenDefaultChannel && (i+int(salt))%3 == 0 {
					// Space-padded: glued to a preceding `/` the opener
					// would read as `//*`, a line comment.
					ins = append(ins, insertion{at: tok.GetStart(), right: " /*" + pick(i) + "*/ "})
				}
			}
		}
		if mode&encodeStringRunes != 0 {
			for i, tok := range literalTokens(toks) {
				r := encodingRunes[(i+int(salt))%len(encodingRunes)]
				// Just past the opening quote, which is outside any ${...}.
				at := tok.GetStart() + literalPrefix(tok.GetText())
				ins = append(ins, insertion{at: at, left: escapeRune(r), right: string(r)})
			}
		}
	}
	if len(ins) == 0 && mode&encodePrependBOM == 0 {
		return "", "", false
	}

	sort.SliceStable(ins, func(a, b int) bool { return ins[a].at < ins[b].at })
	var left, right []rune
	next := 0
	for _, in := range ins {
		left = append(left, runes[next:in.at]...)
		right = append(right, runes[next:in.at]...)
		left = append(left, []rune(in.left)...)
		right = append(right, []rune(in.right)...)
		next = in.at
	}
	left = append(left, runes[next:]...)
	right = append(right, runes[next:]...)

	prefix := ""
	if mode&encodePrependBOM != 0 {
		prefix = "\xef\xbb\xbf"
	}
	return string(left), prefix + string(right), true
}

// lexTokens lexes src (already BOM-less) into all its tokens, hidden-channel
// comments included. Token start/stop indices are code-point offsets.
func lexTokens(src string) []antlr.Token {
	var toks []antlr.Token
	galaparser.VisitTokens(src, func(tok antlr.Token) { toks = append(toks, tok) })
	return toks
}

// tokenCoverage marks the code points that fall within a token.
func tokenCoverage(runes []rune, toks []antlr.Token) []bool {
	covered := make([]bool, len(runes))
	for _, tok := range toks {
		for i := max(tok.GetStart(), 0); i <= tok.GetStop() && i < len(runes); i++ {
			covered[i] = true
		}
	}
	return covered
}

// lexesCleanly reports whether every code point outside the tokens is
// whitespace, i.e. the lexer hit no character it could not place.
func lexesCleanly(runes []rune, covered []bool) bool {
	for i, r := range runes {
		if !covered[i] && !isLexerSpace(r) {
			return false
		}
	}
	return true
}

// isLexerSpace matches the grammar's WS rule exactly: other Unicode spaces
// are not skipped by the lexer.
func isLexerSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// blankLineEndingAt reports whether the line whose newline is at runes[nl]
// holds only whitespace.
func blankLineEndingAt(runes []rune, nl int) bool {
	for i := nl - 1; i >= 0 && runes[i] != '\n'; i-- {
		if !isLexerSpace(runes[i]) {
			return false
		}
	}
	return true
}

// literalTokens returns the string, s"..." and f"..." literals whose value is
// program data — not the path of an import spec, which names a package.
func literalTokens(toks []antlr.Token) []antlr.Token {
	var out []antlr.Token
	inImport, inImportBlock := false, false
	for _, tok := range toks {
		if tok.GetChannel() != antlr.TokenDefaultChannel {
			continue
		}
		switch text := tok.GetText(); {
		case text == "import":
			inImport = true
			continue
		case inImport && text == "(":
			inImportBlock = true
			continue
		case inImportBlock && text == ")":
			inImport, inImportBlock = false, false
			continue
		}
		switch literalPrefix(tok.GetText()) {
		case 1: // "..."
			if inImport {
				if !inImportBlock {
					inImport = false
				}
				continue
			}
			out = append(out, tok)
		case 2: // s"..." or f"..."
			out = append(out, tok)
		}
	}
	return out
}

// literalPrefix classifies a token by its spelling: the length of the text
// before the opening quote plus one (1 for "...", 2 for s"..." and f"..."), or
// 0 for any other token. The generated lexer's token-type constants are
// unexported, and the spelling is unambiguous: no other token starts with a
// double quote, and an identifier cannot contain one.
func literalPrefix(text string) int {
	switch {
	case len(text) >= 2 && text[0] == '"':
		return 1
	case len(text) >= 3 && (text[0] == 's' || text[0] == 'f') && text[1] == '"':
		return 2
	}
	return 0
}

func escapeRune(r rune) string {
	if r > 0xFFFF {
		return fmt.Sprintf(`\U%08X`, r)
	}
	return fmt.Sprintf(`\u%04X`, r)
}

// normalizedGo prints file without comments (the //line directives name GALA
// lines, which a line comment never shifts, but a doc comment's text may carry
// the inserted runes) and with every string and rune literal re-spelled from
// its value, so a raw rune and its escape compare equal.
func normalizedGo(t *testing.T, file *ast.File) string {
	t.Helper()
	file.Comments = nil
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			switch n.Kind {
			case token.STRING:
				if v, err := strconv.Unquote(n.Value); err == nil {
					n.Value = strconv.QuoteToASCII(v)
				}
			case token.CHAR:
				if v, err := strconv.Unquote(n.Value); err == nil {
					r, _ := utf8.DecodeRuneInString(v)
					n.Value = strconv.QuoteRuneToASCII(r)
				}
			}
		case *ast.FuncDecl:
			n.Doc = nil
		case *ast.GenDecl:
			n.Doc = nil
		case *ast.Field:
			n.Doc, n.Comment = nil, nil
		case *ast.ValueSpec:
			n.Doc, n.Comment = nil, nil
		case *ast.TypeSpec:
			n.Doc, n.Comment = nil, nil
		case *ast.ImportSpec:
			n.Doc, n.Comment = nil, nil
		}
		return true
	})
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), file); err != nil {
		t.Fatalf("cannot print normalised Go: %v", err)
	}
	// Re-parse and re-print so positions left over from the original file set
	// cannot influence layout.
	fset := token.NewFileSet()
	again, err := parser.ParseFile(fset, "normalized.go", buf.Bytes(), 0)
	if err != nil {
		t.Fatalf("normalised Go does not re-parse: %v", err)
	}
	buf.Reset()
	if err := printer.Fprint(&buf, fset, again); err != nil {
		t.Fatalf("cannot print normalised Go: %v", err)
	}
	return buf.String()
}
