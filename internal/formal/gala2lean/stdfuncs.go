package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Functions of std and of the immutable collections, and Go's int
// conversions, called by name. A name is taken as one of these only when
// it resolves to it: the program declares no such name, it dot-imports no
// package but std and the immutable collections (another, such as the
// mutable collections with their own ArrayTabulate, might be the one it
// means), and, for ArrayTabulate and ArrayFill, it dot-imports the
// immutable collections. Each is translated after the code it cites.

// stdPath is the import path of GALA's standard library.
const stdPath = "martianoff/gala/std"

// stdFuncs are the functions modelled here, with what each needs: "std"
// (std, always in scope), "seq" (the immutable collections) or "conv" (a
// Go conversion).
var stdFuncs = map[string]string{
	"When": "std", "Unless": "std", "Tuple": "std", "Tuple3": "std", "Tuple4": "std",
	"ArrayTabulate": "seq", "ArrayFill": "seq",
	"int": "conv", "int64": "conv",
}

// stdFunc reports whether name x, called, is one of stdFuncs as it resolves
// in this program.
func (g *gen) stdFunc(x string) bool {
	switch stdFuncs[x] {
	case "":
		return false
	case "seq":
		if !g.collections {
			return false
		}
	}
	return !g.declared(x) && !g.declaredType(x) && !g.otherDots
}

// stdFuncCall translates a call of stdFunc x.
func (g *gen) stdFuncCall(x string, e *coreexport.Call, sc *scope) term {
	for _, a := range e.Args {
		positional(a)
	}
	arity := map[string]int{"When": 2, "Unless": 2, "Tuple": 2, "Tuple3": 3, "Tuple4": 4,
		"ArrayTabulate": 2, "ArrayFill": 2, "int": 1, "int64": 1}[x]
	if len(e.Args) != arity {
		unsup(e.Pos, "argument-count")
	}
	switch x {
	case "int", "int64":
		// Go's conversion of an int or int64 to int or int64 keeps the
		// value: both are 64 bits, as galaspec takes Go's int to be
		// (docs/FORMAL_SEMANTICS.MD). Any other conversion is reported.
		if g.typeOf(e.Args[0], sc) != "Int64" {
			unsup(e.Pos, "conversion")
		}
		return g.expr(e.Args[0], sc)
	case "ArrayTabulate":
		n := g.exprExpect(e.Args[0], sc, &coreexport.TypeName{Name: "int"})
		f := g.exprExpect(e.Args[1], sc, &coreexport.TypeFunc{
			Params: []coreexport.Node{&coreexport.TypeName{Name: "int"}},
			Result: &coreexport.TypeName{Name: inferTypeArg}})
		return act(fmt.Sprintf("GArray.mk <$> seqTabulate %s %s", n.val(), f.val()))
	}
	args := make([]term, len(e.Args))
	vals := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = g.expr(a, sc)
		vals[i] = args[i].val()
	}
	var code string
	switch x {
	case "When":
		code = fmt.Sprintf("(optWhen %s %s)", vals[0], vals[1])
	case "Unless":
		code = fmt.Sprintf("(optWhen (!%s) %s)", vals[0], vals[1])
	case "ArrayFill":
		code = fmt.Sprintf("(GArray.mk (seqFill %s %s))", vals[0], vals[1])
	default: // Tuple, Tuple3, Tuple4
		code = fmt.Sprintf("(T%d.mk %s)", arity, strings.Join(vals, " "))
	}
	return wrap(code, args...)
}

// stdFuncType is exprType of a call of stdFunc x.
func (g *gen) stdFuncType(x string, e *coreexport.Call, sc *scope, seen map[string]bool) string {
	arg := func(i int) string {
		if i >= len(e.Args) {
			return ""
		}
		return g.exprType(argNode(e.Args[i]), sc, seen)
	}
	switch x {
	case "int", "int64":
		return "Int64"
	case "When", "Unless":
		if t := arg(1); t != "" {
			return "(GOption " + t + ")"
		}
	case "ArrayFill":
		if t := arg(1); t != "" {
			return "(GArray " + t + ")"
		}
	case "ArrayTabulate":
		if len(e.Args) == 2 {
			f := &coreexport.TypeFunc{Params: []coreexport.Node{&coreexport.TypeName{Name: "int"}},
				Result: &coreexport.TypeName{Name: inferTypeArg}}
			if r := g.funcArgResult(e.Args[1], f, sc, seen); r != "" {
				return "(GArray " + r + ")"
			}
		}
	default: // Tuple, Tuple3, Tuple4
		parts := make([]string, len(e.Args))
		for i := range e.Args {
			if parts[i] = arg(i); parts[i] == "" {
				return ""
			}
		}
		return fmt.Sprintf("(T%d %s)", len(e.Args), strings.Join(parts, " "))
	}
	return ""
}
