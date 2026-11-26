package gala2lean

import (
	"strconv"

	"martianoff/gala/internal/formal/coreexport"
)

// Placeholder lambdas. A call argument whose slot wants a function and that
// contains `_` is a lambda of one parameter per `_` (docs/GALA.MD,
// "Placeholder Lambda Shorthand"). The rules here follow the transformer's
// code (internal/transpiler/transformer; lines at the time of writing):
// tryRewriteAsPlaceholderLambda (lambdas.go:1551) rewrites an argument when
// its expected type is a function type and countPlaceholders
// (args_source.go:223) finds a `_` in it, counting through the whole
// argument, nested call arguments included but not nested lambdas or case
// clauses; transformArgument (calls.go:5116) tries it on an argument before
// lowering what is inside, so the outermost argument whose slot is a
// function type takes the `_`. The Core exporter marks each `_` in place,
// as the unsupported construct placeholder-lambda.
//
// lowerPlaceholders rewrites an argument A of a call C into an untyped
// lambda of one parameter p, its `_` replaced by p, only where that choice
// cannot differ from the transformer's whatever the slot types are, and
// where the transformer's rewrite finds the `_`:
//
//   - A is built only of its one `_`, literals, names, parentheses, unary and
//     binary operators, selects, interpolated strings of these, and calls
//     whose callee holds the `_` (`_.Contains(1)`) and whose arguments are
//     literals or names. So A has exactly one `_` token, and the order of
//     parameters never matters; no inner slot can take the `_`; and nothing
//     in A lowers to a function literal (an if or match expression, a
//     block, an evaluation-order wrapper), whose `_` the transformer's
//     renaming does not reach (renameWalk in tryRewriteAsPlaceholderLambda
//     stops at a function literal; Go then rejects the program);
//   - C is not inside a call argument, up to the nearest enclosing lambda,
//     case clause or top-level declaration, so no outer slot can take it.
//
// The translation then accepts such a lambda only for a slot of a function
// type of one parameter (g.lambda), as the transformer does; anywhere else
// it is reported (placeholder-lambda). Every other `_` stays as exported,
// and is reported where it is reached.

// isPlaceholder reports whether n is the exporter's mark of a `_`.
func isPlaceholder(n coreexport.Node) bool {
	u, ok := n.(*coreexport.Unsupported)
	return ok && u.What == coreexport.UnsupportedPlaceholder
}

// soleMark is the one `_` of argument a when a has the form the rules above
// admit, else nil.
func soleMark(a coreexport.Node) *coreexport.Unsupported {
	var mark *coreexport.Unsupported
	ok := true
	// visit checks n; onPath is whether n may hold the mark (a call's
	// arguments may not).
	var visit func(n coreexport.Node, onPath bool)
	visit = func(n coreexport.Node, onPath bool) {
		switch n := n.(type) {
		case *coreexport.Unsupported:
			if !isPlaceholder(n) || !onPath || mark != nil {
				ok = false
				return
			}
			mark = n
		case *coreexport.Int, *coreexport.Bool, *coreexport.String, *coreexport.Name:
		case *coreexport.Paren:
			visit(n.Expr, onPath)
		case *coreexport.Unary:
			visit(n.Operand, onPath)
		case *coreexport.Binary:
			visit(n.Left, onPath)
			visit(n.Right, onPath)
		case *coreexport.Select:
			visit(n.Target, onPath)
		case *coreexport.Interp:
			// A `_` in it is inside a string token for the transformer, which
			// does not count it (Go then rejects it).
			walkChildren(n, func(c coreexport.Node) { visit(c, false) })
		case *coreexport.InterpText:
		case *coreexport.Call:
			if n.Bracket != nil {
				ok = false
				return
			}
			visit(n.Callee, onPath)
			for _, x := range n.Args {
				switch argNode(x).(type) {
				case *coreexport.Int, *coreexport.Bool, *coreexport.String, *coreexport.Name:
				default:
					ok = false
				}
			}
		default:
			ok = false
		}
	}
	visit(a, true)
	if !ok {
		return nil
	}
	return mark
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
// when it has the admitted form (soleMark), else nil.
func (l *placeholderLowering) lambdaOf(a coreexport.Node) *coreexport.Lambda {
	v := argNode(a)
	mark := soleMark(v)
	if mark == nil {
		return nil
	}
	l.fresh++
	// No GALA name starts with `_gala_` (GALA-E0097).
	name := "_gala_placeholder" + strconv.Itoa(l.fresh)
	var subst func(n coreexport.Node) coreexport.Node
	subst = func(n coreexport.Node) coreexport.Node {
		if n == coreexport.Node(mark) {
			return &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: mark.Pos}, Name: name}
		}
		return mapChildren(n, subst)
	}
	pos := posOf(v)
	lam := &coreexport.Lambda{
		Meta:   coreexport.Meta{Kind: "lambda", Pos: pos},
		Params: []coreexport.Node{&coreexport.Param{Meta: coreexport.Meta{Kind: "param", Pos: pos}, Name: name}},
		Body:   subst(v),
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
