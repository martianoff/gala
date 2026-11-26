package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Option and Either methods. A call of a method of std's Option
// (std/option.gala) or Either (std/either.gala) on a value whose type is
// plainly one is translated as the prelude's model of that method
// (formal/Gala/Gala2Lean/Prelude.lean), each after the std code it cites:
// the receiver, then the arguments in the order written, as Go evaluates a
// method call's, then the method, which calls a function argument only
// where std does. Where std panics (Get on None, GetLeft on a Right, ...)
// the model throws Panic.std, class `other`, as Go's run of it is classed.
// Any other method of the two types is reported (option-method).

// optMethod is one method of Option or Either the prelude models.
type optMethod struct {
	// params are the parameter types, over T (Option's value type), A and B
	// (Either's), O (the receiver's own type), Bool, and function types of
	// those: `T->` without a result, `T->Bool`, `T->_` with a result Lean
	// infers, `T->O_` returning an Option of one, `B->E_` returning an
	// Either of one, `->` taking nothing.
	params []string
	// code is the Lean code of the call, over {r} (the receiver) and {0},
	// {1} (the arguments).
	code string
	// pure is true when the code is a value, not an action.
	pure bool
	// result is the result's type: T, A, B, O (the receiver's type), Bool,
	// W (the Either swapped), F (a function argument's result), M (Option or
	// Either of that result), or "" for Unit.
	result string
}

// optMethods are the Option methods the prelude models, by name
// (std/option.gala).
var optMethods = map[string]optMethod{
	"IsDefined": {code: "(optIsDefined {r})", pure: true, result: "Bool"},
	"IsEmpty":   {code: "(!optIsDefined {r})", pure: true, result: "Bool"},
	"Get":       {code: "optGet {r}", result: "T"},
	"GetOrElse": {params: []string{"T"}, code: "(optGetOrElse {r} {0})", pure: true, result: "T"},
	"OrElse":    {params: []string{"O"}, code: "(optOrElse {r} {0})", pure: true, result: "O"},
	"OnSome":    {params: []string{"T->"}, code: "optOnSome {r} {0}", result: "O"},
	"OnNone":    {params: []string{"->"}, code: "optOnNone {r} {0}", result: "O"},
	"ForEach":   {params: []string{"T->"}, code: "optForEach {r} {0}"},
	"Map":       {params: []string{"T->_"}, code: "optMap {r} {0}", result: "M"},
	"FlatMap":   {params: []string{"T->O_"}, code: "optFlatMap {r} {0}", result: "F"},
	"Filter":    {params: []string{"T->Bool"}, code: "optFilter {r} {0}", result: "O"},
	"Exists":    {params: []string{"T->Bool"}, code: "optExists {r} {0}", result: "Bool"},
	"ForAll":    {params: []string{"T->Bool"}, code: "optForAll {r} {0}", result: "Bool"},
}

// eitherMethods are the Either methods the prelude models, by name
// (std/either.gala).
var eitherMethods = map[string]optMethod{
	"IsLeft":   {code: "(eitherIsLeft {r})", pure: true, result: "Bool"},
	"IsRight":  {code: "(!eitherIsLeft {r})", pure: true, result: "Bool"},
	"GetLeft":  {code: "eitherGetLeft {r}", result: "A"},
	"GetRight": {code: "eitherGetRight {r}", result: "B"},
	"Swap":     {code: "(eitherSwap {r})", pure: true, result: "W"},
	"OnRight":  {params: []string{"B->"}, code: "eitherOnRight {r} {0}", result: "O"},
	"OnLeft":   {params: []string{"A->"}, code: "eitherOnLeft {r} {0}", result: "O"},
	"Fold":     {params: []string{"A->_", "B->_"}, code: "eitherFold {r} {0} {1}", result: "F"},
	"Map":      {params: []string{"B->_"}, code: "eitherMap {r} {0}", result: "M"},
	"FlatMap":  {params: []string{"B->E_"}, code: "eitherFlatMap {r} {0}", result: "F"},
}

// optOf reads Lean type t as std's Option or Either: its prelude type, its
// type arguments' Lean types, and the methods modelled for it. A type the
// program declares is never GOption or GEither (ident keeps its name).
func optOf(t string) (kind string, args []string, methods map[string]optMethod, ok bool) {
	p := parseLean(t)
	switch {
	case p.head == "GOption" && len(p.args) == 1:
		methods = optMethods
	case p.head == "GEither" && len(p.args) == 2:
		methods = eitherMethods
	default:
		return "", nil, nil, false
	}
	for _, a := range p.args {
		args = append(args, a.String())
	}
	return p.head, args, methods, true
}

