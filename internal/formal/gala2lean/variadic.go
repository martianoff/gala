package gala2lean

import (
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Variadic parameters. A function's or method's last parameter `xs ...T` is,
// in Lean, a List T: Go's []T of the arguments, in order. A call passes the
// arguments at and after that position, evaluated in the order written with
// the others, as one list; none (or the parameter left out of a call with
// named arguments) is []; `xs = v` is [v]; a spread `f(ys...)` passes ys.
// A named spread, `xs = ys...`, is reported (spread-arg): the generated Go
// passes ys without the spread, which does not compile (#1214).
//
// The only uses of the parameter that translate are a spread of it into a
// declared variadic function or into ArrayOf or ListOf, and `xs.Size()`
// (Go's len, whatever T is). Any other use is reported (slice-value), so no
// slice reaches anything that could write it, compare it or print it: Go's
// nil slice for no arguments, and the aliasing of a spread slice, cannot be
// observed. A spread of anything else is reported (spread-arg), and so is a
// variadic function used as a value (variadic-func-value).

// sliceHead is the Lean type head of a variadic parameter's slice. A GALA
// type named List is List' in Lean (names.go), so it is never this.
const sliceHead = "List"

// sliceType is the Lean type of a slice of Lean type elem.
func sliceType(elem string) string {
	return "(" + sliceHead + " " + elem + ")"
}

// sliceElem is the element type of Lean slice type t, or "" when t is not one.
func sliceElem(t string) string {
	p := parseLean(t)
	if p.head != sliceHead || len(p.args) != 1 || p.result != nil {
		return ""
	}
	return p.args[0].String()
}

// isVariadic reports whether f's last parameter is variadic.
func isVariadic(params []coreexport.Node) bool {
	if len(params) == 0 {
		return false
	}
	p, ok := params[len(params)-1].(*coreexport.Param)
	return ok && p.Variadic
}

// packed is the arguments a call gives a variadic parameter: elems, or the
// slice spread, one list in Lean. It exists only inside the translation.
type packed struct {
	elems  []coreexport.Node
	spread coreexport.Node
}

func (*packed) Kind() string { return "packed" }

// packCall is call e of s, a callee with a variadic last parameter, with
// the arguments that parameter takes in one packed argument, where the
// parameter's own argument would be: in place of the positional arguments
// it takes, or as a named argument, last, when it takes none.
func packCall(s *sig, e *coreexport.Call) *coreexport.Call {
	k := len(s.names) - 1
	name := s.names[k]
	nPos := slices.IndexFunc(e.Args, func(a coreexport.Node) bool {
		_, ok := a.(*coreexport.NamedArg)
		return ok
	})
	if nPos < 0 {
		nPos = len(e.Args)
	}
	out := *e
	if nPos > k {
		p := pack(e.Args[k:nPos])
		out.Args = append(append(slices.Clone(e.Args[:k]), p), e.Args[nPos:]...)
		return &out
	}
	out.Args = slices.Clone(e.Args)
	for i, a := range out.Args {
		if na, ok := a.(*coreexport.NamedArg); ok && na.Name == name {
			// `xs = v` is the one value v.
			if sp, ok := na.Value.(*coreexport.Spread); ok {
				unsup(sp.Pos, "spread-arg")
			}
			n := *na
			n.Value = pack([]coreexport.Node{na.Value})
			out.Args[i] = &n
			return &out
		}
	}
	out.Args = append(out.Args, &coreexport.NamedArg{Meta: coreexport.Meta{Pos: e.Pos}, Name: name,
		Value: &packed{}})
	return &out
}

// pack is the packed argument of args. A spread is the only argument, as
// in Go.
func pack(args []coreexport.Node) *packed {
	for _, a := range args {
		if sp, ok := a.(*coreexport.Spread); ok {
			if len(args) != 1 {
				unsup(sp.Pos, "spread-arg")
			}
			return &packed{spread: sp.Value}
		}
	}
	return &packed{elems: args}
}

// packTerm is packed argument p of elements of GALA type elem: the list of
// its elements, evaluated in order, or the spread slice.
func (g *gen) packTerm(p *packed, sc *scope, elem coreexport.Node) term {
	if p.spread != nil {
		return g.spreadTerm(p.spread, sc)
	}
	parts := make([]string, len(p.elems))
	subs := make([]term, len(p.elems))
	for i, a := range p.elems {
		subs[i] = g.exprExpect(a, sc, elem)
		parts[i] = subs[i].val()
	}
	code := "[" + strings.Join(parts, ", ") + "]"
	if len(p.elems) == 0 && elem != nil {
		// No element tells Lean the type.
		if lt := g.optTypeSafe(elem); lt != "" {
			code = "([] : " + sliceType(lt) + ")"
		}
	}
	return wrap(code, subs...)
}

// isSliceParam reports whether e is a name that refers to a variadic
// parameter.
func isSliceParam(e coreexport.Node, sc *scope) bool {
	n, ok := e.(*coreexport.Name)
	if !ok || sc == nil {
		return false
	}
	l := sc.lookup(n.Name)
	return l != nil && l.slice
}

// spreadTerm is the slice `v...` spreads: a variadic parameter, the only
// slice the translation has.
func (g *gen) spreadTerm(v coreexport.Node, sc *scope) term {
	if !isSliceParam(v, sc) {
		unsup(posOf(v), "spread-arg")
	}
	return term{code: ident(v.(*coreexport.Name).Name), pure: true}
}

// sliceSize is `xs.Size()` for a variadic parameter xs: Go's len(xs), which
// the transpiler lowers it to for every element type; ok is false for any
// other call.
func (g *gen) sliceSize(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	if s.Name != "Size" || len(e.Args) != 0 || !isSliceParam(s.Target, sc) {
		return term{}, false
	}
	return term{code: "(seqSize " + ident(s.Target.(*coreexport.Name).Name) + ")", pure: true}, true
}

// firstArg is call e's first argument, or nil when it has none.
func firstArg(e *coreexport.Call) coreexport.Node {
	if len(e.Args) == 0 {
		return nil
	}
	return e.Args[0]
}

// variadicMethodNamed reports whether some method named m has a variadic
// parameter: a cheap test before isVariadicMethod types a receiver.
func (g *gen) variadicMethodNamed(m string) bool {
	for _, ms := range g.methods {
		if f := ms[m]; f != nil && isVariadic(f.Params) {
			return true
		}
	}
	return false
}

// isVariadicMethod reports whether method m of the type of value e has a
// variadic parameter, or may have: when e's type cannot be read, a method of
// that name is.
func (g *gen) isVariadicMethod(e coreexport.Node, m string, sc *scope) bool {
	f, u := attempt(func() *coreexport.Func {
		f, _ := g.methodOf(e, m, sc, nil)
		return f
	})
	return u != nil || f != nil && isVariadic(f.Params)
}
