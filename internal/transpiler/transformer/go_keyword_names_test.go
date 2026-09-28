package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// goOnlyKeywords are the Go keywords the GALA grammar does not reserve itself,
// so they lex as ordinary identifiers. The rest of Go's keywords (func, type,
// map, ...) are GALA tokens and never reach a name position.
var goOnlyKeywords = []string{
	"break", "chan", "const", "continue", "default", "defer",
	"fallthrough", "go", "goto", "select", "switch",
}

// goKeywordNamePositions are the places a GALA program introduces a name.
// Each source writes KW for the keyword and marks with @ the occurrence
// GALA-E0055 must point at: the declaration, even when a use comes first.
var goKeywordNamePositions = []struct {
	name string
	src  string
}{
	{"package name", "package @KW\n\nfunc F() int = 1\n"},
	{"val", "package main\n\nfunc main() {\n    val @KW = 1\n    Println(KW)\n}\n"},
	{"var", "package main\n\nfunc main() {\n    var @KW = 1\n    KW = 2\n    Println(KW)\n}\n"},
	{"short var decl", "package main\n\nfunc main() {\n    @KW := 1\n    Println(KW)\n}\n"},
	{"multi val", "package main\n\nfunc main() {\n    val a, @KW = 1, 2\n    Println(a + KW)\n}\n"},
	{"tuple destructuring", "package main\n\nfunc main() {\n    val (@KW, b) = (1, 2)\n    Println(KW + b)\n}\n"},
	{"package val", "package main\n\nval @KW = 1\n\nfunc main() {\n    Println(KW)\n}\n"},
	{"package var", "package main\n\nvar @KW = 1\n\nfunc main() {\n    Println(KW)\n}\n"},
	{"parameter", "package main\n\nfunc f(@KW int) int = KW + 1\n\nfunc main() {\n    Println(f(1))\n}\n"},
	{"lambda parameter", "package main\n\nfunc main() {\n    val f = (@KW int) => KW + 1\n    Println(f(1))\n}\n"},
	{"receiver", "package main\n\nstruct P(x int)\n\nfunc (@KW P) Get2() int = KW.x\n\nfunc main() {\n    Println(P(1).Get2())\n}\n"},
	{"match binding", "package main\n\nfunc main() {\n    val r = 5 match {\n        case @KW => KW + 1\n    }\n    Println(r)\n}\n"},
	{"extractor binding", "package main\n\nfunc main() {\n    val r = Some(1) match {\n        case Some(@KW) => KW\n        case _ => 0\n    }\n    Println(r)\n}\n"},
	{"tuple pattern binding", "package main\n\nfunc main() {\n    val r = (1, 2) match {\n        case (@KW, b) => KW + b\n    }\n    Println(r)\n}\n"},
	{"typed pattern", "package main\n\nfunc main() {\n    val x any = 1\n    val r = x match {\n        case @KW: int => KW\n        case _ => 0\n    }\n    Println(r)\n}\n"},
	{"shorthand struct field", "package main\n\nstruct P(@KW int)\n\nfunc main() {\n    Println(P(1).KW)\n}\n"},
	{"block struct field", "package main\n\ntype P struct {\n    @KW int\n}\n\nfunc main() {\n    Println(P{KW: 1}.KW)\n}\n"},
	{"sealed case field", "package main\n\nsealed type S {\n    case A(@KW int)\n}\n\nfunc main() {\n    Println(A(1).KW)\n}\n"},
	{"function declared after its use", "package main\n\nfunc main() {\n    Println(KW())\n}\n\nfunc @KW() int = 1\n"},
	{"method", "package main\n\nstruct P(x int)\n\nfunc (p P) @KW() int = p.x\n\nfunc main() {\n    Println(P(1).KW())\n}\n"},
	{"interface method", "package main\n\ntype I interface {\n    @KW() int\n}\n\nfunc main() {\n    Println(1)\n}\n"},
	{"struct type", "package main\n\nstruct @KW(x int)\n\nfunc main() {\n    Println(KW(1).x)\n}\n"},
	{"type alias", "package main\n\ntype @KW int\n\nfunc main() {\n    Println(1)\n}\n"},
	{"sealed type", "package main\n\nsealed type @KW {\n    case A(x int)\n}\n\nfunc main() {\n    Println(A(1).x)\n}\n"},
	{"sealed case", "package main\n\nsealed type S {\n    case @KW(x int)\n}\n\nfunc main() {\n    Println(1)\n}\n"},
	{"type parameter", "package main\n\nfunc id[@KW any](x KW) KW = x\n\nfunc main() {\n    Println(id(1))\n}\n"},
	{"named argument", "package main\n\nfunc f(@KW int, y int) int = KW + y\n\nfunc main() {\n    Println(f(y = 2, KW = 1))\n}\n"},
	{"import alias", "package main\n\nimport @KW \"strings\"\n\nfunc main() {\n    Println(KW.ToUpper(\"a\"))\n}\n"},
	{"use without a declaration", "package main\n\nfunc main() {\n    Println(@KW)\n}\n"},
}

