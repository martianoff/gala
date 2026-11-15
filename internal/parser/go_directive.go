package parser

import (
	"fmt"
	"go/build/constraint"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// goDirectives is GALA-E0119 for each Go compiler directive among the line
// comments of ts, in source order: a `//go:` line (`//go:embed`,
// `//go:build`, `//go:noinline`, ...) or a legacy `// +build` constraint.
//
// GALA does not carry such a line into the generated Go — a comment reaches
// it only as a declaration's doc, and a directive is never doc (see
// isPragmaBody) — so it used to be dropped without a word: a `//go:embed`
// variable stayed empty, and a file constrained to one platform built on
// every one. A block comment is no directive to Go, and is not one here.
func goDirectives(ts *native.GalaTokens) []*galaerr.SemanticError {
	var out []*galaerr.SemanticError
	for h := range ts.Hidden {
		body, ok := strings.CutPrefix(ts.HiddenText(h), "//")
		if !ok || !isGoDirectiveBody(body) {
			continue
		}
		tk := ts.Hidden[h]
		line := "//" + strings.TrimRight(body, " \t\r\n")
		out = append(out, galaerr.NewCodedSemanticError(
			galaerr.CodeGoDirective, int(tk.Line), int(tk.Column),
			fmt.Sprintf("Go directive `%s` in a GALA file: GALA does not carry it into the generated Go", line),
			goDirectiveHint(strings.TrimSpace(body), declaredName(ts, int(tk.Next))),
		).WithSpan(int(tk.Column)+len([]rune(line))))
	}
	return out
}

// isGoDirectiveBody reports whether body, a `//` comment without its marker,
// is a Go compiler directive: `go:` followed by the directive's name (a
// lower-case letter or digit first, as go/ast reads a directive), or a
// `+build` constraint.
func isGoDirectiveBody(body string) bool {
	if name, ok := strings.CutPrefix(body, "go:"); ok {
		return name != "" && (name[0] >= 'a' && name[0] <= 'z' || name[0] >= '0' && name[0] <= '9')
	}
	// Only a body that starts with `+build` can be one; checking that first
	// spares the concatenation for every prose comment.
	return strings.HasPrefix(strings.TrimLeft(body, " \t"), "+build") && constraint.IsPlusBuild("//"+body)
}

// declaredName is the name a `var` or `val` declaration starting at token i
// of ts declares, or "content" when no such declaration starts there: the
// name an `embed val` replacing a `//go:embed` above it takes.
func declaredName(ts *native.GalaTokens, i int) string {
	if i+1 < len(ts.Tokens) && (ts.Text(i) == "var" || ts.Text(i) == "val") && ts.Tokens[i+1].Type == native.GalaIDENTIFIER {
		return ts.Text(i + 1)
	}
	return "content"
}

// goDirectiveHint is the hint of GALA-E0119 for directive, a directive
// comment's text after `//`, whose `embed val` binds varName. It names the
// GALA form where there is one (`embed val`), and a Go file of the package
// otherwise.
func goDirectiveHint(directive, varName string) string {
	name, args, _ := strings.Cut(directive, " ")
	switch name {
	case "go:embed":
		patterns := embedPatterns(args)
		switch len(patterns) {
		case 0:
			return fmt.Sprintf("embed with `embed val`: `embed val %s = \"file.txt\"` for a string, or `embed val %s EmbeddedFS = \"static/*\"` for a file system", varName, varName)
		case 1:
			return fmt.Sprintf("embed with `embed val`; here, `embed val %s = %s` for a string, or `embed val %s EmbeddedFS = %s` for a file system", varName, patterns[0], varName, patterns[0])
		}
		return fmt.Sprintf("embed with `embed val`; here, `embed val %s EmbeddedFS = %s`", varName, strings.Join(patterns, ", "))
	case "go:build", "+build":
		return "a GALA file has no build constraint; put the code for one platform in a .go file of the package, which keeps its `//go:build` line"
	}
	return fmt.Sprintf("GALA has no form of `//%s`; put the code that needs it in a .go file of the package", name)
}

// embedPatterns is the patterns of a `//go:embed` line's arguments, args, as
// GALA string literals. Go separates them by spaces and lets one be quoted,
// with "..." or `...`, to hold a space.
func embedPatterns(args string) []string {
	var out []string
	for rest := strings.TrimSpace(args); rest != ""; rest = strings.TrimSpace(rest) {
		pattern := rest
		if quoted, err := strconv.QuotedPrefix(rest); err == nil {
			pattern, _ = strconv.Unquote(quoted)
			rest = rest[len(quoted):]
		} else if i := strings.IndexAny(rest, " \t"); i >= 0 {
			pattern, rest = rest[:i], rest[i:]
		} else {
			rest = ""
		}
		out = append(out, strconv.Quote(pattern))
	}
	return out
}
