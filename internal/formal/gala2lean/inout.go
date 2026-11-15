package gala2lean

import (
	"fmt"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// `inout` parameters (docs/OWNERSHIP.MD). The transpiler lowers
// `inout c T` to the Go parameter `c *T` and the argument `&place` to the
// place's address, so the callee reads and writes the caller's place
// directly. They are translated by state passing, as pointer receivers are
// (methods.go):
//
//   - A function with inout parameters p1..pn and result R is the Lean
//     function `f ... : _gala_M (R × T1 × ... × Tn)`: it takes each lent
//     place's value, keeps it in a cell for the body, and returns the
//     final values beside its result (lendBody).
//   - A call evaluates the other arguments in the order written, then reads
//     the lent places, calls f, and writes each final value back to its
//     place, in parameter order (lendSig).
//
// This is exact when nothing but the callee reaches a lent place during the
// call, and the translation checks that, as for a pointer receiver: a
// place is a local var no lambda captures, a field path in one, or an inout
// parameter or receiver of the caller (placeKind); the lent places of one
// call are disjoint; and the callee uses a parameter only directly, never
// in a lambda (receiverUses). GALA rejects overlapping places (GALA-E0112)
// and captures of the place (GALA-E0113) itself; the translation checks
// again rather than rely on it.

// lendCall is lendSig for e when it calls a top-level function with inout
// parameters, and nil items otherwise.
func (g *gen) lendCall(e *coreexport.Call, sc *scope) ([]string, string) {
	n, ok := e.Callee.(*coreexport.Name)
	if !ok || sc.lookup(n.Name) != nil || g.funcs[n.Name] == nil || !hasInout(g.funcs[n.Name].Params) {
		return nil, ""
	}
	return g.lendSig(n.Name, g.sigOf(n.Name), e, sc)
}

// lendSig translates call e of top-level function x, of signature s, which
// has inout parameters: the do-items that run it and write its places
// back, and the name holding its result.
func (g *gen) lendSig(x string, s *sig, e *coreexport.Call, sc *scope) ([]string, string) {
	given := bindArgs(s, e.Args, e.Pos)
	s.lent = map[int]coreexport.Node{}
	var places []coreexport.Node
	for k, a := range given {
		if !s.inout[k] {
			continue
		}
		if a < 0 {
			// A default lends no place of the caller (GALA rejects one).
			unsup(e.Pos, "inout-arg")
		}
		ad, ok := argNode(e.Args[a]).(*coreexport.AddressOf)
		if !ok {
			unsup(posOf(e.Args[a]), "inout-arg")
		}
		q := ad.Operand
		g.uniquePlace(q, sc, posOf(q), "inout-arg")
		for _, o := range places {
			if overlaps(o, q) {
				unsup(posOf(q), "ptr-escape")
			}
		}
		s.lent[a] = q
		places = append(places, q)
	}
	if f := g.funcs[x]; len(f.TypeParams) > 0 {
		// Type arguments are inferred from the values the places hold.
		plain := *e
		plain.Args = slices.Clone(e.Args)
		for a, q := range s.lent {
			if na, ok := e.Args[a].(*coreexport.NamedArg); ok {
				named := *na
				named.Value = q
				plain.Args[a] = &named
			} else {
				plain.Args[a] = q
			}
		}
		g.instSig(f, s, &plain, sc)
	}
	return g.writeBack(s, e, places, sc)
}

// overlaps reports whether places a and b share memory: the same variable,
// with one field path a prefix of the other.
func overlaps(a, b coreexport.Node) bool {
	ra, pa := placeRoot(a)
	rb, pb := placeRoot(b)
	na, okA := ra.(*coreexport.Name)
	nb, okB := rb.(*coreexport.Name)
	if !okA || !okB || na.Name != nb.Name {
		return false
	}
	n := min(len(pa), len(pb))
	return slices.Equal(pa[:n], pb[:n])
}

// cell is a lent place inside the function it is lent to (an inout
// parameter, or a state-passed receiver): the Lean parameter holding the
// value lent, and the cell, in heap heap of Lean type typ, where the body
// reads and writes it.
type cell struct {
	name, in string
	heap     int
	typ      string
}

// bindCell binds name, of GALA type gt and Lean type lt, in sc to a cell
// for the value of Lean parameter in: a unique place (placeKind), which the
// body writes at any depth.
func (g *gen) bindCell(name string, gt coreexport.Node, lt, in string, pos coreexport.Pos, sc *scope) cell {
	c := cell{name: name, in: in, heap: g.heap(lt, pos), typ: lt}
	l := sc.bind(name, true, lt)
	l.box, l.gtype, l.receiver = c.heap, gt, true
	return c
}

// funcParams are a top-level function's parameters in Lean, bound in sc,
// and its inout parameters' cells.
func (g *gen) funcParams(f *coreexport.Func, sc *scope) ([]string, []cell) {
	var ps []string
	var cells []cell
	for _, pn := range f.Params {
		p := g.paramOf(pn)
		if !p.Inout {
			ps = append(ps, g.param(p, sc))
			continue
		}
		if p.Default != nil {
			unsup(p.Pos, "param-default")
		}
		g.receiverUses(f, p.Name, true)
		c := g.bindCell(p.Name, p.Type, g.leanType(p.Type), fmt.Sprintf("_gala_in%d", len(cells)), p.Pos, sc)
		ps = append(ps, fmt.Sprintf("(%s : %s)", c.in, c.typ))
		cells = append(cells, c)
	}
	return ps, cells
}

// lendBody is body, the do-items of a function with lent places cells and
// result type res, wrapped to put each lent value in its cell first and
// return the final values beside the result; it returns the wrapped items
// and the Lean result type.
func lendBody(body []string, cells []cell, res string) ([]string, string) {
	var items, finals, types []string
	for _, c := range cells {
		items = append(items, fmt.Sprintf("let %s ← _gala_new%d %s", ident(c.name), c.heap, c.in))
		finals = append(finals, fmt.Sprintf("← _gala_get%d %s", c.heap, ident(c.name)))
		types = append(types, c.typ)
	}
	items = append(items,
		"let _gala_res ← "+doTerm(body),
		fmt.Sprintf("pure (_gala_res, %s)", strings.Join(finals, ", ")))
	return items, "(" + res + " × " + strings.Join(types, " × ") + ")"
}
