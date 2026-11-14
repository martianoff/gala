package gala2lean

import (
	"fmt"
	"maps"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Methods. A method of type T is a Lean function T.m taking the receiver
// first.
//
//   - A value receiver `(c T)` is an ordinary parameter:
//     `def T.m (c : T) (args) : M R`.
//   - A pointer receiver `(c *T)` mutates the caller's value. The receiver is
//     unique while the call runs (the method may only read and write its
//     fields and call methods on it; any other use is reported as
//     ptr-escape), so it is translated by state passing, as
//     docs/OWNERSHIP_DESIGN.MD (3.2.5) describes for `inout`: the method takes
//     the value and returns its result with the updated value,
//     `def T.m (c : T) (args) : M (R × T)`, and the caller writes the value
//     back to the place it called the method on. Inside, the receiver lives
//     in a cell, so the body may write its fields from any depth of
//     expression, and `return` leaves the body as usual.
//
// The place must be one no other path reaches while the method runs: a
// local var no lambda captures (or a field path in one), or the enclosing
// pointer method's own receiver. A package-level var or a captured var is
// reported (ptr-escape): code the method calls could read it and would see
// Go's writes in place. The value is written back after the arguments are
// evaluated and the method returns; a method that panics writes nothing
// back. That loses no output today, since a panic ends the run, but it
// would be wrong once a panic can be caught.

// key is a function's name, or "T.m" for method m of type T.
func (g *gen) key(f *coreexport.Func) string {
	if r, ok := f.Receiver.(*coreexport.Receiver); ok {
		return typeName(r.Type) + "." + f.Name
	}
	return f.Name
}

// addMethod records method f. A method whose receiver is outside the subset
// (Ptr[T], a generic type) is a declaration outside it: a call of it is
// reported, and its String or Equal, if it has one, may decide how any
// value prints or compares (hasMethods).
func (g *gen) addMethod(f *coreexport.Func) {
	r, ok := f.Receiver.(*coreexport.Receiver)
	if !ok || typeName(r.Type) == "" {
		what := "method"
		if u, isUnsup := f.Receiver.(*coreexport.Unsupported); isUnsup {
			what = u.What
		} else if ok {
			if u, isUnsup := r.Type.(*coreexport.Unsupported); isUnsup {
				what = u.What
			}
		}
		g.outsideDecls = append(g.outsideDecls, &coreexport.Unsupported{What: what, Pos: f.Pos})
		g.hasMethods = true
		return
	}
	t := typeName(r.Type)
	if g.methods[t] == nil {
		g.methods[t] = map[string]*coreexport.Func{}
	}
	g.methods[t][f.Name] = f
	k := g.key(f)
	g.byKey[k] = f
	g.methodsNamed[f.Name] = append(g.methodsNamed[f.Name], k)
	g.funcOrder = append(g.funcOrder, f)
	switch f.Name {
	case "String":
		g.customString[t] = true
	case "Equal":
		g.customEqual[t] = true
	}
}

// funcOrMethodDecl is the definition of a function or method.
func (g *gen) funcOrMethodDecl(f *coreexport.Func, recursive bool) string {
	if f.Receiver != nil {
		return g.methodDecl(f, recursive)
	}
	return g.funcDecl(f, recursive)
}

// comparable stops the translation when `==` on e might call an Equal
// method: e's type (or a type in it) declares its own Equal, or, when e's
// type is not plain to see, any type does. In a program with a method the
// translation cannot see, only int, string and bool compare.
func (g *gen) comparable(e coreexport.Node, sc *scope) {
	t := g.typeOf(e, sc)
	if len(g.customEqual) > 0 {
		if t == "" {
			unsup(posOf(e), "custom-equal")
		}
		g.stateTypeIn(t, g.holders(g.customEqual), posOf(e), "custom-equal")
	}
	g.basicType(t, posOf(e))
}

// customStringHolders are the types that print through a String method of
// their own: one that declares it, or one holding such a value in a field.
func (g *gen) customStringHolders() map[string]bool { return g.holders(g.customString) }

// holders is types and every type holding one of them in a field, at any
// depth.
func (g *gen) holders(types map[string]bool) map[string]bool {
	out := maps.Clone(types)
	for changed := true; changed; {
		changed = false
		for _, n := range g.typeNames {
			if out[n] {
				continue
			}
			for _, f := range g.typeFields(n) {
				if out[typeName(f.Type)] {
					out[n], changed = true, true
					break
				}
			}
		}
	}
	return out
}

// methodHead is the Lean name of method m of type t.
func methodHead(t, m string) string { return ident(t) + "." + ident(m) }

// methodDecl is the definition of method f.
func (g *gen) methodDecl(f *coreexport.Func, recursive bool) string {
	r := f.Receiver.(*coreexport.Receiver)
	t := typeName(r.Type)
	if g.structs[t] == nil && g.sealeds[t] == nil {
		unsup(r.Pos, "method-receiver-type")
	}
	lt := g.leanType(r.Type)
	sc := newScope(nil, 0)
	ps := []string{}
	res := "Unit"
	if f.Result != nil {
		res = g.leanType(f.Result)
	}
	var body string
	if !r.Pointer {
		ps = append(ps, fmt.Sprintf("(%s : %s)", ident(r.Name), lt))
		sc.bind(r.Name, false, lt).gtype = r.Type
		ps = append(ps, g.params(f, sc)...)
		g.fn = &fnCtx{result: res}
		body = indentLines(g.bodyItems(f, sc, res), 2)
	} else {
		g.receiverUses(f, r.Name)
		self := "_gala_self"
		ps = append(ps, fmt.Sprintf("(%s : %s)", self, lt))
		ps = append(ps, g.params(f, sc)...)
		// The receiver in a cell, so that the body writes it at any depth.
		h := g.heap(lt, r.Pos)
		l := sc.bind(r.Name, true, lt)
		l.box, l.gtype, l.receiver = h, r.Type, true
		g.fn = &fnCtx{result: res}
		inner := newScope(sc, 0)
		items := []string{
			fmt.Sprintf("let %s ← _gala_new%d %s", ident(r.Name), h, self),
			"let _gala_res ← " + doTerm(g.bodyItems(f, inner, res)) + " -- the method body",
			fmt.Sprintf("pure (_gala_res, ← _gala_get%d %s)", h, ident(r.Name)),
		}
		items[1] = strings.TrimSuffix(items[1], " -- the method body")
		body = indentLines(items, 2)
		res = "(" + res + " × " + lt + ")"
	}
	s := fmt.Sprintf("def %s %s : _gala_M %s := do\n%s", methodHead(t, f.Name), strings.Join(ps, " "), res, body)
	if recursive {
		s += "\npartial_fixpoint"
	}
	return s
}

// params are a function's parameters in Lean, bound in sc.
func (g *gen) params(f *coreexport.Func, sc *scope) []string {
	var ps []string
	for _, pn := range f.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			unsupAt(pn)
		}
		if p.Type == nil {
			unsup(p.Pos, "untyped-param")
		}
		ps = append(ps, fmt.Sprintf("(%s : %s)", ident(p.Name), g.leanType(p.Type)))
		sc.bind(p.Name, false, g.leanType(p.Type)).gtype = p.Type
	}
	return ps
}

