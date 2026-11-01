package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// An alternative pattern `p1 | p2 | …` matches when any of its alternatives
// matches. In a pattern `|` always separates alternatives, at the top of a case
// and nested inside an extractor or tuple; a bitwise OR is matched with a guard
// or a named val. Alternatives bind no names.

// patternAlternatives returns the alternatives of the pattern expr, or nil when
// it is not an alternative pattern. A parenthesized pattern `(p1 | p2)` is the
// alternative pattern inside it. `|` mixed with `+`, `-` or `^` in one operand
// chain is an error: those share its precedence, so the operand they belong to
// has to be parenthesized.
func (t *galaASTTransformer) patternAlternatives(expr patExpr) ([]patExpr, error) {
	add, ok, err := expr.soleAdditive()
	if err != nil {
		return nil, err
	}
	if !ok {
		if bar, ok := expr.barUnderOperator(); ok {
			line, col := bar.start()
			return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				line, col,
				"'|' separates pattern alternatives and cannot be used inside a comparison or boolean expression in a pattern",
				"compare in a guard, as in `case n if n > limit`")
		}
		return nil, nil
	}
	if len(add.operands) == 1 {
		if inner, ok := t.parenthesizedPattern(expr); ok {
			return t.patternAlternatives(inner)
		}
		return nil, nil
	}
	hasBar, other := false, ""
	for _, op := range add.ops {
		if op == "|" {
			hasBar = true
		} else if other == "" {
			other = op
		}
	}
	if !hasBar {
		return nil, nil
	}
	if other != "" {
		line, col := add.at.start()
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
			line, col,
			fmt.Sprintf("'|' separates pattern alternatives and cannot be mixed with '%s' in a pattern", other),
			fmt.Sprintf("put parentheses around the alternative that uses '%s'", other))
	}
	return add.operands, nil
}

// flatAlternatives returns the alternatives of pat, nested parenthesized
// alternatives included, or pat itself when it has none. A malformed
// alternative pattern counts as one pattern here; lowering reports it.
func (t *galaASTTransformer) flatAlternatives(pat patExpr) []patExpr {
	if !strings.Contains(pat.text(), "|") {
		return []patExpr{pat}
	}
	alts, err := t.patternAlternatives(pat)
	if err != nil || alts == nil {
		return []patExpr{pat}
	}
	var flat []patExpr
	for _, alt := range alts {
		flat = append(flat, t.flatAlternatives(alt)...)
	}
	return flat
}

// transformAlternativePattern lowers the alternatives of one pattern, tried
// left to right: the first one that matches decides, and the statements of
// the ones after it (extractor calls among them) do not run. Alternatives
// that need no statements are a plain `c1 || c2 || …`.
func (t *galaASTTransformer) transformAlternativePattern(alts []patExpr, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	conds := make([]ast.Expr, len(alts))
	stmts := make([][]ast.Stmt, len(alts))
	needStmts := false
	for i, alt := range alts {
		if isWildcard(alt.text()) {
			return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				alt.line(), alt.col(),
				"a wildcard alternative matches everything, so the other alternatives never decide",
				"write `case _` on its own")
		}
		c, s, err := t.transformExpressionPatternWithType(alt, objExpr, matchedType)
		if err != nil {
			return nil, nil, err
		}
		if names := extractUserPatternVarNames(s); len(names) > 0 {
			return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				alt.line(), alt.col(),
				fmt.Sprintf("pattern alternative binds '%s'; an alternative pattern cannot bind names", names[0]),
				"use `_` in place of the name, or give each alternative its own case")
		}
		conds[i], stmts[i] = c, s
		needStmts = needStmts || len(s) > 0
	}
	if !needStmts {
		cond := conds[0]
		for _, c := range conds[1:] {
			cond = &ast.BinaryExpr{X: cond, Op: token.LOR, Y: c}
		}
		return &ast.ParenExpr{X: cond}, nil, nil
	}
	// var matched bool
	// { stmts1; matched = c1 }
	// if !matched { stmts2; matched = c2 }
	matched := t.nextTempVar()
	out := []ast.Stmt{&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{
		&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(matched)}, Type: ast.NewIdent("bool")},
	}}}}
	for i := range alts {
		body := &ast.BlockStmt{List: append(stmts[i], &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(matched)}, Tok: token.ASSIGN, Rhs: []ast.Expr{conds[i]},
		})}
		if i == 0 {
			out = append(out, body)
			continue
		}
		out = append(out, &ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: ast.NewIdent(matched)}, Body: body})
	}
	return ast.NewIdent(matched), out, nil
}

// parenthesizedPattern returns p when expr is exactly `(p)`, or nil; `(p,)`
// is a one-element tuple, not a parenthesized pattern.
func (t *galaASTTransformer) parenthesizedPattern(expr patExpr) (patExpr, bool) {
	if elems, single, ok := expr.parenthesized(t); ok && single {
		return elems[0], true
	}
	return patExpr{}, false
}