// TestGoKeywordNamesAreRejected pins GALA-E0055: a name spelled like a Go
// keyword the GALA grammar does not reserve used to be emitted verbatim, so the
// generated Go did not parse and the author got the internal GALA-E0017. Every
// keyword in every naming position is now a coded error at the declaration.
func TestGoKeywordNamesAreRejected(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	for _, kw := range goOnlyKeywords {
		for _, pos := range goKeywordNamePositions {
			t.Run(kw+"/"+pos.name, func(t *testing.T) {
				src := strings.ReplaceAll(pos.src, "KW", kw)
				wantLine, wantCol := markerPosition(t, src)
				src = strings.Replace(src, "@", "", 1)

				_, err := trans.Transpile(src, "go_keyword_names_test.gala")
				require.Error(t, err)
				var se *galaerr.SemanticError
				require.True(t, errors.As(err, &se), "want a semantic error, got %T: %v", err, err)
				require.Equal(t, galaerr.CodeGoKeywordAsName, se.Code, "got: %v", err)
				require.Contains(t, se.Msg, `"`+kw+`" is a Go keyword`)
				require.Equal(t, wantLine, se.Line, "line of %v", err)
				require.Equal(t, wantCol, se.Column, "column of %v", err)
				require.Equal(t, wantCol+len(kw), se.EndColumn, "span of %v", err)
			})
		}
	}
}

// TestGoKeywordBareStatements pins who reports a keyword standing alone as a
// statement: break / continue are loop control, the E0036 keywords get
// E0036's replacement hint, and every other keyword is still a name, E0055.
func TestGoKeywordBareStatements(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	shapes := []struct {
		name string
		src  string
		// loopControl: break / continue compile here.
		loopControl bool
	}{
		{"loop body", "package main\n\nfunc main() {\n    for i := 0; i < 3; i++ {\n        KW\n    }\n}\n", true},
		{"match arm", "package main\n\nfunc f(n int) {\n    n match {\n        case 1 => KW\n        case _ => Println(n)\n    }\n}\n", false},
		{"partial function arm", "package main\n\nfunc main() {\n    val pf = { case 1 => KW }\n    Println(pf)\n}\n", false},
	}
	for _, shape := range shapes {
		for _, kw := range goOnlyKeywords {
			t.Run(shape.name+"/"+kw, func(t *testing.T) {
				src := strings.ReplaceAll(shape.src, "KW", kw)
				_, err := trans.Transpile(src, "go_keyword_names_test.gala")
				switch {
				case kw == "break" || kw == "continue":
					if shape.loopControl {
						require.NoError(t, err)
					}
				case transformer.ForbiddenStatementKeywords()[kw]:
					require.ErrorContains(t, err, string(galaerr.CodeForbiddenStatementKeyword))
				default:
					require.ErrorContains(t, err, string(galaerr.CodeGoKeywordAsName))
					require.ErrorContains(t, err, `"`+kw+`" is a Go keyword and is not part of GALA`)
				}
			})
		}
	}
}

// TestGoKeywordUseKeepsReplacementHint: a statement keyword written as a call
// (`go(work())`) is E0055, not E0036, but it still names E0036's replacement
// rather than telling the author to rename something they never declared.
func TestGoKeywordUseKeepsReplacementHint(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	_, err := trans.Transpile("package main\n\nfunc work() {}\n\nfunc main() {\n    go(work())\n}\n", "go_keyword_names_test.gala")
	require.ErrorContains(t, err, string(galaerr.CodeGoKeywordAsName))
	require.ErrorContains(t, err, `"go" is a Go keyword and is not part of GALA`)
	require.ErrorContains(t, err, "go_interop.Spawn")
}

// markerPosition returns the 1-based line and 0-based column of the single @
// in src, as the parser reports token positions.
func markerPosition(t *testing.T, src string) (line, col int) {
	t.Helper()
	i := strings.Index(src, "@")
	require.GreaterOrEqual(t, i, 0, "source has no @ marker:\n%s", src)
	before := src[:i]
	return strings.Count(before, "\n") + 1, i - (strings.LastIndex(before, "\n") + 1)
}

// TestGoKeywordNamesLegalNeighbours covers what GALA-E0055 must leave alone:
// break / continue as loop control, names that merely contain a keyword, and
// Go's predeclared identifiers, which a binding may shadow as in Go. Each
// output goes through the generated-Go oracle, which parses and type-checks it.
func TestGoKeywordNamesLegalNeighbours(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "break and continue as loop control",
			src: `package main

func main() {
    var total = 0
    for i := 0; i < 10; i++ {
        if i == 2 {
            continue
        }
        if i == 5 {
            break
        }
        total += i
    }
    Println(total)
}
`,
			want: "continue",
		},
		{
			name: "names containing a keyword",
			src: `package main

struct Job(selected bool, defaults int)

func goNow(switchOn bool, constValue int) int = if (switchOn) constValue else 0

func main() {
    val defaultPort = 8080
    val job = Job(true, 1)
    Println(goNow(job.selected, defaultPort + job.defaults))
}
`,
			want: "defaultPort",
		},
		{
			name: "predeclared identifiers shadowed by locals",
			src: `package main

func main() {
    val len = 3
    val min = 1
    val string = "s"
    val error = len + min
    Println(s"${string}${error}")
}
`,
			want: "var error =",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.src, "")
			require.NoError(t, err)
			require.Contains(t, out, tc.want)
		})
	}
}