// bodyItems are a function's body as do-items, for result type res.
func (g *gen) bodyItems(f *coreexport.Func, sc *scope, res string) []string {
	if b, ok := f.Body.(*coreexport.Block); ok {
		return g.fnBlock(b, sc)
	}
	if res == "Unit" {
		// A function without a result type runs its expression body as a
		// statement.
		return g.unitItems(f.Body, sc)
	}
	return g.valueItems(f.Body, sc)
}

// receiverUses checks that a pointer method uses its receiver only through
// it: reading or writing a field, or calling a method on it, and never in a
// lambda. Anything else (passing it on, binding it to another name,
// comparing it, capturing it) would let another path reach the value, which
// state passing does not model.
func (g *gen) receiverUses(f *coreexport.Func, name string) {
	var visit func(n coreexport.Node, inLambda bool)
	visit = func(n coreexport.Node, inLambda bool) {
		switch n := n.(type) {
		case nil:
			return
		case *coreexport.Lambda:
			inLambda = true
		case *coreexport.Select:
			// p.f, p.f.g and p.m(...) reach the value through p.
			root := n.Target
			for {
				if s, ok := root.(*coreexport.Select); ok {
					root = s.Target
					continue
				}
				if p, ok := root.(*coreexport.Paren); ok {
					root = p.Expr
					continue
				}
				break
			}
			if id, ok := root.(*coreexport.Name); ok && id.Name == name {
				if inLambda {
					unsup(n.Pos, "ptr-escape")
				}
				return
			}
		case *coreexport.Name:
			if n.Name == name {
				unsup(n.Pos, "ptr-escape")
			}
			return
		}
		walkChildren(n, func(c coreexport.Node) { visit(c, inLambda) })
	}
	visit(f.Body, false)
}

// methodOf is method m of the type of value e, and that type's name, or nil.
func (g *gen) methodOf(e coreexport.Node, m string, sc *scope) (*coreexport.Func, string) {
	t := g.typeOf(e, sc)
	for _, n := range g.typeNames {
		if ident(n) == t && g.methods[n][m] != nil {
			return g.methods[n][m], n
		}
	}
	return nil, ""
}

// methodSig is the signature of method f of type t, its receiver aside.
func (g *gen) methodSig(f *coreexport.Func, t string) *sig {
	s := &sig{head: methodHead(t, f.Name), paramsInDefaults: true, unit: f.Result == nil}
	for _, pn := range f.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			unsupAt(pn)
		}
		s.names, s.types, s.defaults = append(s.names, p.Name), append(s.types, p.Type), append(s.defaults, p.Default)
	}
	return s
}

