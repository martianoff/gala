package gala2lean

import (
	"cmp"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Placeholder lambdas. A call argument whose slot wants a function and that
// contains `_` is a lambda of one parameter per `_` (docs/GALA.MD,
// "Placeholder Lambda Shorthand"). The rules here follow the transformer's
// code (internal/transpiler/transformer; lines at the time of writing):
//
//   - transformArgument (calls.go:5359) tries tryRewriteAsPlaceholderLambda
//     (lambdas.go:1621) on a call argument, a by-name argument (calls.go:783)
//     or a parameter's default value (default_exprs.go:785), before lowering
//     what is inside; it rewrites when the slot is a function type and
//     visitPlaceholders (args_source.go:233) finds a `_`: every `_` written
//     in it, in source order, outside nested lambdas and case clauses,
//     inside if and match expressions and blocks too (a `_` in `${...}` is
//     inside a string token there, and is never found). No other context
//     (a typed val, an assignment, a return) starts a rewrite;
//   - while the body is lowered, a nested argument whose slot is a function
//     type makes a lambda of its own, binding `_` closer: a `_` belongs to
//     the innermost such argument around it (placeholderTakes.take,
//     lambdas.go:1559);
//   - the lambda's parameters are the `_` it takes, in source order, and
//     must be as many as its function type's.
//
// The Core exporter marks each `_` in place, as the unsupported construct
// placeholder-lambda. gala2lean decides at each argument, where its slot
// type is known (argExpect):
//
//   - for a slot of a function type with k parameters, an argument holding
//     `_` marks (argMarks) is the lambda placeholderLambda makes of it: a
//     parameter per mark, in source order, each mark replaced by its
//     parameter; it is translated only when it has k marks (g.lambda);
//   - inside such a lambda's body, an argument or other typed context that
//     reads one of its parameters must have a slot known not to be a
//     function type (placeholderArg): else the transformer's innermost rule
//     might give that `_` to a lambda of its own, or the slot is unknown;
//   - outside any, an argument holding marks must have a slot known not to
//     be a function type, or be the lambda's own (a function slot): the
//     marks are then left to the arguments nested in it.
//
// So no function slot lies between a `_` and the argument whose lambda
// takes it, nor around that argument up to the nearest lambda or case
// clause, and Go's innermost rule gives the `_` to the same lambda. Every
// other `_` stays as exported, and is reported where it is reached
// (placeholder-lambda).

// placeholderParam starts the name of each parameter placeholderLambda
// makes (freshName); no GALA name starts with `_gala_` (GALA-E0097).
const placeholderParam = "_gala_placeholder"

// anyType is the slot type of Println's and Print's arguments.
var anyType = &coreexport.TypeName{Name: "any"}

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
// placeholderLambda made.
func hasPlaceholderParam(n coreexport.Node) bool {
	return hasNode(n, func(n coreexport.Node) bool {
		x, ok := n.(*coreexport.Name)
		return ok && strings.HasPrefix(x.Name, placeholderParam)
	})
}

// placeholderLambda is the lambda the transformer makes of argument a for
// a slot of type expect: when expect is a function type and a, not a lambda
// itself, holds `_` marks. It is made once per argument, so the translation
// and the type queries see the same one.
func (g *gen) placeholderLambda(a coreexport.Node, expect coreexport.Node) (*coreexport.Lambda, bool) {
	if !g.hasMarks {
		return nil, false
	}
	a = argNode(a)
	if _, isFunc := expect.(*coreexport.TypeFunc); !isFunc {
		return nil, false
	}
	if l, ok := g.placeholderOf[a]; ok {
		return l, l != nil
	}
	g.placeholderOf[a] = nil
	switch a.(type) {
	case *coreexport.Lambda, *coreexport.Spread:
		return nil, false
	}
	marks, ok := argMarks(a)
	if !ok || len(marks) == 0 {
		return nil, false
	}
	pos := posOf(a)
	names := map[*coreexport.Unsupported]string{}
	params := make([]coreexport.Node, len(marks))
	for i, m := range marks {
		names[m] = g.freshName("placeholder")
		params[i] = &coreexport.Param{Meta: coreexport.Meta{Kind: "param", Pos: pos}, Name: names[m]}
	}
	// Only the nodes on a path to a mark are copied: the rest is shared
	// with the original tree, so what the passes before the translation
	// keyed by node (findBoxes's cells, the constant folds) still applies.
	onPath := map[coreexport.Node]bool{}
	var find func(n coreexport.Node) bool
	find = func(n coreexport.Node) bool {
		m, isMark := n.(*coreexport.Unsupported)
		found := isMark && names[m] != ""
		walkChildren(n, func(c coreexport.Node) { found = find(c) || found })
		onPath[n] = found
		return found
	}
	find(a)
	var subst func(n coreexport.Node) coreexport.Node
	subst = func(n coreexport.Node) coreexport.Node {
		if m, ok := n.(*coreexport.Unsupported); ok && names[m] != "" {
			return &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: m.Pos}, Name: names[m]}
		}
		if !onPath[n] {
			return n
		}
		c := mapChildren(n, subst)
		if v, ok := n.(*coreexport.Var); ok {
			// `var x = _`, copied: the copy binds x in a cell when the
			// original does.
			for _, x := range v.Names {
				if g.boxed[boxKey{n, x}] {
					g.boxed[boxKey{c, x}] = true
				}
			}
		}
		return c
	}
	l := &coreexport.Lambda{Meta: coreexport.Meta{Kind: "lambda", Pos: pos}, Params: params, Body: subst(a)}
	g.placeholderOf[a], g.placeholders[l] = l, true
	return l, true
}

