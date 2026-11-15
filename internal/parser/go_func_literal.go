package parser

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// goFuncLiterals is GALA-E0117 for each Go function literal of tree, in
// source order: a function value written as Go's
// `func(params) result { body }`, which GALA writes as a lambda.
//
// The grammar parses a literal only to report it here: `primary` has
// `'func' signature block?`, whose block-less form is the function type
// GALA-E0103 is about. With the block the literal is one node, whatever its
// body holds and wherever it is written, so a body's own syntax errors keep
// their own messages and every literal of the file is reported. The parser
// used to fail inside the body instead ("no viable alternative at input
// '{return'", then an "extraneous input" error for each token it skipped up
// to the next declaration), and a body that happened to parse as a composite
// literal's element list (`func() { Println("hi") }`) was rejected by the
// transformer for its body rather than for being a literal.
func goFuncLiterals(tree *native.GalaTree) []*galaerr.SemanticError {
	var out []*galaerr.SemanticError
	// Nodes are stored children first, so a literal nested in another's body
	// comes before it; source order is the order of their `func` tokens.
	for i, n := range tree.Nodes {
		// A literal's primary starts with `func`; checking the token first
		// keeps the pass to two integer compares for every other node.
		if n.Rule != native.GalaRULE_primary || int(n.Start) >= len(tree.Tokens) || tree.Tokens[n.Start].Type != native.GalaFUNC {
			continue
		}
		p := native.PrimaryNode{GalaNodeRef: tree.Node(i)}
		if fn := p.FUNC(); fn.Ok() && hasBody(p) {
			out = append(out, goFuncLiteralDiagnostic(fn, p.Signature()))
		}
	}
	slices.SortFunc(out, func(a, b *galaerr.SemanticError) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
	return out
}

// mergeByPosition is errs, in their order, with lits, in source order, each
// placed before the first error of errs that comes after it in the source.
// An error without a position keeps its place.
func mergeByPosition(errs []error, lits []*galaerr.SemanticError) []error {
	if len(lits) == 0 {
		return errs
	}
	out := make([]error, 0, len(errs)+len(lits))
	for _, e := range errs {
		if line, col, ok := errorPosition(e); ok {
			for len(lits) > 0 && (lits[0].Line < line || lits[0].Line == line && lits[0].Column <= col) {
				out = append(out, lits[0])
				lits = lits[1:]
			}
		}
		out = append(out, e)
	}
	for _, l := range lits {
		out = append(out, l)
	}
	return out
}

// errorPosition is where e, a parser diagnostic, points, if it has a position.
func errorPosition(e error) (line, col int, ok bool) {
	switch e := e.(type) {
	case *galaerr.SyntaxError:
		return e.Line, e.Column, true
	case *galaerr.SemanticError:
		return e.Line, e.Column, true
	}
	return 0, 0, false
}

// hasBody reports whether p, a `func` primary, has a body: a block, or, when
// a syntax error in the body left the error-recovered tree without one, a
// '{' right after the signature.
func hasBody(p native.PrimaryNode) bool {
	if p.Block().Ok() {
		return true
	}
	sig := p.Signature()
	if !sig.Ok() {
		return false
	}
	next := sig.StopIndex() + 1
	return next < len(p.Tree().Tokens) && p.Tree().Text(next) == "{"
}

// goFuncLiteralDiagnostic is GALA-E0117 for the literal whose `func` is fn and
// whose signature is sig, spanning `func` and the signature when they sit on
// one line.
func goFuncLiteralDiagnostic(fn native.GalaTerminal, sig native.SignatureNode) *galaerr.SemanticError {
	start := fn.Token()
	// The caret annotation keeps only the hint's first clause, up to "; ",
	// and a signature is too long for it, so the clause is short.
	body := " with the body unchanged"
	if sig.GoResults().Ok() {
		// The body's `return a, b` does not carry over.
		body = ", whose body gives the Go results as a Try or a tuple"
	}
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeGoFuncLiteral,
		int(start.Line),
		int(start.Column),
		"Go function literal: a function value in GALA is a lambda",
		fmt.Sprintf("write it as a lambda; here, `%s => { ... }`%s", lambdaSignature(sig), body),
	)
	if sig.Ok() {
		if stop := sig.Stop(); stop.Line == start.Line {
			src := sig.Tree().Src
			err = err.WithSpan(int(start.Column) + len([]rune(src[start.Start:stop.End])))
		}
	}
	return err
}

// lambdaSignature is sig respelled as a lambda's signature,
// `(x int, y int) int`. A lambda names and types every parameter, so Go's
// other parameter forms are respelled: an unnamed parameter (`func(int,
// string)`) is named `_`, and a grouped one (`func(a, b int)`) takes the type
// of its group. A Go result list (`(int, error)`) has no lambda form and is
// left out: a lambda gives it as a Try or a tuple.
func lambdaSignature(sig native.SignatureNode) string {
	// Each parameter's name and type, either one empty when not written. A
	// lone name is the type of an unnamed parameter, unless some parameter
	// has both: Go's parameters are all named or all unnamed.
	params := sig.Parameters().ParameterList().AllParameter()
	names, types := make([]string, len(params)), make([]string, len(params))
	named := false
	for i, p := range params {
		if id := p.Identifier(); id.Ok() {
			names[i] = id.GetText()
		}
		if t := p.Type_(); t.Ok() {
			types[i] = oneLine(t.SourceText())
			if p.ELLIPSIS().Ok() {
				types[i] = "..." + types[i]
			}
		}
		named = named || names[i] != "" && types[i] != ""
	}
	parts := make([]string, len(params))
	group := "" // the type of the group the parameters after i belong to
	for i := len(params) - 1; i >= 0; i-- {
		name, typ := names[i], types[i]
		switch {
		case !named && typ == "":
			name, typ = "_", name
		case !named || name == "":
			name = "_"
		case typ == "":
			typ = group
		}
		group = typ
		parts[i] = name + " " + typ
	}
	out := "(" + strings.Join(parts, ", ") + ")"
	if res := sig.Type_(); res.Ok() {
		out += " " + oneLine(res.SourceText())
	}
	return out
}

// oneLine collapses every run of whitespace in s, newlines included, to one
// space: source text respelled on one line for a hint.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
