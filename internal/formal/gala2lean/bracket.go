package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Explicit type arguments. Core JSON gives a call written `X[items](args)`
// its bracket in two readings (coreexport.Bracket): the items as expressions
// and, when each reads as one, as types. Which they are depends on X: type
// arguments when X is a generic function, the constructor of a generic
// struct or sealed type, or a standard-library constructor the prelude
// models (Some, None, Left, Right, ArrayOf, ListOf, EmptyArray, EmptyList);
// an index otherwise (`fs[i](x)`), which is reported (index). Type arguments
// are passed to Lean by name (`f (T := Int64)`), so Lean takes them as
// written instead of inferring them; a type argument the arguments
// contradict is a Lean error the run reports, never a different value.

// bracketCall translates a call written with a bracket.
func (g *gen) bracketCall(e *coreexport.Call, sc *scope) term {
	x, types := g.bracketTypes(e, sc)
	switch {
	case stdCtors[x] != "" && !g.declared(x):
		return g.stdCtorTyped(x, types, e, sc)
	case g.collections && seqCtors[x] != "" && !g.declared(x):
		if len(types) != 1 {
			unsup(e.Pos, "argument-count")
		}
		t := g.seqCtor(x, e, sc)
		return wrap(fmt.Sprintf("(%s : %s %s)", t.val(), seqCtors[x], argType(types[0])), t)
	}
	s := g.sigOf(x)
	if s == nil || s.inout != nil {
		unsup(e.Pos, "index")
	}
	var tps []string
	if f := g.funcs[x]; f != nil {
		tps = typeParamNames(f.TypeParams)
	} else {
		tps = g.typeParamNamesOf(g.ctorOf(x))
	}
	if len(tps) == 0 {
		unsup(e.Pos, "index")
	}
	if len(tps) != len(types) {
		unsup(e.Pos, "argument-count")
	}
	m := map[string]string{}
	var named []string
	for i, tp := range tps {
		m[tp] = types[i]
		named = append(named, fmt.Sprintf("(%s := %s)", g.tpLean(tp), types[i]))
	}
	g.substSig(s, m, e.Pos)
	s.head = "(" + s.head + " " + strings.Join(named, " ") + ")"
	return g.callSig(s, e, sc)
}

// bracketTypes is the callee name of call e, written with a bracket, and
// the Lean types of its bracket's items; it reports the call (index) when
// the callee is not a name the program or the prelude declares or an item
// is not a type.
func (g *gen) bracketTypes(e *coreexport.Call, sc *scope) (string, []string) {
	n, ok := e.Callee.(*coreexport.Name)
	if !ok || sc.lookup(n.Name) != nil || e.Bracket.Types == nil {
		unsup(e.Pos, "index")
	}
	var types []string
	for _, t := range e.Bracket.Types {
		types = append(types, g.leanType(t))
	}
	return n.Name, types
}

// stdCtorTyped is stdCtor with Option's or Either's type arguments given.
func (g *gen) stdCtorTyped(x string, types []string, e *coreexport.Call, sc *scope) term {
	params := []string{"α"}
	if x == "Left" || x == "Right" {
		params = append(params, "β")
	}
	if len(types) != len(params) {
		unsup(e.Pos, "argument-count")
	}
	head := stdCtors[x]
	for i, p := range params {
		head += fmt.Sprintf(" (%s := %s)", p, types[i])
	}
	if x == "None" {
		if len(e.Args) != 0 {
			unsup(e.Pos, "argument-count")
		}
		return term{code: "(" + head + ")", pure: true}
	}
	if len(e.Args) != 1 {
		unsup(e.Pos, "argument-count")
	}
	positional(e.Args[0])
	v := g.expr(e.Args[0], sc)
	return wrap("("+head+" "+v.val()+")", v)
}

// bracketCallType is exprType of a call written with a bracket, "" when it
// is not one the translation takes.
func (g *gen) bracketCallType(e *coreexport.Call, sc *scope) string {
	n, ok := e.Callee.(*coreexport.Name)
	if !ok || e.Bracket.Types == nil || sc != nil && sc.lookup(n.Name) != nil {
		return ""
	}
	types := make([]string, len(e.Bracket.Types))
	for i, t := range e.Bracket.Types {
		types[i] = g.safeType(func() string { return g.leanType(t) })
		if types[i] == "" {
			return ""
		}
	}
	x := n.Name
	switch {
	case (x == "Some" || x == "None") && !g.declared(x) && len(types) == 1:
		return "(GOption " + types[0] + ")"
	case (x == "Left" || x == "Right") && !g.declared(x) && len(types) == 2:
		return "(GEither " + types[0] + " " + types[1] + ")"
	case g.collections && seqCtors[x] != "" && !g.declared(x) && len(types) == 1:
		return "(" + seqCtors[x] + " " + types[0] + ")"
	}
	var tps []string
	var result string
	if f := g.funcs[x]; f != nil {
		tps, result = typeParamNames(f.TypeParams), ""
		if len(tps) != len(types) || f.Result == nil {
			return ""
		}
		m := map[string]string{}
		for i, tp := range tps {
			m[tp] = types[i]
		}
		return g.instType(f.Result, m)
	}
	if c := g.ctorOf(x); c != "" {
		tps = g.typeParamNamesOf(c)
		if len(tps) != len(types) || len(tps) == 0 {
			return ""
		}
		result = "(" + ident(c) + " " + strings.Join(argTypes(types), " ") + ")"
	}
	return result
}

// argType is Lean type t as one argument of a type: in parentheses unless
// it is one word, as the translation writes types (`(GOption Int64)`).
func argType(t string) string {
	if strings.ContainsAny(t, " ") && !strings.HasPrefix(t, "(") {
		return parenthesize(t)
	}
	return t
}

// argTypes is argType of each of ts.
func argTypes(ts []string) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = argType(t)
	}
	return out
}