// argExpect translates a, a call argument or a parameter's default value,
// for a slot of type expect (nil when unknown): as the lambda the
// transformer makes of it when it is a placeholder lambda, else as
// exprExpect does.
func (g *gen) argExpect(a coreexport.Node, sc *scope, expect coreexport.Node) term {
	g.placeholderArg(a, expect)
	if l, ok := g.placeholderLambda(a, expect); ok {
		return g.lambda(l, sc, expect.(*coreexport.TypeFunc))
	}
	return g.exprExpect(a, sc, expect)
}

// placeholderArg stops the translation of e, an argument or other typed
// context for a slot of type expect (nil when unknown), where the `_` it
// holds might not be the lambda's gala2lean gives them to: e reads a
// parameter of a placeholder lambda around it and its slot is not known to
// want something other than a function (the transformer might make a
// lambda of e that takes that `_`), or e holds `_` marks for a slot that is
// neither a function type nor known not to be one.
func (g *gen) placeholderArg(e coreexport.Node, expect coreexport.Node) {
	e = argNode(e)
	if !g.hasMarks {
		return
	}
	// Checked on every visit: the same argument is reached for different
	// slots (a type query, then its translation) and inside or outside a
	// placeholder lambda's body; argChecked only records that it was.
	g.argChecked[e] = true
	if l, ok := e.(*coreexport.Lambda); ok && g.placeholders[l] || g.nonFuncSlot(expect) {
		return
	}
	if g.inPlaceholder > 0 && hasPlaceholderParam(e) {
		unsup(posOf(e), "placeholder-lambda")
	}
	if _, isFunc := expect.(*coreexport.TypeFunc); !isFunc {
		if marks, _ := argMarks(e); len(marks) > 0 {
			unsup(posOf(e), "placeholder-lambda")
		}
	}
}

// nonFuncSlot reports whether t is a type that is plainly not a function
// type: int, int64, string, bool or any, a declared struct or sealed type,
// or one of std's generic types, and not a name the program declares as a
// type parameter anywhere: a callee's slot may still be written with its
// own type parameter (a generic receiver's method keeps it), which the call
// may make a function type.
func (g *gen) nonFuncSlot(t coreexport.Node) bool {
	n, ok := t.(*coreexport.TypeName)
	if !ok || n.Package != "" {
		return false
	}
	if _, param := g.tparams[n.Name]; param || g.typeParamNames[n.Name] {
		return false
	}
	switch n.Name {
	case "int", "int64", "string", "bool", "any":
		return true
	}
	_, std := stdGeneric[n.Name]
	return g.declaredType(n.Name) || std || hashTypes[n.Name]
}

// uncheckedArgs stops the translation of call e when an argument of it that
// holds `_` marks or reads a placeholder lambda's parameter was translated
// without placeholderArg's check: every path that translates an argument
// must check it (argExpect, or placeholderArg with the slot's type, nil when
// unknown). TestPlaceholderArgsChecked keeps this construct out of every
// program the tests translate.
func (g *gen) uncheckedArgs(e *coreexport.Call) {
	if !g.hasMarks {
		return
	}
	for _, a := range e.Args {
		n := argNode(a)
		if g.argChecked[n] {
			continue
		}
		if sp, ok := n.(*coreexport.Spread); ok {
			if n = sp.Value; g.argChecked[n] {
				continue
			}
		}
		if marks, _ := argMarks(n); len(marks) > 0 || g.inPlaceholder > 0 && hasPlaceholderParam(n) {
			unsup(posOf(n), "placeholder-unchecked-arg")
		}
	}
}

// declaredTypeParams is every name program p declares as a type parameter.
func declaredTypeParams(p *coreexport.Program) map[string]bool {
	names := map[string]bool{}
	var visit func(n coreexport.Node)
	visit = func(n coreexport.Node) {
		if tp, ok := n.(*coreexport.TypeParam); ok {
			names[tp.Name] = true
		}
		walkChildren(n, visit)
	}
	for _, f := range p.Files {
		visit(f)
	}
	return names
}

// markedArg reports whether a, a call argument or a parameter's default
// value, holds `_` marks (argMarks): the translation may make a placeholder
// lambda of it (argExpect). The passes that run before the translation
// (findBoxes, receiverUses) do not know the slot's type yet, so they take
// every such argument for a lambda: when its `_` go to a lambda nested in
// it instead, or to none, they only box or report more than they need to.
func (g *gen) markedArg(a coreexport.Node) bool {
	if !g.hasMarks || a == nil {
		return false
	}
	marks, _ := argMarks(argNode(a))
	return len(marks) > 0
}

// walkArgs calls f on each direct child of n (walkChildren), with lambda
// true for an argument of call n that markedArg says may be a placeholder
// lambda.
func (g *gen) walkArgs(n coreexport.Node, f func(c coreexport.Node, lambda bool)) {
	var marked map[coreexport.Node]bool
	if c, ok := n.(*coreexport.Call); ok && g.hasMarks {
		for _, a := range c.Args {
			if g.markedArg(a) {
				if marked == nil {
					marked = map[coreexport.Node]bool{}
				}
				marked[a] = true
			}
		}
	}
	walkChildren(n, func(c coreexport.Node) { f(c, marked[c]) })
}