// optCall translates a call of a method of an Option or an Either: the
// receiver, then the arguments in the order written, then the method. It
// reports false when the receiver is not plainly one.
func (g *gen) optCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	t := g.typeOf(s.Target, sc)
	_, args, methods, ok := optOf(t)
	if !ok {
		return term{}, false
	}
	// The receiver first: a construct outside the subset in it is the one
	// to report.
	recv := g.expr(s.Target, sc)
	m, ok := methods[s.Name]
	if !ok {
		unsup(s.Pos, "option-method")
	}
	if len(e.Args) != len(m.params) {
		unsup(e.Pos, "argument-count")
	}
	subs := []term{recv}
	repl := []string{"{r}", recv.val()}
	for i, a := range e.Args {
		positional(a)
		v := g.argExpect(a, sc, g.optParamType(m.params[i], t, args))
		subs = append(subs, v)
		repl = append(repl, fmt.Sprintf("{%d}", i), v.val())
	}
	code := strings.NewReplacer(repl...).Replace(m.code)
	if m.pure {
		return wrap(code, subs...), true
	}
	return actUnit(code, m.result == ""), true
}

// optParamType is the GALA type a parameter of kind p wants, for a
// receiver of Lean type t with type arguments args; nil where it is not
// known.
func (g *gen) optParamType(p, t string, args []string) coreexport.Node {
	known := func(n coreexport.Node) coreexport.Node {
		if n == nil {
			return &coreexport.TypeName{Name: unknownTypeArg}
		}
		return n
	}
	infer := &coreexport.TypeName{Name: inferTypeArg}
	one := func(x string) coreexport.Node {
		switch x {
		case "T", "A":
			return known(g.galaType(args[0]))
		case "B":
			return known(g.galaType(args[1]))
		case "Bool":
			return &coreexport.TypeName{Name: "bool"}
		case "O":
			return known(g.galaType(t))
		case "_":
			return infer
		case "O_":
			return optionOfInferred()
		case "E_":
			// Lean takes the left type from eitherFlatMap's signature.
			return &coreexport.TypeName{Name: "Either", Args: []coreexport.Node{infer, infer}}
		}
		return nil
	}
	ps, res, isFunc := strings.Cut(p, "->")
	if !isFunc {
		return one(p)
	}
	f := &coreexport.TypeFunc{}
	if ps != "" {
		f.Params = append(f.Params, one(ps))
	}
	if res != "" {
		f.Result = one(res)
	}
	return f
}

// typedLambda is l with the parameter types it leaves out taken from
// expect, the function type its context wants, where that type is known:
// for reading its result's type, as the translation types its parameters
// (lambda). l itself is not changed.
func (g *gen) typedLambda(l *coreexport.Lambda, expect coreexport.Node) *coreexport.Lambda {
	ft, ok := expect.(*coreexport.TypeFunc)
	if !ok || len(ft.Params) != len(l.Params) {
		return l
	}
	c := *l
	c.Params = make([]coreexport.Node, len(l.Params))
	for i, pn := range l.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			return l
		}
		if p.Type == nil {
			if n, isName := ft.Params[i].(*coreexport.TypeName); isName && n.Name == unknownTypeArg {
				return l
			}
			q := *p
			q.Type = ft.Params[i]
			p = &q
		}
		c.Params[i] = p
	}
	return &c
}

// optionOfInferred is the GALA type Option of a type Lean infers.
func optionOfInferred() coreexport.Node {
	return &coreexport.TypeName{Name: "Option", Args: []coreexport.Node{&coreexport.TypeName{Name: inferTypeArg}}}
}

// funcArgResult is the Lean result type of function argument a of a std
// call whose parameter wants expect, "" when not plain to see. A lambda
// that leaves out its parameter types is read with them typed as the call
// types them (typedLambda).
func (g *gen) funcArgResult(a coreexport.Node, expect coreexport.Node, sc *scope, seen map[string]bool) string {
	a = argNode(a)
	if l, ok := g.placeholderLambda(a, expect); ok {
		a = l
	}
	if l, ok := a.(*coreexport.Lambda); ok {
		a = g.typedLambda(l, expect)
	}
	if r := funcResult(g.exprType(a, sc, seen)); r != "_" {
		return r
	}
	return ""
}

// optCallType is exprType of a call of a method of an Option or an Either
// whose receiver has Lean type t.
func (g *gen) optCallType(e *coreexport.Call, t string, s *coreexport.Select, sc *scope, seen map[string]bool) string {
	kind, args, methods, ok := optOf(t)
	if !ok {
		return ""
	}
	m, ok := methods[s.Name]
	if !ok || len(e.Args) != len(m.params) {
		return ""
	}
	// fres is the result type of the first function argument whose result
	// is plain to see (Fold's two give the same type).
	fres := func() string {
		for i, a := range e.Args {
			if r := g.funcArgResult(a, g.optParamType(m.params[i], t, args), sc, seen); r != "" {
				return r
			}
		}
		return ""
	}
	switch m.result {
	case "Bool":
		return "Bool"
	case "T", "A":
		return args[0]
	case "B":
		return args[1]
	case "O":
		return t
	case "W":
		return "(GEither " + args[1] + " " + args[0] + ")"
	case "F":
		return fres()
	case "M":
		r := fres()
		switch {
		case r == "":
			return ""
		case kind == "GOption":
			return "(GOption " + r + ")"
		}
		return "(GEither " + args[0] + " " + r + ")"
	}
	return ""
}
