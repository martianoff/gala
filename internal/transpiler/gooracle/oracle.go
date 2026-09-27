// Package gooracle checks that a piece of generated Go is Go a user could
// actually compile, independently of whatever a test expected it to look like.
//
// Transformer tests mostly compare generated Go against an expected string.
// That catches a change, but not a wrong expectation: if the expected string
// itself names an internal symbol or does not parse, the test enshrines the
// bug. Every compile failure of this shape shipped since 0.70 named a symbol
// the user never wrote — a method type-parameter sentinel, an uninstantiated
// Go type parameter, `void` or go/types' `invalid type` rendered as a type, a
// source-map line marker left behind. The oracle looks for exactly that, on
// the output rather than on the expectation:
//
//   - Check parses the source and scans it for the tokens in LeakRules.
//   - TypeChecker type-checks it with go/types, resolving imports through a
//     caller-supplied importer.
//
// The package is test support. It is a library rather than a _test.go file so
// every test binary that produces Go can share one definition of "leaked".
package gooracle

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"sort"
	"strings"
)

// LeakRule describes one internal name that must never reach generated Go.
type LeakRule struct {
	// Name identifies the rule in findings.
	Name string
	// Why says what the token is and why its presence means a transpiler bug.
	Why string
	// match reports whether the identifier at toks[i] starts a leak.
	match func(toks []tok, i int) bool
}

// LeakRules is the single list of internal tokens the oracle rejects. Every
// entry is a name the transpiler uses internally and is contractually bound to
// replace before emitting Go; none of them is something a user can write.
//
// When adding a sentinel to the transformer, add it here.
var LeakRules = []LeakRule{
	{
		Name: "method-type-param-sentinel",
		Why: "`__mtpN` is the name freshMethodTypeParamName gives a method's own " +
			"type parameters while unifying them against receiver type arguments. " +
			"Inference must substitute it away; reaching the output means a method " +
			"type argument was never inferred.",
		match: identPrefix("__mtp"),
	},
	{
		Name: "expected-void-sentinel",
		Why: "`__void__` is ExpectedVoid, the marker for \"this lambda returns " +
			"nothing\". It is an instruction to the lambda transformer, never a type.",
		match: identExact("__void__"),
	},
	{
		Name: "line-marker",
		Why: "`__gala_line_N` is the per-statement source-map marker the " +
			"transformer stamps and insertLineDirectives rewrites into a `//line` " +
			"directive. A surviving marker is an undefined identifier.",
		match: identPrefix("__gala_line_"),
	},
	{
		Name: "go-types-invalid",
		Why: "`invalid type` is how go/types prints types.Typ[types.Invalid] — a " +
			"type it could not resolve. It is a display string, not Go.",
		match: func(toks []tok, i int) bool {
			return toks[i].lit == "invalid" && i+1 < len(toks) && toks[i+1].tok == token.TYPE
		},
	},
	{
		Name: "go-types-untyped",
		Why: "`untyped int`, `untyped nil` and friends are go/types display names " +
			"for the type of an untyped constant. Code generation must default them " +
			"to a concrete type.",
		match: func(toks []tok, i int) bool {
			if toks[i].lit != "untyped" || i+1 >= len(toks) || toks[i+1].tok != token.IDENT {
				return false
			}
			switch toks[i+1].lit {
			case "bool", "int", "rune", "float", "complex", "string", "nil":
				return true
			}
			return false
		},
	},
	{
		Name: "void-type",
		Why: "`void` is GALA's VoidType rendered by name. Go has no such type; a " +
			"function with no result has an empty result list. Flagged only where " +
			"the file does not declare `void` itself.",
		// Handled by the AST pass in Check: it needs to know whether the file
		// declares the name, which a token scan cannot tell.
		match: nil,
	},
}

// Finding is one problem the oracle found.
type Finding struct {
	Rule   string // a LeakRules name, or "parse"
	Line   int
	Detail string
}

func (f Finding) String() string {
	if f.Line > 0 {
		return fmt.Sprintf("line %d: [%s] %s", f.Line, f.Rule, f.Detail)
	}
	return fmt.Sprintf("[%s] %s", f.Rule, f.Detail)
}

// Check parses src as a Go file and reports parse errors and leaked internal
// tokens. The leak scan runs on the token stream, so it still reports leaks in
// a file that does not parse — which is usually why it does not parse.
func Check(filename, src string) []Finding {
	var out []Finding

	toks := scan(src)
	for i := range toks {
		if toks[i].tok != token.IDENT {
			continue
		}
		for _, r := range LeakRules {
			if r.match != nil && r.match(toks, i) {
				out = append(out, Finding{Rule: r.Name, Line: toks[i].line, Detail: fmt.Sprintf("leaked internal token %q", toks[i].lit)})
			}
		}
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				out = append(out, Finding{Rule: "parse", Line: e.Pos.Line, Detail: e.Msg})
			}
		} else {
			out = append(out, Finding{Rule: "parse", Detail: err.Error()})
		}
	}
	if file != nil {
		out = append(out, voidTypeFindings(fset, file)...)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// voidTypeFindings reports every reference to `void` the file does not
// declare. go/parser resolves identifiers against the file's own scope, so an
// unresolved `void` is a use of a name Go does not predeclare.
func voidTypeFindings(fset *token.FileSet, file *ast.File) []Finding {
	var out []Finding
	for _, id := range file.Unresolved {
		if id.Name == "void" {
			out = append(out, Finding{Rule: "void-type", Line: fset.Position(id.Pos()).Line,
				Detail: "`void` used as a Go identifier"})
		}
	}
	return out
}

type tok struct {
	tok  token.Token
	lit  string
	line int
}

// scan tokenizes src, dropping comments. Scan errors are ignored here; the
// parser reports them.
func scan(src string) []tok {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), func(token.Position, string) {}, 0)
	var out []tok
	for {
		pos, t, lit := s.Scan()
		if t == token.EOF {
			return out
		}
		if t == token.SEMICOLON && lit == "\n" {
			continue
		}
		if t.IsKeyword() {
			lit = t.String()
		}
		out = append(out, tok{t, lit, fset.Position(pos).Line})
	}
}

func identPrefix(p string) func([]tok, int) bool {
	return func(toks []tok, i int) bool { return strings.HasPrefix(toks[i].lit, p) }
}

func identExact(n string) func([]tok, int) bool {
	return func(toks []tok, i int) bool { return toks[i].lit == n }
}
