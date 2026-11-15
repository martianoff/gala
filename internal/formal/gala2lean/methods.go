package gala2lean

import (
	"fmt"
	"maps"
	"slices"
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
//     ptr-escape), so no other path can observe its updates, and it is
//     translated by state passing: the method takes
//     the value and returns its result with the updated value,
//     `def T.m (c : T) (args) : M (R × T)`, and the caller writes the value
//     back to the place it called the method on. Inside, the receiver lives
//     in a cell, so the body may write its fields from any depth of
//     expression, and `return` leaves the body as usual.
//   - An `inout` receiver `(inout c T)` lends the caller's place as a `*T`
//     receiver does (it lowers to one), so it is translated the same way
//     (statePassed), under the same rules: the diagnostics below that say
//     "pointer" apply to it too. Reading or replacing the receiver as a
//     whole (`c = c.Copy(...)`) is reported as ptr-escape, as for `*T`.
//
// The place must be one no other path reaches while the method runs: a
// local var no lambda captures (or a field path in one), or the enclosing
// pointer method's own receiver. A package-level var or a captured var is
// reported (ptr-escape): code the method calls could read it and would see
// Go's writes in place. The value is written back after the arguments are
// evaluated and the method returns; a method that panics writes nothing
// back. That loses no output today, since a panic ends the run, but it
// would be wrong once a panic can be caught.

// ptrReceivers is p with each receiver `(c Ptr[T])` written `(c *T)`, the
// receiver it is: the transpiler lowers both to the Go pointer receiver
// `func (c *T) m`, and a call on a `var` x to `x.m(...)`. (Ptr[T]'s method
// also panics when c is nil; a receiver the translation passes, a place,
// never is.) Only std's Ptr is: in a package that may declare a type named
// Ptr itself (ownPtr), nothing is rewritten. The exporter writes the
// receiver as the source does.
func ptrReceivers(p *coreexport.Program) *coreexport.Program {
	own := ownPtr(p)
	out := &coreexport.Program{Core: p.Core}
	changed := false
	for _, f := range p.Files {
		var c *coreexport.File
		for i, d := range f.Decls {
			fn, ok := d.(*coreexport.Func)
			if !ok {
				continue
			}
			inner := stdPtrReceiver(fn, own)
			if inner == nil {
				continue
			}
			if c == nil {
				cf := *f
				cf.Decls = slices.Clone(f.Decls)
				c = &cf
			}
			rc := *fn.Receiver.(*coreexport.Receiver)
			rc.Type, rc.Pointer = inner, true
			fc := *fn
			fc.Receiver = &rc
			c.Decls[i] = &fc
		}
		if c == nil {
			out.Files = append(out.Files, f)
			continue
		}
		changed = true
		out.Files = append(out.Files, c)
	}
	if !changed {
		return p
	}
	return out
}

// stdPtrReceiver is T for a method whose receiver is `(c Ptr[T])`, std's Ptr
// over a plain type T of its package; nil for any other. own is ownPtr.
func stdPtrReceiver(fn *coreexport.Func, own map[string]bool) coreexport.Node {
	r, ok := fn.Receiver.(*coreexport.Receiver)
	if !ok || r.Pointer || r.Inout {
		return nil
	}
	tn, ok := r.Type.(*coreexport.TypeName)
	if !ok || tn.Package != "" || tn.Name != "Ptr" || len(tn.Args) != 1 {
		return nil
	}
	inner, ok := tn.Args[0].(*coreexport.TypeName)
	if !ok || inner.Package != "" || len(inner.Args) > 0 || own[pkgPrefix(inner.Name)] {
		return nil
	}
	return inner
}

// ownPtr is the packages, by namespace prefix ("" for the program's own),
// that may declare a type named Ptr themselves, which a receiver `Ptr[T]`
// in them then names: a struct, sealed type or case of that name (a case is
// a Go type too), or a type declaration Core JSON does not name (an
// interface, a defined or opaque type), which might be one. Only a
// package's own declarations can hide std's Ptr from its methods: a method
// is declared in its receiver type's package.
func ownPtr(p *coreexport.Program) map[string]bool {
	own := map[string]bool{}
	for _, f := range p.Files {
		prefix, unnamed := "", false
		for _, d := range f.Decls {
			var names []string
			switch d := d.(type) {
			case *coreexport.Struct:
				names = []string{d.Name}
			case *coreexport.Sealed:
				names = append(names, d.Name)
				for _, vn := range d.Variants {
					if v, ok := vn.(*coreexport.Variant); ok {
						names = append(names, pkgPrefix(d.Name)+v.Name)
					}
				}
			case *coreexport.Func:
				if d.Receiver == nil {
					names = []string{d.Name}
				} else if t := receiverTypeName(d, nil); t != "" {
					prefix = pkgPrefix(t)
				}
			case *coreexport.Unsupported:
				switch d.What {
				case coreexport.UnsupportedDefinedType, coreexport.UnsupportedInterface, coreexport.UnsupportedOpaqueType:
					unnamed = true
				}
			}
			for _, n := range names {
				prefix = pkgPrefix(n)
				if bareName(n) == "Ptr" {
					own[prefix] = true
				}
			}
		}
		// The file's package is the one its named declarations are in.
		if unnamed {
			own[prefix] = true
		}
	}
	return own
}

// key is a function's name, or "T.m" for method m of type T.
func (g *gen) key(f *coreexport.Func) string { return funcKey(f) }

// funcKey is gen.key, for code without a gen.
func funcKey(f *coreexport.Func) string {
	if r, ok := f.Receiver.(*coreexport.Receiver); ok {
		return typeName(r.Type) + "." + f.Name
	}
	return f.Name
}

// outsideSubset is the construct that puts function or method f outside
// the translated subset as a whole, "" when it is inside: for a method, type
// parameters of its own (generic-decl), or a receiver whose type is not a
// plain type of the program's (Box[T]: generic-type). A Ptr[T] receiver is
// a pointer receiver (ptrReceivers). A generic function is inside.
func outsideSubset(f *coreexport.Func) string {
	if f.Receiver == nil {
		return ""
	}
	r, ok := f.Receiver.(*coreexport.Receiver)
	switch {
	case !ok:
		return f.Receiver.(*coreexport.Unsupported).What
	case typeName(r.Type) == "":
		if u, isUnsup := r.Type.(*coreexport.Unsupported); isUnsup {
			return u.What
		}
		return "generic-type"
	case len(f.TypeParams) > 0:
		return "generic-decl"
	}
	return ""
}

// addMethod records method f. A method whose receiver is outside the subset
// (a generic type) is a declaration outside it: a call of it is reported,
// and its String or Equal, if it has one, decides how its type's values
// print or compare (markCustom).
func (g *gen) addMethod(f *coreexport.Func) {
	if what := outsideSubset(f); what != "" {
		g.outsideDecls = append(g.outsideDecls, &coreexport.Unsupported{What: what, Pos: f.Pos})
		// A method the translation does not see changes how values print
		// or compare only through its name, on its receiver's type; when
		// that type is not plain to see, any value might be affected.
		if t := receiverTypeName(f, g.ownPtrs); t != "" {
			g.markCustom(t, f.Name)
		} else {
			g.hasMethods = true
		}
		return
	}
	t := typeName(f.Receiver.(*coreexport.Receiver).Type)
	if g.methods[t] == nil {
		g.methods[t] = map[string]*coreexport.Func{}
	}
	g.methods[t][f.Name] = f
	k := g.key(f)
	g.byKey[k] = f
	g.methodsNamed[f.Name] = append(g.methodsNamed[f.Name], k)
	g.funcOrder = append(g.funcOrder, f)
	g.markCustom(t, f.Name)
}

// markCustom records that type t has a method named m that decides how its
// values print (fmt calls Format, else Error, else String: Go's fmt,
// "Printing"; GoString for %#v) or compare (Equal, docs/GALA.MD,
// "Equality").
func (g *gen) markCustom(t, m string) {
	switch customMethods[m] {
	case "print":
		g.customString[t] = true
	case "compare":
		g.customEqual[t] = true
	}
}

// customMethods are the method names that decide how a type's values print
// or compare (markCustom).
var customMethods = map[string]string{
	"String": "print", "Error": "print", "Format": "print", "GoString": "print",
	"Equal": "compare",
}

// receiverTypeName is the type whose values a method's receiver holds: T
// for `(c T)`, `(c Ptr[T])` and `(c T[A])`; "" when not plain to see.
func receiverTypeName(f *coreexport.Func, own map[string]bool) string {
	r, ok := f.Receiver.(*coreexport.Receiver)
	if !ok {
		return ""
	}
	tn, ok := r.Type.(*coreexport.TypeName)
	if !ok || tn.Package != "" {
		return ""
	}
	if tn.Name == "Ptr" && len(tn.Args) == 1 {
		// Std's Ptr, unless the method's package declares its own (own,
		// ownPtr), whose method it then is.
		if t := typeHead(tn.Args[0]); !own[pkgPrefix(t)] {
			return t
		}
	}
	return tn.Name
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
	g.comparableType(g.typeOf(e, sc), posOf(e))
}

// comparableType is comparable for a value of Lean type t at pos.
func (g *gen) comparableType(t string, pos coreexport.Pos) {
	// A value holding a function does not compare: Go rejects `==` on it,
	// and std.Equal, which a collection's Equal or Contains calls, panics.
	if strings.Contains(t, "→") {
		unsup(pos, "func-in-equal")
	}
	g.stateTypeIn(t, g.noBEq, pos, "func-in-equal")
	g.equalityOfParams(t, pos)
	g.customCheck(t, g.customEqual, &g.equalHolders, pos, "custom-equal")
	g.basicType(t, pos)
}

// customCheck stops the translation with construct when a value of Lean
// type t would go through a String or Equal method of its own: t names a
// type of custom or one
// holding one (cached in *holders), or t is not known while custom types
// exist.
func (g *gen) customCheck(t string, custom map[string]bool, holders *map[string]bool, pos coreexport.Pos, construct string) {
	if len(custom) == 0 {
		return
	}
	if t == "" || g.mentionsTypeParam(t) {
		// Not plain to see, or a type parameter, whose type argument may be
		// one of the custom types.
		unsup(pos, "%s", construct)
	}
	if *holders == nil {
		*holders = g.holders(custom)
	}
	g.stateTypeIn(t, *holders, pos, construct)
}

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
				if g.anyIn(n, f.Type, out) {
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
	// Lean puts a sealed type's cases in the type's namespace too.
	if v := g.variants[pkgPrefix(t)+f.Name]; v != nil && v.sealed.Name == t {
		unsup(f.Pos, "method-name-clash")
	}
	lt := g.leanType(r.Type)
	sc := newScope(nil, 0)
	ps := []string{}
	res := "Unit"
	if f.Result != nil {
		res = g.leanType(f.Result)
	}
	var body string
	if !statePassed(r) {
		ps = append(ps, fmt.Sprintf("(%s : %s)", ident(r.Name), lt))
		sc.bind(r.Name, false, lt).gtype = r.Type
		ps = append(ps, g.params(f, sc)...)
		g.fn = &fnCtx{result: res}
		body = indentLines(g.bodyItems(f, sc, res), 2)
	} else {
		g.receiverUses(f, r.Name, r.Inout)
		self := "_gala_self"
		ps = append(ps, fmt.Sprintf("(%s : %s)", self, lt))
		ps = append(ps, g.params(f, sc)...)
		// The receiver in a cell, so that the body writes it at any depth.
		c := g.bindCell(r.Name, r.Type, lt, self, r.Pos, sc)
		g.fn = &fnCtx{result: res}
		var items []string
		items, res = lendBody(g.bodyItems(f, newScope(sc, 0), res), []cell{c}, res)
		body = indentLines(items, 2)
	}
	s := fmt.Sprintf("def %s %s : _gala_M %s := do\n%s", methodHead(t, f.Name), strings.Join(ps, " "), res, body)
	if recursive {
		s += "\npartial_fixpoint"
	}
	return s
}

// params are a method's parameters in Lean, bound in sc. An `inout`
// parameter of a method is outside the subset; a function's are translated
// (funcParams).
func (g *gen) params(f *coreexport.Func, sc *scope) []string {
	var ps []string
	for _, pn := range f.Params {
		p := g.paramOf(pn)
		if p.Inout {
			unsup(p.Pos, "inout-method-param")
		}
		ps = append(ps, g.param(p, sc))
	}
	return ps
}

// paramOf is parameter node pn, which must have a written type.
func (g *gen) paramOf(pn coreexport.Node) *coreexport.Param {
	p, ok := pn.(*coreexport.Param)
	if !ok {
		unsupAt(pn)
	}
	if p.Type == nil {
		unsup(p.Pos, "untyped-param")
	}
	return p
}

// param is value parameter p in Lean, bound in sc.
func (g *gen) param(p *coreexport.Param, sc *scope) string {
	lt := g.leanType(p.Type)
	sc.bind(p.Name, false, lt).gtype = p.Type
	return fmt.Sprintf("(%s : %s)", ident(p.Name), lt)
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
// state passing does not model. With whole, the name itself is a value too
// (an `inout` place: reading it copies the value, assigning it replaces
// it), but still never in a lambda, which could keep the place past the
// call.
func (g *gen) receiverUses(f *coreexport.Func, name string, whole bool) {
	var visit func(n coreexport.Node, inLambda bool)
	visit = func(n coreexport.Node, inLambda bool) {
		switch n := n.(type) {
		case nil:
			return
		case *coreexport.Lambda:
			inLambda = true
		case *coreexport.Select:
			// p.f, p.f.g and p.m(...) reach the value through p.
			root, _ := placeRoot(n.Target)
			if id, ok := root.(*coreexport.Name); ok && id.Name == name {
				if inLambda {
					unsup(n.Pos, "ptr-escape")
				}
				return
			}
		case *coreexport.Name:
			if n.Name == name && (inLambda || !whole) {
				unsup(n.Pos, "ptr-escape")
			}
			return
		}
		walkChildren(n, func(c coreexport.Node) { visit(c, inLambda) })
	}
	visit(f.Body, false)
	// A parameter's default sees the receiver and the parameters before it.
	for _, pn := range f.Params {
		if p, ok := pn.(*coreexport.Param); ok {
			visit(p.Default, false)
		}
	}
}

// methodOf is method m of the type of value e, and that type's name, or
// nil. seen is exprType's cycle guard, nil outside it.
func (g *gen) methodOf(e coreexport.Node, m string, sc *scope, seen map[string]bool) (*coreexport.Func, string) {
	if seen == nil {
		seen = map[string]bool{}
	}
	n := g.typeByLean(g.exprType(e, sc, seen))
	if f := g.methods[n][m]; f != nil {
		g.use(g.key(f))
		return f, n
	}
	return nil, ""
}

// methodSig is the signature of method f of type t, its receiver aside,
// for the call at pos. A method with an `inout` parameter is outside the
// subset (params).
func (g *gen) methodSig(f *coreexport.Func, t string, pos coreexport.Pos) *sig {
	if hasInout(f.Params) {
		unsup(pos, "inout-method-param")
	}
	r := f.Receiver.(*coreexport.Receiver)
	s := funcSig(f, methodHead(t, f.Name))
	s.recvName, s.recvType = r.Name, g.optTypeSafe(r.Type)
	return s
}

// methodCall translates `x.m(args)` for a method m of x's type: x first, then
// the arguments. A pointer method's call is do-items (ptrCall); here it is
// a term, so the place it writes back must be one a nested do block can
// write (a cell or a package-level variable).
func (g *gen) methodCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	f, t := g.methodOf(s.Target, s.Name, sc, nil)
	if f == nil {
		return term{}, false
	}
	if statePassed(f.Receiver.(*coreexport.Receiver)) {
		items, res := g.ptrCall(e, sc.term())
		if items == nil {
			return term{}, false
		}
		return callTerm(items, res, f.Result == nil), true
	}
	sg := g.methodSig(f, t, s.Pos)
	recv := g.expr(s.Target, sc)
	sg.recv = &recv
	return g.callSig(sg, e, sc), true
}

// ptrCall translates a call of a pointer method on a place: the arguments
// are evaluated, then the place is read and the method called, and its new
// value written back. It returns the do-items and the name holding the
// call's result, or nil items when e is not such a call.
func (g *gen) ptrCall(e *coreexport.Call, sc *scope) ([]string, string) {
	if items, res := g.lendCall(e, sc); items != nil {
		return items, res
	}
	s, ok := e.Callee.(*coreexport.Select)
	if !ok {
		return nil, ""
	}
	f, t := g.methodOf(s.Target, s.Name, sc, nil)
	if f == nil || !statePassed(f.Receiver.(*coreexport.Receiver)) {
		return nil, ""
	}
	g.uniquePlace(s.Target, sc, s.Pos, "pointer-method-on-value")
	sg := g.methodSig(f, t, s.Pos)
	recv := g.expr(s.Target, sc)
	sg.recv = &recv
	sg.recvLast = true
	return g.writeBack(sg, e, []coreexport.Node{s.Target}, sc)
}

// uniquePlace stops the translation unless place e, written back after a
// call, is one only this code reaches (placeKind): none is reported as
// none, a shared one as ptr-escape, since another path reaching the
// variable while the callee runs (any function reads a package-level var;
// a lambda shares a captured one) would see Go's writes in place, which
// state passing does not model.
func (g *gen) uniquePlace(e coreexport.Node, sc *scope, pos coreexport.Pos, none string) {
	switch g.placeKind(e, sc) {
	case placeNone:
		unsup(pos, "%s", none)
	case placeShared:
		unsup(pos, "ptr-escape")
	}
}

// writeBack runs the state-passing call e of s, whose result is its own
// result and then the final values of places, in order, and writes each
// back to its place. It returns the do-items and the name holding the
// result.
func (g *gen) writeBack(s *sig, e *coreexport.Call, places []coreexport.Node, sc *scope) ([]string, string) {
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

// callTerm is a state-passing call's do-items and result name as a term:
// a do block of its own, so the places it writes back must be ones a
// nested block can write (cells, package-level variables).
func callTerm(items []string, res string, unit bool) term {
	return term{code: doTerm(append(items, "pure "+res)), unit: unit}
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
	root, _ := placeRoot(e)
	n, ok := root.(*coreexport.Name)
	if !ok {
		return placeNone
	}
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

// caseTest is case C of sealed type name when m is `isC`, the test GALA
// generates for it, and nil otherwise. A generic sealed type is left out:
// a method it declares is outside the subset (outsideSubset), so an `isC`
// of its own would not be seen, and would win over the generated one.
func (g *gen) caseTest(name, m string) *variantInfo {
	c, ok := strings.CutPrefix(m, "is")
	if !ok || g.sealeds[name] == nil || g.isGenericDecl(name) {
		return nil
	}
	if v := g.variants[pkgPrefix(name)+c]; v != nil && v.sealed.Name == name {
		return v
	}
	return nil
}

// generatedMethod translates a call of a method GALA generates, when the
// type does not declare its own: `Copy` with named arguments and `Equal`
// for every struct and sealed type, and `isC()` for each case C of a sealed
// type (caseTest).
func (g *gen) generatedMethod(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	t := g.typeOf(s.Target, sc)
	name := g.typeByLean(t)
	if name == "" || g.methods[name][s.Name] != nil {
		return term{}, false
	}
	if v := g.caseTest(name, s.Name); v != nil && len(e.Args) == 0 {
		x := g.expr(s.Target, sc)
		// A sealed type of one case has no other arm: Lean rejects a
		// redundant one.
		other := ""
		if len(g.sealeds[name].Variants) > 1 {
			other = " | _ => false"
		}
		return wrap(fmt.Sprintf("(match %s with | %s.%s .. => true%s)", x.val(), ident(name), ident(v.v.Name), other), x), true
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

// statePassed reports whether the method of receiver r is translated by
// state passing: a `*T` receiver, and an `inout` receiver, which lends the
// caller's place as a `*T` receiver does and lowers to one
// (docs/OWNERSHIP.MD#inout-basics).
func statePassed(r *coreexport.Receiver) bool { return r.Pointer || r.Inout }
