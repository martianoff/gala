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
// clauses; transformArgument (calls.go:4944) tries it on an argument before
// lowering what is inside, so the outermost argument whose slot is a
// function type takes the `_`. The Core exporter marks each `_` in place,
// as the unsupported construct placeholder-lambda.
//
// lowerPlaceholders rewrites an argument A of a call C into an untyped
// lambda of one parameter p, its `_` replaced by p, only where that choice
// cannot differ from the transformer's whatever the slot types are:
//
//   - A has exactly one `_`, so the order of parameters never matters;
//   - that `_` is not in the arguments of a call inside A, so no inner slot
//     can take it (it may be a receiver or an operand: `_.Name == n`);
//   - C is not inside a call argument, up to the nearest lambda, case or
//     declaration, so no outer slot can take it.
//
// The translation then accepts such a lambda only for a slot of a function
// type of one parameter (exprExpect), as the transformer does; anywhere else
// it is reported (placeholder-lambda). Every other `_` stays as exported,
// and is reported where it is reached.

// isPlaceholder reports whether n is the exporter's mark of a `_`.
func isPlaceholder(n coreexport.Node) bool {
	u, ok := n.(*coreexport.Unsupported)
	return ok && u.What == coreexport.UnsupportedPlaceholder
}

// placeholderCount counts the `_` of argument a as the transformer does
// (countPlaceholders, transformer/args_source.go:223): every
// `_` token outside lambdas and case clauses. A `_` in an interpolated
// string is inside a string token there, and is not counted (Go then
// rejects it). A wildcard pattern or a `_` type counts, as its token does.
// inArgs is how many of the marks are in the arguments of a call inside a.
func placeholderCount(a coreexport.Node) (all, inArgs int) {
	var visit func(n coreexport.Node, inArg bool)
	visit = func(n coreexport.Node, inArg bool) {
		switch n := n.(type) {
		case *coreexport.Lambda, *coreexport.Case, *coreexport.Interp:
			return
		case *coreexport.PatWild:
			all++
			return
		case *coreexport.TypeName:
			if n.Name == "_" {
				all++
			}
		case *coreexport.Call:
			visit(n.Callee, inArg)
			for _, x := range n.Args {
				visit(x, true)
			}
			return
		}
		if isPlaceholder(n) {
			all++
			if inArg {
				inArgs++
			}
			return
		}
		walkChildren(n, func(c coreexport.Node) { visit(c, inArg) })
	}
	visit(a, false)
	return all, inArgs
}

// lowerPlaceholders is p with each placeholder argument that the rules
// above admit rewritten into a lambda; the lambdas are in the returned set.
func lowerPlaceholders(p *coreexport.Program) (*coreexport.Program, map[*coreexport.Lambda]bool) {
	l := &placeholderLowering{lambdas: map[*coreexport.Lambda]bool{}}
	out := *p
	out.Files = make([]*coreexport.File, len(p.Files))
	for i, f := range p.Files {
		out.Files[i] = l.rewrite(f, false).(*coreexport.File)
	}
	return &out, l.lambdas
}

type placeholderLowering struct {
	lambdas map[*coreexport.Lambda]bool
	fresh   int
}

// rewrite is n with its admitted placeholder arguments rewritten; inArg is
// whether n is inside a call argument within the current lambda, case or
// declaration.
func (l *placeholderLowering) rewrite(n coreexport.Node, inArg bool) coreexport.Node {
	switch n := n.(type) {
	case nil:
		return nil
	case *coreexport.Lambda, *coreexport.Case:
		// A new context: its body's `_` are its own.
		return mapChildren(n, func(c coreexport.Node) coreexport.Node { return l.rewrite(c, false) })
	case *coreexport.Call:
		out := *n
		out.Callee = l.rewrite(n.Callee, inArg)
		out.Args = make([]coreexport.Node, len(n.Args))
		for i, a := range n.Args {
			if lam := l.lambdaOf(a, inArg); lam != nil {
				out.Args[i] = withArg(a, lam)
				continue
			}
			out.Args[i] = l.rewrite(a, true)
		}
		return &out
	}
	return mapChildren(n, func(c coreexport.Node) coreexport.Node { return l.rewrite(c, inArg) })
}

// lambdaOf is argument a as a lambda, when the rules admit it (inArg: the
// call is inside a call argument), else nil.
func (l *placeholderLowering) lambdaOf(a coreexport.Node, inArg bool) *coreexport.Lambda {
	v := argNode(a)
	if _, spread := v.(*coreexport.Spread); inArg || spread {
		return nil
	}
	if all, inArgs := placeholderCount(v); all != 1 || inArgs != 0 {
		return nil
	}
	l.fresh++
	// No GALA name starts with `_gala_` (GALA-E0097).
	name := "_gala_placeholder" + strconv.Itoa(l.fresh)
	var subst func(n coreexport.Node) coreexport.Node
	subst = func(n coreexport.Node) coreexport.Node {
		if isPlaceholder(n) {
			return &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: posOf(n)}, Name: name}
		}
		switch n.(type) {
		case nil, *coreexport.Lambda, *coreexport.Case, *coreexport.Interp:
			return n
		}
		return mapChildren(n, subst)
	}
	pos := posOf(v)
	lam := &coreexport.Lambda{
		Meta:   coreexport.Meta{Kind: "lambda", Pos: pos},
		Params: []coreexport.Node{&coreexport.Param{Meta: coreexport.Meta{Kind: "param", Pos: pos}, Name: name}},
		// Its lambdas and cases are contexts of their own; no call in it is
		// outside an argument (its one `_` was the transformer's).
		Body: l.rewrite(subst(v), true),
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
