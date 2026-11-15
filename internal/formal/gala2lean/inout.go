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
//     final values beside its result.
//   - A call evaluates the other arguments in the order written, then reads
//     the lent places, calls f, and writes each final value back to its
//     place, in parameter order (lendCall).
//
// This is exact when nothing but the callee reaches a lent place during the
// call, and the translation checks that, as for a pointer receiver: a
// place is a local var no lambda captures, a field path in one, or an inout
// parameter or receiver of the caller (placeKind); the lent places of one
// call are disjoint; and the callee uses a parameter only directly, never
// in a lambda (receiverUses). GALA rejects overlapping places (GALA-E0112)
// and captures of the place (GALA-E0113) itself; the translation checks
// again rather than rely on it.

// lendCall translates a call of a top-level function with inout
// parameters: the do-items that run it and write its places back, and the
// name holding its result. It returns nil items when e is another call.
func (g *gen) lendCall(e *coreexport.Call, sc *scope) ([]string, string) {
	n, ok := e.Callee.(*coreexport.Name)
	if !ok || sc.lookup(n.Name) != nil {
		return nil, ""
	}
	f := g.funcs[n.Name]
	if f == nil || !hasInout(f.Params) {
		return nil, ""
	}
	s := g.sigOf(n.Name)
	given := bindArgs(s, e.Args, e.Pos)
	var places []coreexport.Node
	for k, a := range given {
		if !s.inout[k] {
			continue
		}
		// An inout parameter has no default, so a is an argument.
		q := lentPlace(e.Args[a])
		switch g.placeKind(q, sc) {
		case placeNone:
			unsup(posOf(q), "inout-arg")
		case placeShared:
			unsup(posOf(q), "ptr-escape")
		}
		for _, o := range places {
			if overlaps(o, q) {
				unsup(posOf(q), "ptr-escape")
			}
		}
		places = append(places, q)
	}
	if len(f.TypeParams) > 0 {
		// Type arguments are inferred from the values the places hold.
		plain := *e
		plain.Args = slices.Clone(e.Args)
		for k, a := range given {
			if s.inout[k] {
				plain.Args[a] = lentPlace(e.Args[a])
			}
		}
		g.instSig(f, s, &plain, sc)
	}
	items, call := g.callItems(s, e, sc, true)
	res := g.freshName("r")
	ups := make([]string, len(places))
	for i := range ups {
		ups[i] = g.freshName("u")
	}
	items = append(items, fmt.Sprintf("let (%s, %s) ← %s", res, strings.Join(ups, ", "), call.mon()))
	for i, q := range places {
		items = append(items, g.assignTo(q, func(string) string { return ups[i] }, sc, posOf(q))...)
	}
	return items, res
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

// cell is an inout parameter inside its function: the Lean parameter
// holding the value lent, and the heap of the cell the body uses.
type cell struct {
	name, in string
	heap     int
	typ      string
}

// funcParams are a top-level function's parameters in Lean, bound in sc,
// and its inout parameters, each bound to a cell (a unique place, as a
// pointer receiver is).
func (g *gen) funcParams(f *coreexport.Func, sc *scope) ([]string, []cell) {
	var ps []string
	var cells []cell
	for _, pn := range f.Params {
		p := g.paramOf(pn)
		if !p.Inout {
			ps = append(ps, g.param(p, sc))
			continue
		}
		g.receiverUses(f, p.Name, true)
		lt := g.leanType(p.Type)
		c := cell{name: p.Name, in: fmt.Sprintf("_gala_in%d", len(cells)), heap: g.heap(lt, p.Pos), typ: lt}
		l := sc.bind(p.Name, true, lt)
		l.box, l.gtype, l.receiver = c.heap, p.Type, true
		ps = append(ps, fmt.Sprintf("(%s : %s)", c.in, lt))
		cells = append(cells, c)
	}
	return ps, cells
}

// lendBody is body, the do-items of a function with inout parameters
// cells and result type res, wrapped to put each lent value in its cell
// first and return the final values beside the result; it returns the
// wrapped items and the Lean result type.
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
