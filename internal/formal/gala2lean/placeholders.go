package gala2lean

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Placeholder lambdas. A call argument whose slot wants a function and that
// contains `_` is a lambda of one parameter per `_` (docs/GALA.MD,
// "Placeholder Lambda Shorthand"). The rules here follow the transformer's
// code (internal/transpiler/transformer; lines at the time of writing):
//
//   - tryRewriteAsPlaceholderLambda (lambdas.go:1621) rewrites an argument
//     whose slot is a function type when visitPlaceholders (args_source.go:233)
//     finds a `_` in it: every `_` written in the argument, in source order,
//     outside nested lambdas and case clauses, inside if and match
//     expressions and blocks too; a `_` in `${...}` is inside a string token
//     there, and is never found;
//   - transformArgument (calls.go:5359) tries it on an argument before
//     lowering what is inside, and while the body is lowered a nested
//     argument whose slot is a function type makes a lambda of its own,
//     binding `_` closer: a `_` belongs to the innermost such argument
//     around it (placeholderTakes.take, lambdas.go:1559);
//   - the lambda's parameters are the `_` it takes, in source order, and
//     must be as many as its function type's.
//
// The Core exporter marks each `_` in place, as the unsupported construct
// placeholder-lambda. lowerPlaceholders rewrites an argument A of a call C
// into an untyped lambda with one parameter per mark in A (outside lambdas,
// case clauses and interpolated strings), in source order, each mark
// replaced by its parameter, where C is not inside a call argument up to the
// nearest enclosing lambda, case clause or top-level declaration, so no
// outer slot can take them. The translation then checks the rest:
//
//   - g.lambda accepts such a lambda only for a slot of a function type with
//     as many parameters;
//   - argExpect accepts a parameter of one inside a nested call argument only
//     when that argument's slot is known not to be a function type, so no
//     inner lambda can take it.
//
// Anywhere else it is reported (placeholder-lambda). Every other `_` stays
// as exported, and is reported where it is reached.

// placeholderParam starts the name of each parameter lowerPlaceholders
// makes; no GALA name starts with `_gala_` (GALA-E0097).
const placeholderParam = "_gala_placeholder"

// isPlaceholder reports whether n is the exporter's mark of a `_`.
func isPlaceholder(n coreexport.Node) bool {
	u, ok := n.(*coreexport.Unsupported)
	return ok && u.What == coreexport.UnsupportedPlaceholder
}

// argMarks are the `_` of argument a a placeholder lambda of it takes, in
// source order: the marks outside lambdas, case clauses and interpolated
// strings. ok is false when a call in it holds a `_` between brackets.
func argMarks(a coreexport.Node) (marks []*coreexport.Unsupported, ok bool) {
	ok = true
	var visit func(n coreexport.Node)
	visit = func(n coreexport.Node) {
		switch n := n.(type) {
		case *coreexport.Lambda, *coreexport.Case, *coreexport.Interp:
			return
		case *coreexport.Unsupported:
			if isPlaceholder(n) {
				marks = append(marks, n)
			}
			return
		case *coreexport.Call:
			if n.Bracket != nil && slices.ContainsFunc(n.Bracket.Exprs, hasPlaceholder) {
				ok = false
			}
		}
		walkChildren(n, visit)
	}
	visit(a)
	slices.SortFunc(marks, func(x, y *coreexport.Unsupported) int { return cmp.Compare(x.Pos.Off, y.Pos.Off) })
	return marks, ok
}

// hasNode reports whether n or a node below it satisfies f.
func hasNode(n coreexport.Node, f func(coreexport.Node) bool) bool {
	found := false
	var scan func(n coreexport.Node)
	scan = func(n coreexport.Node) {
		found = found || f(n)
		if !found {
			walkChildren(n, scan)
		}
	}
	scan(n)
	return found
}

// hasPlaceholder reports whether n holds a `_` mark.
func hasPlaceholder(n coreexport.Node) bool { return hasNode(n, isPlaceholder) }

// hasPlaceholderParam reports whether n refers to a parameter of a lambda
// lowerPlaceholders made.
func hasPlaceholderParam(n coreexport.Node) bool {
	return hasNode(n, func(n coreexport.Node) bool {
		x, ok := n.(*coreexport.Name)
		return ok && strings.HasPrefix(x.Name, placeholderParam)
	})
}

