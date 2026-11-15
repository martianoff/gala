package gala2lean

import (
	"fmt"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// sig is what a call needs of a callee declared in the program: a function,
// a struct or a sealed variant.
type sig struct {
	head     string // the Lean function or constructor applied to the arguments
	names    []string
	types    []coreexport.Node
	defaults []coreexport.Node
	// paramsInDefaults is true for a function: a parameter's default sees
	// the parameters before it. A field's default sees no field.
	paramsInDefaults bool
	unit             bool // a function without a result
	ctor             bool // a struct or variant constructor, whose call is a value
	// recv is a method's receiver, the first argument; recvLast evaluates
	// it after the other arguments (a pointer method's place).
	recv     *term
	recvLast bool
	// recvName and recvType are the method's receiver name and Lean type,
	// which its parameters' defaults may use.
	recvName, recvType string
	// inout marks the `inout` parameters, whose arguments are lent places
	// (`&x`); nil when there are none.
	inout []bool
	// lent is, for a call of it, the place each argument given to an inout
	// parameter lends, by argument index (lendCall).
	lent map[int]coreexport.Node
}

// stdCtors are the standard library's Option and Either constructors, which
// the prelude models (GOption, GEither), when the program does not declare
// the name itself.
var stdCtors = map[string]string{
	"Some": "GOption.Some", "None": "GOption.None",
	"Left": "GEither.Left", "Right": "GEither.Right",
}

// sigOf is the callee x declared in the program, or nil.
func (g *gen) sigOf(x string) *sig {
	switch {
	case g.funcs[x] != nil:
		g.use(x)
		return funcSig(g.funcs[x], g.funcRef(x))
	case g.structs[x] != nil:
		g.use(x)
		return g.fieldSig(ident(x)+".mk", fieldsOf(g.structs[x].Fields))
	case g.variants[x] != nil:
		v := g.variants[x]
		g.use(v.sealed.Name)
		return g.fieldSig(ident(v.sealed.Name)+"."+ident(v.v.Name), v.fields)
	}
	return nil
}

// funcSig is the signature of function or method f called as head.
func funcSig(f *coreexport.Func, head string) *sig {
	s := &sig{head: head, paramsInDefaults: true}
	lends := hasInout(f.Params)
	for _, pn := range f.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			unsupAt(pn)
		}
		s.names, s.types, s.defaults = append(s.names, p.Name), append(s.types, p.Type), append(s.defaults, p.Default)
		if lends {
			s.inout = append(s.inout, p.Inout)
		}
	}
	// A function with inout parameters returns their final values beside
	// its result (lendCall), so its call is never just Unit.
	s.unit = f.Result == nil && !lends
	return s
}

// hasInout reports whether any of params is an `inout` parameter.
func hasInout(params []coreexport.Node) bool {
	return slices.ContainsFunc(params, func(pn coreexport.Node) bool {
		p, ok := pn.(*coreexport.Param)
		return ok && p.Inout
	})
}

// sigOfSafe is sigOf, or nil for a callee the translation will report.
func (g *gen) sigOfSafe(x string) *sig {
	s, _ := attempt(func() *sig { return g.sigOf(x) })
	return s
}

func (g *gen) fieldSig(head string, fields []*coreexport.Field) *sig {
	s := &sig{head: head, ctor: true}
	for _, f := range fields {
		s.names, s.types, s.defaults = append(s.names, f.Name), append(s.types, f.Type), append(s.defaults, f.Default)
	}
	return s
}

// funcRef is how the code refers to top-level function x: by its full name
// inside a default value, which is translated where the caller's locals are
// in scope in Lean but must not be seen.
func (g *gen) funcRef(x string) string {
	if g.qualify {
		return "_root_." + g.ns + "." + ident(x)
	}
	return ident(x)
}

// bindArgs matches a call's arguments to s's parameters: positional ones
// first, then named ones. It returns, per parameter, the index of the
// argument that gives it, or -1 for a parameter left to its default.
func bindArgs(s *sig, args []coreexport.Node, pos coreexport.Pos) []int {
	given := make([]int, len(s.names))
	for i := range given {
		given[i] = -1
	}
	named := false
	for i, a := range args {
		if na, ok := a.(*coreexport.NamedArg); ok {
			named = true
			k := slices.Index(s.names, na.Name)
			if k < 0 || given[k] >= 0 {
				unsup(na.Pos, "named-arg")
			}
			given[k] = i
			continue
		}
		if named {
			unsup(posOf(a), "positional-after-named")
		}
		if i >= len(s.names) {
			unsup(pos, "argument-count")
		}
		given[i] = i
	}
	for k, a := range given {
		if a < 0 && s.defaults[k] == nil {
			unsup(pos, "argument-count")
		}
	}
	return given
}

