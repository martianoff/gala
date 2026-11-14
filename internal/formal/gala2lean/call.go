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
		f := g.funcs[x]
		s := &sig{head: g.funcRef(x), paramsInDefaults: true, unit: f.Result == nil}
		for _, pn := range f.Params {
			p, ok := pn.(*coreexport.Param)
			if !ok {
				unsupAt(pn)
			}
			s.names, s.types, s.defaults = append(s.names, p.Name), append(s.types, p.Type), append(s.defaults, p.Default)
		}
		return s
	case g.structs[x] != nil:
		return g.fieldSig(ident(x)+".mk", fieldsOf(g.structs[x].Fields))
	case g.variants[x] != nil:
		v := g.variants[x]
		return g.fieldSig(ident(v.sealed.Name)+"."+ident(x), v.fields)
	}
	return nil
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
	given := bindArgs(s, e.Args, e.Pos)
	paramOf := make([]int, len(e.Args))
	for k, a := range given {
		if a >= 0 {
			paramOf[a] = k
		}
	}
	vals := make([]term, len(e.Args))
	for i, a := range e.Args {
		vals[i] = g.exprExpect(argNode(a), sc, s.types[paramOf[i]])
	}
	inOrder := true
	defaults := false
	for k, a := range given {
		inOrder = inOrder && a == k
		defaults = defaults || a < 0
	}
	if !defaults && inOrder {
		vs := make([]string, len(vals))
		for i, v := range vals {
			vs[i] = v.val()
		}
		return g.apply(s, vs, vals...)
	}
	// Arguments into temporaries, in the order written. Every one gets a
	// fresh name, even a plain value: the parameters bound below for the
	// defaults may shadow a caller's local it names.
	var items []string
	tmps := make([]string, len(e.Args))
	for i, v := range vals {
		tmps[i] = g.freshName("a")
		items = append(items, "let "+tmps[i]+" := "+v.val())
	}
	// Then the defaults, in parameter order.
	args := make([]string, len(s.names))
	dsc := newScope(nil, 0)
	for k, a := range given {
		if a >= 0 {
			args[k] = tmps[a]
		} else {
			key := s.head + "." + s.names[k]
			if g.inDefault[key] {
				unsup(e.Pos, "recursive-default")
			}
			g.inDefault[key] = true
			saved := g.qualify
			g.qualify = true
			d := g.exprExpect(s.defaults[k], dsc, s.types[k])
			g.qualify = saved
			delete(g.inDefault, key)
			tmp := g.freshName("d")
			items = append(items, "let "+tmp+" := "+d.val())
			args[k] = tmp
		}
		if s.paramsInDefaults && slices.Contains(given[k+1:], -1) {
			// The parameters a later default may see.
			items = append(items, "let "+ident(s.names[k])+" := "+args[k])
			args[k] = ident(s.names[k])
			dsc.bind(s.names[k], false, g.optType(s.types[k]))
		}
	}
	items = append(items, g.apply(s, args).mon())
	return term{code: doTerm(items), unit: s.unit}
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
			return g.callSig(s, e, sc)
		}
		switch {
		case x == "Println" || x == "Print":
			return g.print(x, e, sc)
		case stdCtors[x] != "":
			return g.stdCtor(x, e, sc)
		case g.globalBy[x] == nil:
			g.unresolved(e.Pos, "call-of-"+x)
		}
	}
	ft := g.calleeType(e.Callee, sc)
	if s, ok := e.Callee.(*coreexport.Select); ok && ft == nil {
		// pkg.F(...) names a member of an import; v.M(...) calls a method.
		if n, isName := s.Target.(*coreexport.Name); isName && sc.lookup(n.Name) == nil &&
			g.globalBy[n.Name] == nil && g.variants[n.Name] == nil {
			g.unresolved(s.Pos, "method-call")
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
	t := g.typeOf(e, sc)
	if strings.Contains(t, "→") {
		unsup(posOf(e), "print-pointer")
	}
	if len(g.unprintable) > 0 {
		// A value of a type whose text holds a function's address; when the
		// type is not plain to see, any value might be one.
		if t == "" {
			unsup(posOf(e), "print-pointer")
		}
		g.stateTypeIn(t, g.unprintable, posOf(e), "print-pointer")
	}
	g.basicType(t, posOf(e))
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
