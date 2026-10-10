package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
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
func (t *galaASTTransformer) patternAlternatives(expr grammar.IExpressionContext) ([]grammar.IExpressionContext, error) {
	add := leadingAdditiveExpr(expr, true)
	if add == nil {
		if bar := barUnderOperator(expr); bar != nil {
			return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				bar.GetStart().GetLine(), bar.GetStart().GetColumn(),
				"'|' separates pattern alternatives and cannot be used inside a comparison or boolean expression in a pattern",
				"compare in a guard, as in `case n if n > limit`")
		}
		return nil, nil
	}
	muls := add.AllMultiplicativeExpr()
	if len(muls) == 1 {
		// `(p,)` is a one-element tuple, not a parenthesized pattern.
		if list := t.parenthesizedList(expr); list != nil && list.GetChildCount() == 1 {
			return t.patternAlternatives(list.Expression(0))
		}
		return nil, nil
	}
	hasBar, other := false, ""
	for i := 1; i < len(muls); i++ {
		op, err := getChildOperatorText(add, i*2-1)
		if err != nil {
			return nil, err
		}
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
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
			add.GetStart().GetLine(), add.GetStart().GetColumn(),
			fmt.Sprintf("'|' separates pattern alternatives and cannot be mixed with '%s' in a pattern", other),
			fmt.Sprintf("put parentheses around the alternative that uses '%s'", other))
	}
	alts := make([]grammar.IExpressionContext, len(muls))
	for i, m := range muls {
		alts[i] = wrapAsExpression(expr, m.(*grammar.MultiplicativeExprContext))
	}
	return alts, nil
}

// barUnderOperator returns an additive chain of expr that holds a `|`, when
// `||`, `&&` or a comparison joins several operands above it, or nil.
func barUnderOperator(expr grammar.IExpressionContext) *grammar.AdditiveExprContext {
	or, _ := expr.OrExpr().(*grammar.OrExprContext)
	if or == nil {
		return nil
	}
	for _, a := range or.AllAndExpr() {
		for _, e := range a.(*grammar.AndExprContext).AllEqualityExpr() {
			for _, r := range e.(*grammar.EqualityExprContext).AllRelationalExpr() {
				for _, ad := range r.(*grammar.RelationalExprContext).AllAdditiveExpr() {
					add := ad.(*grammar.AdditiveExprContext)
					for i := 1; i < len(add.AllMultiplicativeExpr()); i++ {
						if op, err := getChildOperatorText(add, i*2-1); err == nil && op == "|" {
							return add
						}
					}
				}
			}
		}
	}
	return nil
}

// flatAlternatives returns the alternatives of pat, nested parenthesized
// alternatives included, or pat itself when it has none. A malformed
// alternative pattern counts as one pattern here; lowering reports it.
func (t *galaASTTransformer) flatAlternatives(pat grammar.IExpressionContext) []grammar.IExpressionContext {
	if !strings.Contains(pat.GetText(), "|") {
		return []grammar.IExpressionContext{pat}
	}
	alts, err := t.patternAlternatives(pat)
	if err != nil || alts == nil {
		return []grammar.IExpressionContext{pat}
	}
	var flat []grammar.IExpressionContext
	for _, alt := range alts {
		flat = append(flat, t.flatAlternatives(alt)...)
	}
	return flat
}

// wrapAsExpression builds an expression node whose only operand is m, so an
// alternative goes through the same pattern lowering as a whole pattern. The
// node takes m's position and hangs off the parent of the pattern it was
// split from; m itself keeps its parent.
func wrapAsExpression(from grammar.IExpressionContext, m *grammar.MultiplicativeExprContext) grammar.IExpressionContext {
	parser := m.GetParser()
	parent, _ := from.GetParent().(antlr.ParserRuleContext)
	expr := grammar.NewExpressionContext(parser, parent, -1)
	or := grammar.NewOrExprContext(parser, expr, -1)
	and := grammar.NewAndExprContext(parser, or, -1)
	eq := grammar.NewEqualityExprContext(parser, and, -1)
	rel := grammar.NewRelationalExprContext(parser, eq, -1)
	add := grammar.NewAdditiveExprContext(parser, rel, -1)
	add.AddChild(m)
	rel.AddChild(add)
	eq.AddChild(rel)
	and.AddChild(eq)
	or.AddChild(and)
	expr.AddChild(or)
	for _, c := range []antlr.ParserRuleContext{expr, or, and, eq, rel, add} {
		c.SetStart(m.GetStart())
		c.SetStop(m.GetStop())
	}
	return expr
}

// transformAlternativePattern lowers the alternatives of one pattern, tried
// left to right: the first one that matches decides, and the statements of
// the ones after it (extractor calls among them) do not run. Alternatives
// that need no statements are a plain `c1 || c2 || …`.
func (t *galaASTTransformer) transformAlternativePattern(alts []grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	conds := make([]ast.Expr, len(alts))
	stmts := make([][]ast.Stmt, len(alts))
	needStmts := false
	for i, alt := range alts {
		if isWildcard(alt.GetText()) {
			return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				alt.GetStart().GetLine(), alt.GetStart().GetColumn(),
				"a wildcard alternative matches everything, so the other alternatives never decide",
				"write `case _` on its own")
		}
		c, s, err := t.transformExpressionPatternWithType(alt, objExpr, matchedType)
		if err != nil {
			return nil, nil, err
		}
		if names := extractUserPatternVarNames(s); len(names) > 0 {
			return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidAlternativePattern,
				alt.GetStart().GetLine(), alt.GetStart().GetColumn(),
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