// positional stops the translation at a named argument where only
// positional ones are translated (Println, a function value, ...).
func positional(a coreexport.Node) {
	if na, ok := a.(*coreexport.NamedArg); ok {
		unsup(na.Pos, "named-arg")
	}
}

// argNode is the expression of a call argument, named or not.
func argNode(a coreexport.Node) coreexport.Node {
	if na, ok := a.(*coreexport.NamedArg); ok {
		return na.Value
	}
	return a
}

// callSig translates a call of a declared callee (docs/FORMAL_SEMANTICS.MD,
// "Decisions"): the arguments are evaluated in the order written, then
// passed in parameter order; each parameter left out takes its default,
// evaluated after them, in parameter order, in the callee's scope (a
// function's default sees the parameters before it, a field's none).
func (g *gen) callSig(s *sig, e *coreexport.Call, sc *scope) term {
	items, call := g.callItems(s, e, sc, false)
	if len(items) == 0 {
		return call
	}
	return term{code: doTerm(append(items, call.mon())), unit: s.unit}
}

// callItems is callSig as do-items that compute the arguments, and the call
// that follows them. With hoist, the items run at sc's own level (a
// statement), so an argument that calls a pointer method writes its
// receiver back there, in argument order (`x.m(x.n())`).
func (g *gen) callItems(s *sig, e *coreexport.Call, sc *scope, hoist bool) ([]string, term) {
	given := bindArgs(s, e.Args, e.Pos)
	paramOf := make([]int, len(e.Args))
	for k, a := range given {
		if a >= 0 {
			paramOf[a] = k
		}
	}
	vals := make([]term, len(e.Args))
	pre := make([][]string, len(e.Args))
	hoisted := false
	for i, a := range e.Args {
		if s.lent[i] != nil {
			continue // read after the other arguments, below
		}
		if c, ok := argNode(a).(*coreexport.Call); ok && hoist {
			if items, res := g.ptrCall(c, sc); items != nil {
				pre[i], vals[i], hoisted = items, term{code: res, pure: true}, true
				continue
			}
		}
		vals[i] = g.exprExpect(argNode(a), sc, s.types[paramOf[i]])
	}
	inOrder := true
	defaults := false
	for k, a := range given {
		inOrder = inOrder && a == k
		defaults = defaults || a < 0
	}
	// A method's receiver comes first; a pointer method's place is read
	// after the arguments (recvLast), which may change it.
	recvFirst := s.recv != nil && !s.recvLast
	if !defaults && inOrder && !hoisted && len(s.lent) == 0 && (s.recv == nil || s.recv.pure || recvFirst || allPure(vals...)) {
		var vs []string
		subs := vals
		if s.recv != nil {
			vs, subs = []string{s.recv.val()}, append([]term{*s.recv}, vals...)
		}
		for _, v := range vals {
			vs = append(vs, v.val())
		}
		return nil, g.apply(s, vs, subs...)
	}
	// Arguments into temporaries, in the order written; every name here is
	// fresh, so nothing the caller sees is shadowed.
	var items []string
	recv := ""
	bindRecv := func() {
		recv = g.freshName("o")
		items = append(items, "let "+recv+" := "+s.recv.val())
	}
	if recvFirst {
		bindRecv()
	}
	tmps := make([]string, len(e.Args))
	for i, v := range vals {
		if s.lent[i] != nil {
			continue
		}
		tmps[i] = g.freshName("a")
		items = append(items, pre[i]...)
		items = append(items, "let "+tmps[i]+" := "+v.val())
	}
	// A lent place is passed as its value when the callee starts, after
	// the other arguments, which may write it (`f(&x, g(&x))`): Go passes
	// its address, so the callee sees every earlier write.
	for i := range e.Args {
		if q := s.lent[i]; q != nil {
			tmps[i] = g.freshName("a")
			items = append(items, "let "+tmps[i]+" := "+g.expr(q, sc).val())
		}
	}
	if s.recv != nil && s.recvLast {
		// A pointer method's place, after the arguments and before the
		// defaults, which may read the receiver.
		bindRecv()
	}
	// Then the defaults, in parameter order. Each is evaluated in a do block
	// of its own that binds what it may see (the receiver, the parameters
	// before it) under their own names, so those bindings end with it.
	args := make([]string, len(s.names))
	for k, a := range given {
		if a >= 0 {
			args[k] = tmps[a]
			continue
		}
		key := s.head + "." + s.names[k]
		if g.inDefault[key] {
			unsup(e.Pos, "recursive-default")
		}
		g.inDefault[key] = true
		dsc := newScope(nil, 0)
		var seen []string
		if s.recv != nil && s.recvName != "" {
			seen = append(seen, "let "+ident(s.recvName)+" := "+recv)
			dsc.bind(s.recvName, false, s.recvType)
		}
		if s.paramsInDefaults {
			for j := range k {
				seen = append(seen, "let "+ident(s.names[j])+" := "+args[j])
				dsc.bind(s.names[j], false, g.optTypeSafe(s.types[j]))
			}
		}
		saved := g.qualify
		g.qualify = true
		d := g.exprExpect(s.defaults[k], dsc, s.types[k])
		g.qualify = saved
		delete(g.inDefault, key)
		tmp := g.freshName("d")
		if len(seen) == 0 {
			items = append(items, "let "+tmp+" := "+d.val())
		} else {
			items = append(items, "let "+tmp+" ← "+doTerm(append(seen, d.mon())))
		}
		args[k] = tmp
	}
	if s.recv != nil {
		args = append([]string{recv}, args...)
	}
	return items, g.apply(s, args)
}

