package fuzz_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// literalKind selects how FuzzStringLiterals wraps the fuzzed content.
type literalKind uint8

const (
	kindString literalKind = iota // "..."
	kindRaw                       // `...`
	kindInterp                    // s"..."
	kindFormat                    // f"..."
	kindRune                      // '...'
	numLiteralKinds
)

func (k literalKind) wrap(content string) string {
	switch k {
	case kindRaw:
		return "`" + content + "`"
	case kindInterp:
		return `s"` + content + `"`
	case kindFormat:
		return `f"` + content + `"`
	case kindRune:
		return "'" + content + "'"
	default:
		return `"` + content + `"`
	}
}

// literalProgram embeds a literal in a small valid program. `v` is in scope
// so interpolations like $v and ${v + 1} have something to refer to.
func literalProgram(lit string) string {
	return "package main\n\nfunc main() {\n    val v = 7\n    val x = " + lit + "\n    Println(v)\n    Println(x)\n}\n"
}

// literalSeeds are contents that exercised past literal bugs, or sit on the
// edges of the escape and interpolation rules.
var literalSeeds = []string{
	"",
	"plain",
	"é ✓ 𝄞 日本",
	`a\tb\nc\\d\"e`,
	`\a\b\f\n\r\t\v`,
	`\x41\x7f\xff`,
	`\101\377`,
	`\400`,
	`\u00e9\U0001F600`,
	`\uD800`,
	`\U00110000`,
	`\x4`,
	`\d{4}`,
	`(\d+)\s*`,
	`\'`,
	`\`,
	"\\\n",
	`100%`,
	`%d %s %%`,
	`$$`,
	`$$v`,
	`$v`,
	`$v%04d`,
	`${v}`,
	`${v + 1}`,
	`${v +}`,
	`${`,
	`${}`,
	`${"a"}`,
	`${"a\"b"}`,
	`${v}%x and $v%`,
	`__gala_line_5`,
	`//line other.gala:9`,
	"/* not a comment */",
	"\x00",
	"a\x00b",
	"\xef\xbb\xbf",
	"a\xef\xbb\xbfb",
	"\xff",
	"a\xc3",
	"\r",
	"a\r\nb",
	"`",
	`"`,
	"'",
	"x",
	`\n`,
	`\u0041`,
}

// FuzzStringLiterals puts arbitrary content inside each literal form. Beyond
// the shared properties (no panic or hang; a coded, in-bounds diagnostic or
// parseable Go), it checks the literal's VALUE wherever the GALA spec fixes
// one: GALA literals have Go's semantics, so for content the lexer takes as a
// single literal token and that carries no interpolation,
//
//   - if Go would reject it (an invalid escape, or a character Go source may
//     not contain: NUL, a byte order mark, invalid UTF-8), GALA must reject it
//     with a diagnostic;
//   - otherwise GALA must accept it, and the Go it emits must evaluate to the
//     same bytes strconv.Unquote gives for the Go literal (for raw strings:
//     the content with carriage returns removed, as in Go).
func FuzzStringLiterals(f *testing.F) {
	for _, s := range literalSeeds {
		for k := literalKind(0); k < numLiteralKinds; k++ {
			f.Add(s, uint8(k))
		}
	}
	warmUp(f)
	f.Fuzz(func(t *testing.T, content string, kindByte uint8) {
		kind := literalKind(kindByte % uint8(numLiteralKinds))
		src := literalProgram(kind.wrap(content))
		res := transpile(t, src)
		file := checkOutcome(t, src, res)

		want, verdict := expectedLiteral(content, kind)
		switch verdict {
		case verdictReject:
			if res.Err == nil {
				t.Fatalf("%s literal %q is invalid Go-literal content, but it was accepted; generated:\n%s", kindName(kind), content, res.Go)
			}
		case verdictAccept:
			if res.Err != nil {
				t.Fatalf("%s literal %q is valid, but it was rejected: %v", kindName(kind), content, res.Err)
			}
			got, err := literalValue(file)
			if err != nil {
				t.Fatalf("%s literal %q: %v\ngenerated:\n%s", kindName(kind), content, err, res.Go)
			}
			if got != want {
				t.Fatalf("%s literal %q: generated Go evaluates to %q, want %q\ngenerated:\n%s", kindName(kind), content, got, want, res.Go)
			}
		}
	})
}

func kindName(k literalKind) string {
	return [...]string{"string", "raw string", "s-interpolated", "f-format", "rune"}[k]
}

type verdict int

const (
	verdictUnknown verdict = iota // no value model; shared properties only
	verdictReject
	verdictAccept
)

// expectedLiteral models the GALA spec for a literal's content. It returns
// verdictUnknown when the content would not lex as one literal token of the
// intended kind (it then changes the program's shape, not the literal) or when
// it contains an interpolation, which this model does not evaluate.
func expectedLiteral(content string, kind literalKind) (string, verdict) {
	if !lexesAsOneToken(content, kind) {
		return "", verdictUnknown
	}
	if (kind == kindInterp || kind == kindFormat) && strings.Contains(content, "$") {
		return "", verdictUnknown
	}
	if !goSourceChars(content) {
		return "", verdictReject
	}
	switch kind {
	case kindRaw:
		return strings.ReplaceAll(content, "\r", ""), verdictAccept
	case kindRune:
		v, err := strconv.Unquote("'" + content + "'")
		if err != nil {
			return "", verdictReject
		}
		return v, verdictAccept
	default:
		v, err := strconv.Unquote(`"` + content + `"`)
		if err != nil {
			return "", verdictReject
		}
		return v, verdictAccept
	}
}

// goSourceChars reports whether s may appear in Go source: valid UTF-8, with
// no NUL and no byte order mark (Go only tolerates a BOM as the file's first
// character, which a literal's content never is).
func goSourceChars(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && !strings.ContainsRune(s, byteOrderMark)
}

// byteOrderMark is U+FEFF, spelled numerically so this file never contains it.
const byteOrderMark = rune(0xFEFF)

// lexesAsOneToken mirrors the grammar's STRING / RAW_STRING / CHAR_LIT /
// INTERPOLATED_STRING token rules for content without `$`.
func lexesAsOneToken(content string, kind literalKind) bool {
	runes := []rune(content) // invalid bytes become U+FFFD, as for the lexer
	switch kind {
	case kindRaw:
		return !strings.ContainsRune(content, '`')
	case kindRune:
		switch {
		case len(runes) == 1:
			return runes[0] != '\'' && runes[0] != '\r' && runes[0] != '\n' && runes[0] != '\\'
		case len(runes) == 2:
			return runes[0] == '\\'
		}
		return false
	}
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			if i+1 >= len(runes) {
				return false
			}
			i++
		case '"', '\r', '\n':
			return false
		}
	}
	return true
}