// methodCall translates `x.m(args)` for a method m of x's type: x first, then
// the arguments. A pointer method's call is do-items (ptrCall); here it is
// a term, so the place it writes back must be one a nested do block can
// write (a cell or a package-level variable).
func (g *gen) methodCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	f, t := g.methodOf(s.Target, s.Name, sc)
	if f == nil {
		return term{}, false
	}
	if f.Receiver.(*coreexport.Receiver).Pointer {
		items, res := g.ptrCall(e, sc.term())
		if items == nil {
			return term{}, false
		}
		return term{code: doTerm(append(items, "pure "+res)), unit: f.Result == nil}, true
	}
	sg := g.methodSig(f, t)
	recv := g.expr(s.Target, sc)
	sg.recv = &recv
	return g.callSig(sg, e, sc), true
}

// ptrCall translates a call of a pointer method on a place: the arguments
// are evaluated, then the place is read and the method called, and its new
// value written back. It returns the do-items and the name holding the
// call's result, or nil items when e is not such a call.
func (g *gen) ptrCall(e *coreexport.Call, sc *scope) ([]string, string) {
	s, ok := e.Callee.(*coreexport.Select)
	if !ok {
		return nil, ""
	}
	f, t := g.methodOf(s.Target, s.Name, sc)
	if f == nil || !f.Receiver.(*coreexport.Receiver).Pointer {
		return nil, ""
	}
	switch g.placeKind(s.Target, sc) {
	case placeNone:
		unsup(s.Pos, "pointer-method-on-value")
	case placeShared:
		// Another path reaches this variable while the method runs (any
		// function reads a package-level var; a lambda shares a captured
		// one), and would see Go's writes in place, which state passing
		// does not model.
		unsup(s.Pos, "ptr-escape")
	}
	sg := g.methodSig(f, t)
	recv := g.expr(s.Target, sc)
	sg.recv = &recv
	sg.recvLast = true
	items, call := g.callItems(sg, e, sc)
	res, upd := g.freshName("r"), g.freshName("u")
	items = append(items, fmt.Sprintf("let (%s, %s) ← %s", res, upd, call.mon()))
	items = append(items, g.assignTo(s.Target, func(string) string { return upd }, sc, s.Pos)...)
	return items, res
}

// placeKind is what e, a pointer method's receiver, is rooted in: a
// variable only this code reaches (a local var no lambda captures, or the
// enclosing pointer method's own receiver), a variable another path may
// reach while the method runs (a package-level var, a captured var), or no
// variable at all. A field path counts as its root.
type placeKind int

const (
	placeNone placeKind = iota
	placeUnique
	placeShared
)

func (g *gen) placeKind(e coreexport.Node, sc *scope) placeKind {
	for {
		switch n := e.(type) {
		case *coreexport.Select:
			e = n.Target
			continue
		case *coreexport.Paren:
			e = n.Expr
			continue
		case *coreexport.Name:
			l := sc.lookup(n.Name)
			switch {
			case l == nil && g.globalBy[n.Name] != nil && g.globalBy[n.Name].mutable:
				return placeShared
			case l == nil || !l.mutable:
				return placeNone
			case l.box >= 0 && !l.receiver:
				return placeShared
			}
			return placeUnique
		}
		return placeNone
	}
}

// generatedMethod translates a call of a method GALA generates for every
// struct, `Copy` with named arguments and `Equal`, when the type does not
// declare its own.
func (g *gen) generatedMethod(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	t := g.typeOf(s.Target, sc)
	var name string
	for _, n := range g.typeNames {
		if ident(n) == t {
			name = n
		}
	}
	if name == "" || g.methods[name][s.Name] != nil {
		return term{}, false
	}
	switch s.Name {
	case "Equal":
		if len(e.Args) != 1 || g.customEqual[name] {
			return term{}, false
		}
		positional(e.Args[0])
		g.comparable(s.Target, sc)
		x, y := g.expr(s.Target, sc), g.expr(e.Args[0], sc)
		return wrap(fmt.Sprintf("(%s == %s)", x.val(), y.val()), x, y), true
	case "Copy":
		st := g.structs[name]
		if st == nil {
			return term{}, false
		}
		x := g.expr(s.Target, sc)
		subs := []term{x}
		var sets []string
		for _, a := range e.Args {
			na, ok := a.(*coreexport.NamedArg)
			if !ok || g.fieldByType(t, na.Name) == nil {
				unsup(posOf(a), "copy-argument")
			}
			v := g.exprExpect(na.Value, sc, g.fieldByType(t, na.Name).Type)
			subs = append(subs, v)
			sets = append(sets, ident(na.Name)+" := "+v.val())
		}
		if len(sets) == 0 {
			return x, true
		}
		return wrap(fmt.Sprintf("{ %s with %s }", x.val(), strings.Join(sets, ", ")), subs...), true
	}
	return term{}, false
}