// lowerPlaceholders is p with each placeholder argument that the rules
// above admit rewritten into a lambda; the lambdas are in the returned set.
func lowerPlaceholders(p *coreexport.Program) (*coreexport.Program, map[*coreexport.Lambda]bool) {
	l := &placeholderLowering{lambdas: map[*coreexport.Lambda]bool{}, marked: map[coreexport.Node]bool{}}
	out := *p
	out.Files = make([]*coreexport.File, len(p.Files))
	for i, f := range p.Files {
		out.Files[i] = f
		if l.mark(f) {
			out.Files[i] = l.rewrite(f, false).(*coreexport.File)
		}
	}
	return &out, l.lambdas
}

type placeholderLowering struct {
	lambdas map[*coreexport.Lambda]bool
	// marked holds the nodes with a `_` mark at or below them: only those
	// are rewritten (copied).
	marked map[coreexport.Node]bool
	fresh  int
}

// mark records in l.marked whether n holds a `_` mark, and reports it.
func (l *placeholderLowering) mark(n coreexport.Node) bool {
	has := isPlaceholder(n)
	walkChildren(n, func(c coreexport.Node) {
		if l.mark(c) {
			has = true
		}
	})
	if has {
		l.marked[n] = true
	}
	return has
}

// rewrite is n with its admitted placeholder arguments rewritten; inArg is
// whether n is inside a call argument within the current lambda, case
// clause or top-level declaration.
func (l *placeholderLowering) rewrite(n coreexport.Node, inArg bool) coreexport.Node {
	if !l.marked[n] {
		return n
	}
	switch n := n.(type) {
	case *coreexport.Lambda, *coreexport.Case:
		// A new context: its body's `_` are its own.
		return mapChildren(n, func(c coreexport.Node) coreexport.Node { return l.rewrite(c, false) })
	case *coreexport.Call:
		out := *n
		out.Callee = l.rewrite(n.Callee, inArg)
		out.Args = make([]coreexport.Node, len(n.Args))
		for i, a := range n.Args {
			if !inArg && l.marked[a] {
				if lam := l.lambdaOf(a); lam != nil {
					out.Args[i] = withArg(a, lam)
					continue
				}
			}
			out.Args[i] = l.rewrite(a, true)
		}
		return &out
	}
	return mapChildren(n, func(c coreexport.Node) coreexport.Node { return l.rewrite(c, inArg) })
}

// lambdaOf is argument a, of a call outside any call argument, as a lambda
// of a parameter per `_` it holds (argMarks), else nil.
func (l *placeholderLowering) lambdaOf(a coreexport.Node) *coreexport.Lambda {
	v := argNode(a)
	if _, spread := v.(*coreexport.Spread); spread {
		return nil
	}
	marks, ok := argMarks(v)
	if !ok || len(marks) == 0 {
		return nil
	}
	pos := posOf(v)
	names := map[*coreexport.Unsupported]string{}
	params := make([]coreexport.Node, len(marks))
	for i, m := range marks {
		l.fresh++
		names[m] = placeholderParam + strconv.Itoa(l.fresh)
		params[i] = &coreexport.Param{Meta: coreexport.Meta{Kind: "param", Pos: pos}, Name: names[m]}
	}
	var subst func(n coreexport.Node) coreexport.Node
	subst = func(n coreexport.Node) coreexport.Node {
		if m, ok := n.(*coreexport.Unsupported); ok && names[m] != "" {
			return &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: m.Pos}, Name: names[m]}
		}
		return mapChildren(n, subst)
	}
	// Its lambdas and case clauses are contexts of their own.
	body := subst(v)
	l.mark(body)
	lam := &coreexport.Lambda{
		Meta:   coreexport.Meta{Kind: "lambda", Pos: pos},
		Params: params,
		Body:   l.rewrite(body, true),
	}
	l.lambdas[lam] = true
	return lam
}

// withArg is argument a with its value replaced by v.
func withArg(a, v coreexport.Node) coreexport.Node {
	if na, ok := a.(*coreexport.NamedArg); ok {
		out := *na
		out.Value = v
		return &out
	}
	return v
}