// optType is the Lean type of an optional type node, "" when absent.
func (g *gen) optType(t coreexport.Node) string {
	if t == nil {
		return ""
	}
	return g.leanType(t)
}

// apply applies s's head to argument values; subs are the argument terms
// (for purity).
func (g *gen) apply(s *sig, args []string, subs ...term) term {
	c := s.head
	if len(args) > 0 {
		c = "(" + c + " " + strings.Join(args, " ") + ")"
	}
	switch {
	case s.ctor:
		// A constructor: a value.
		return wrap(c, subs...)
	case allPure(subs...):
		return term{code: c, unit: s.unit}
	}
	return actUnit(c, s.unit)
}

// call translates a call: of a declared function, struct or variant, of
// Println/Print, of an Option/Either constructor, or of a function value.
func (g *gen) call(e *coreexport.Call, sc *scope) term {
	if u, ok := e.Callee.(*coreexport.Unsupported); ok {
		unsup(u.Pos, "%s", u.What)
	}
	if n, ok := e.Callee.(*coreexport.Name); ok && sc.lookup(n.Name) == nil {
		x := n.Name
		if s := g.sigOf(x); s != nil {
			if s.inout != nil {
				// Its write-backs run in a do block of their own, so the
				// places must be ones it can write (cells).
				items, res := g.lendSig(x, s, e, sc.term())
				return callTerm(items, res, g.funcs[x].Result == nil)
			}
			if f := g.funcs[x]; f != nil && len(f.TypeParams) > 0 {
				g.instSig(f, s, e, sc)
			} else if n := g.ctorOf(x); n != "" && g.isGenericDecl(n) {
				g.instCtor(n, s, e, sc)
			}
			return g.callSig(s, e, sc)
		}
		switch {
		case x == "Println" || x == "Print":
			return g.print(x, e, sc)
		case stdCtors[x] != "":
			return g.stdCtor(x, e, sc)
		case g.collections && seqCtors[x] != "":
			return g.seqCtor(x, e, sc)
		case g.collections && hashTypes[x]:
			unsup(e.Pos, "hash-order")
		case g.globalBy[x] == nil:
			g.unresolved(e.Pos, "call-of-"+x)
		}
	}
	ft := g.calleeType(e.Callee, sc)
	if s, ok := e.Callee.(*coreexport.Select); ok && ft == nil {
		if t, ok := g.methodCall(e, s, sc); ok {
			return t
		}
		if t, ok := g.generatedMethod(e, s, sc); ok {
			return t
		}
		if t, ok := g.seqCall(e, s, sc); ok {
			return t
		}
		// pkg.F(...) names a member of an import; v.M(...) calls a method.
		if n, isName := s.Target.(*coreexport.Name); isName && sc.lookup(n.Name) == nil &&
			g.globalBy[n.Name] == nil && g.variants[n.Name] == nil {
			g.unresolved(s.Pos, "method-call")
		}
		if _, isCall := s.Target.(*coreexport.Call); isCall {
			// The receiver runs first: a construct outside the subset in
			// it (`HashSetOf(1).Size()`) is the one to report.
			g.expr(s.Target, sc)
		}
		unsup(s.Pos, "method-call")
	}
	// A function value: a local, a package-level variable, a field, or the
	// result of another call. The callee is evaluated first, then the
	// arguments left to right.
	f := g.expr(e.Callee, sc)
	parts := []string{f.val()}
	subs := []term{f}
	for i, a := range e.Args {
		positional(a)
		// The callee's parameter types, when known, type lambda arguments
		// and turn by-name arguments into thunks.
		var expect coreexport.Node
		if ft != nil && i < len(ft.Params) {
			expect = ft.Params[i]
		}
		t := g.exprExpect(a, sc, expect)
		parts, subs = append(parts, t.val()), append(subs, t)
	}
	if len(e.Args) == 0 {
		parts = append(parts, "()")
	}
	c := "(" + strings.Join(parts, " ") + ")"
	if allPure(subs...) {
		return term{code: c}
	}
	return act(c)
}

