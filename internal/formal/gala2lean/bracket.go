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
// are passed to Lean by name (`f (T := Int64)`, `GArray.mk (α := Int64)`),
// so Lean takes them as written instead of inferring them; a type argument
// the arguments contradict is a Lean error the run reports, never a
// different value.

// bracketKind is what callee x of a call written with a bracket is: a
// standard-library constructor ("std"), a collection builder ("seq"), a
// generic function or constructor of the program ("decl"), or "" for
// anything else, whose bracket is an index. A HashMap or HashSet builder is
// reported for its order (hash-order), as without a bracket.
func (g *gen) bracketKind(e *coreexport.Call, sc *scope) (string, string) {
	n, ok := e.Callee.(*coreexport.Name)
	if !ok || sc.lookup(n.Name) != nil {
		return "", ""
	}
	x := n.Name
	switch {
	case stdCtors[x] != "" && !g.declared(x):
		return x, "std"
	case seqCtors[x] != "" && g.libName(x, true):
		return x, "seq"
	case g.collections && hashTypes[x] && !g.declared(x):
		unsup(e.Pos, "hash-order")
	case len(g.bracketParams(x)) > 0:
		return x, "decl"
	}
	return x, ""
}

// bracketParams is the type parameters of generic function or constructor
// x of the program, nil for any other name.
func (g *gen) bracketParams(x string) []string {
	if f := g.funcs[x]; f != nil {
		return typeParamNames(f.TypeParams)
	}
	if c := g.ctorOf(x); c != "" {
		return g.typeParamNamesOf(c)
	}
	return nil
}

// stdBracketType is the GALA type std constructor or collection builder x
// builds: Option, Either, Array or List.
func stdBracketType(x string) string {
	switch x {
	case "Some", "None":
		return "Option"
	case "Left", "Right":
		return "Either"
	}
	return map[string]string{"GArray": "Array", "GList": "List"}[seqCtors[x]]
}

// bracketCall translates a call written with a bracket.
func (g *gen) bracketCall(e *coreexport.Call, sc *scope) term {
	x, kind := g.bracketKind(e, sc)
	// A default is translated where the caller's locals and type parameters
	// are, not the callee's, which its bracket may name.
	if kind == "" || e.Bracket.Types == nil || len(g.inDefault) > 0 {
		unsup(e.Pos, "index")
	}
	var params []string
	switch kind {
	case "std":
		params = []string{"α"}
		if x == "Left" || x == "Right" {
			params = append(params, "β")
		}
	case "seq":
		params = []string{"α"}
	default:
		for _, tp := range g.bracketParams(x) {
			params = append(params, g.tpLean(tp))
		}
	}
	if len(params) != len(e.Bracket.Types) {
		unsup(e.Pos, "argument-count")
	}
	types := make([]string, len(params))
	var named []string
	for i, t := range e.Bracket.Types {
		types[i] = g.leanType(t)
		named = append(named, fmt.Sprintf("(%s := %s)", params[i], types[i]))
	}
	given := strings.Join(named, " ")
	switch kind {
	case "std":
		return g.stdCtorAt(stdCtors[x]+" "+given, x, e, sc)
	case "seq":
		return g.seqCtorAt(seqCtors[x]+".mk "+given, x, e, sc)
	}
	s := g.sigOf(x)
	if s.inout != nil {
		unsup(e.Pos, "index")
	}
	m := map[string]string{}
	for i, tp := range g.bracketParams(x) {
		m[tp] = types[i]
	}
	g.substSig(s, m, e.Pos)
	s.head = "(" + s.head + " " + given + ")"
	return g.callSig(s, e, sc)
}

// bracketCallType is exprType of a call written with a bracket, "" when it
// is not one the translation takes.
func (g *gen) bracketCallType(e *coreexport.Call, sc *scope) string {
	t, _ := attempt(func() string {
		x, kind := g.bracketKind(e, sc)
		if kind == "" || e.Bracket.Types == nil {
			return ""
		}
		switch kind {
		case "std", "seq":
			return g.leanType(&coreexport.TypeName{Name: stdBracketType(x), Args: e.Bracket.Types})
		}
		m := map[string]string{}
		params := g.bracketParams(x)
		if len(params) != len(e.Bracket.Types) {
			return ""
		}
		for i, tp := range params {
			m[tp] = g.leanType(e.Bracket.Types[i])
		}
		if f := g.funcs[x]; f != nil {
			if f.Result == nil {
				return ""
			}
			return g.instType(f.Result, m)
		}
		return g.leanType(&coreexport.TypeName{Name: g.ctorOf(x), Args: e.Bracket.Types})
	})
	return t
}