// literalValue finds `x` in the generated main and evaluates its initializer.
func literalValue(file *ast.File) (string, error) {
	var init ast.Expr
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || init != nil {
			return init == nil
		}
		for i, name := range spec.Names {
			if name.Name == "x" && i < len(spec.Values) {
				init = spec.Values[i]
			}
		}
		return init == nil
	})
	if init == nil {
		return "", fmt.Errorf("no initializer for x in the generated Go")
	}
	// A val lowers to std.NewImmutable(<value>).
	if call, ok := init.(*ast.CallExpr); ok && isSelector(call.Fun, "std", "NewImmutable") && len(call.Args) == 1 {
		init = call.Args[0]
	}
	return evalString(init)
}

// evalString evaluates the constant string (or rune) expressions the
// transpiler emits for literals: basic literals, `+` concatenation and
// fmt.Sprintf over constant arguments.
func evalString(e ast.Expr) (string, error) {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return evalString(e.X)
	case *ast.BasicLit:
		switch e.Kind {
		case token.STRING, token.CHAR:
			return strconv.Unquote(e.Value)
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			l, err := evalString(e.X)
			if err != nil {
				return "", err
			}
			r, err := evalString(e.Y)
			if err != nil {
				return "", err
			}
			return l + r, nil
		}
	case *ast.CallExpr:
		if isSelector(e.Fun, "fmt", "Sprintf") && len(e.Args) > 0 && e.Ellipsis == token.NoPos {
			format, err := evalString(e.Args[0])
			if err != nil {
				return "", err
			}
			args := make([]any, 0, len(e.Args)-1)
			for _, a := range e.Args[1:] {
				v, err := evalString(a)
				if err != nil {
					return "", err
				}
				args = append(args, v)
			}
			return fmt.Sprintf(format, args...), nil
		}
	}
	return "", fmt.Errorf("unrecognised shape for a literal's value: %T", e)
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