// calleeType is the GALA function type of a called function value when it is
// plain to see: a local declared with one, a lambda local, a parameter, or a
// function-typed field; nil otherwise.
func (g *gen) calleeType(c coreexport.Node, sc *scope) *coreexport.TypeFunc {
	switch c := c.(type) {
	case *coreexport.Name:
		if l := sc.lookup(c.Name); l != nil {
			ft, _ := l.gtype.(*coreexport.TypeFunc)
			return ft
		}
	case *coreexport.Select:
		if f := g.fieldByType(g.typeOf(c.Target, sc), c.Name); f != nil {
			ft, _ := f.Type.(*coreexport.TypeFunc)
			return ft
		}
	}
	return nil
}

// print translates Println or Print.
func (g *gen) print(x string, e *coreexport.Call, sc *scope) term {
	shown := make([]string, len(e.Args))
	subs := make([]term, len(e.Args))
	for i, a := range e.Args {
		positional(a)
		g.printable(a, sc)
		subs[i] = g.expr(a, sc)
		shown[i] = "garg " + subs[i].val()
	}
	fn := map[string]string{"Println": "gprintln", "Print": "gprint"}[x]
	return actUnit(fmt.Sprintf("%s [%s]", fn, strings.Join(shown, ", ")), true)
}

// printable stops the translation for a value whose printed text is not
// what the translation would print: a function (`fmt` prints its address),
// or, in a program with methods, anything but int, string and bool.
func (g *gen) printable(e coreexport.Node, sc *scope) {
	g.printableType(g.typeOf(e, sc), posOf(e))
}

// printableType is printable for a value of Lean type t ("" when not plain
// to see) at pos.
func (g *gen) printableType(t string, pos coreexport.Pos) {
	if strings.Contains(t, "→") {
		unsup(pos, "print-pointer")
	}
	// A type with its own String method prints through it, and so does a
	// value holding one.
	g.customCheck(t, g.customString, &g.stringHolders, pos, "custom-string")
	if len(g.unprintable) > 0 {
		// A value of a type whose text holds a function's address; when the
		// type is not plain to see, any value might be one.
		if t == "" || g.mentionsTypeParam(t) {
			unsup(pos, "print-pointer")
		}
		g.stateTypeIn(t, g.unprintable, pos, "print-pointer")
	}
	// A value whose raw text reaches a generic type (rawPrinted).
	g.rawPrinted()
	g.customCheck(t, g.rawGeneric, &g.rawGenericHolders, pos, "print-raw-generic")
	g.basicType(t, pos)
}

// stdCtor builds an Option or Either value.
func (g *gen) stdCtor(x string, e *coreexport.Call, sc *scope) term {
	want := 1
	if x == "None" {
		want = 0
	}
	if len(e.Args) != want {
		unsup(e.Pos, "argument-count")
	}
	if want == 0 {
		return term{code: stdCtors[x], pure: true}
	}
	positional(e.Args[0])
	v := g.expr(e.Args[0], sc)
	return wrap("("+stdCtors[x]+" "+v.val()+")", v)
}

// interp translates an interpolated string: its parts left to right, each
// embedded value printed as Println prints it.
func (g *gen) interp(e *coreexport.Interp, sc *scope) term {
	var parts []string
	var subs []term
	for _, p := range e.Parts {
		if t, ok := p.(*coreexport.InterpText); ok {
			parts = append(parts, strLit(t.Value, t.Pos))
			continue
		}
		g.printable(p, sc)
		t := g.expr(p, sc)
		parts, subs = append(parts, "gshow "+t.val()), append(subs, t)
	}
	if len(parts) == 0 {
		return term{code: `""`, pure: true}
	}
	return wrap("(String.join ["+strings.Join(parts, ", ")+"])", subs...)
}
