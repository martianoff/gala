package transformer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A type's own Equal that hands its own type to a generic function as a
// type argument constrained by comparable recurses forever: `==` on that
// type parameter lowers to std.EqualComparable, which calls the value's
// Equal, which calls the function again. `==` written in Equal itself is
// Go's (see equalityOf); a function it calls is compiled once for every
// caller, so it cannot be told apart there. The call is rejected instead
// (GALA-E0108).
//
// Equal reaches such a call three ways, all checked:
//   - it makes the call itself (checkEqualReentry, as the call is lowered);
//   - it calls a method of a generic type whose comparable type parameter
//     takes the type as its argument, `Pair(p, o).Same()`: the method is
//     compiled with that parameter, as a generic function is;
//   - it calls a function or method of the same file that makes such a call,
//     directly or through others of the file (checkIndirectEqualReentry,
//     once the file is lowered).
//
// What is not seen: a call through a function value, and functions and
// methods declared in another file or package. A generic function of the
// file that receives the value as a type parameter of its own is followed,
// but a call it makes with that parameter is not, since its type argument
// is known only at the outer call.

// checkEqualReentry rejects, inside the Equal method of a GALA type, a call
// of the generic function or method name whose type parameter, constrained
// by comparable or by a Go interface that embeds it, takes as its type
// argument the type Equal is defined on, or a type that holds it by value
// (Option[T], a tuple, a List[T]): its == would call Equal again. bound maps
// typeParams to the call's type arguments, as far as they are known.
//
// The call is also recorded for checkIndirectEqualReentry, wherever it is
// made: a function Equal calls may make it.
func (t *galaASTTransformer) checkEqualReentry(c callSrc, name string, typeParams []string, constraints map[string]transpiler.Type, bound typeSubstMap) error {
	if t.reentry == nil && t.equalOwner == nil {
		return nil
	}
	insts := t.comparableInstantiations(name, typeParams, constraints, bound)
	if g := t.reentry; g != nil && g.decl != "" {
		g.insts[g.decl] = append(g.insts[g.decl], t.sameAtEveryCall(insts)...)
	}
	if t.equalOwner == nil {
		return nil
	}
	// == calls only an Equal(T) bool.
	if _, usable := t.equalMethodOf(t.equalOwner); !usable {
		return nil
	}
	for _, in := range insts {
		if t.holdsEqualOwner(in.arg) {
			return t.equalReentryError(c.anchor(), nil, in)
		}
	}
	return nil
}

// checkMethodEqualReentry is checkEqualReentry for a call of method on a
// value of recvType, a GALA type whose metadata is meta, given with the
// receiver type's own type parameters: a method of `Pair[T comparable]` is
// compiled with T, as a generic function is, so `Pair(p, o).Same()` in
// Point's Equal is the call `same(p, o)` is. The call is recorded as well,
// so a function of the file that makes it is followed by
// checkIndirectEqualReentry.
func (t *galaASTTransformer) checkMethodEqualReentry(c callSrc, recvType transpiler.Type, lookupBaseName, method string) error {
	if method == "" || lookupBaseName == "" || (t.reentry == nil && t.equalOwner == nil) {
		return nil
	}
	t.noteReentryCall(c, t.reentryKey(lookupBaseName, method))
	meta := t.getTypeMeta(lookupBaseName)
	if meta == nil || len(meta.TypeParams) == 0 {
		return nil
	}
	if ptr, ok := recvType.(transpiler.PointerType); ok {
		recvType = ptr.Elem
	}
	bound := receiverTypeSubst(meta.TypeParams, recvType, nil)
	return t.checkEqualReentry(c, stripPackagePrefix(meta.Name)+"."+method, meta.TypeParams, meta.TypeParamConstraints, bound)
}

// reentryKey is the key of the method of typeName (method "" for a
// function, typeName its name), as the graph records a declaration and a
// call of it alike: a type of this package unqualified, as the source
// names it.
func (t *galaASTTransformer) reentryKey(typeName, method string) string {
	typeName = strings.TrimPrefix(typeName, t.packageName+".")
	if method == "" {
		return typeName
	}
	return typeName + "." + method
}

// noteReentryCall records that the declaration being lowered calls callee,
// a function (by name) or a method ("Type.Method"), at c. Only callees this
// file declares have calls of their own recorded, so only they are
// followed (checkIndirectEqualReentry).
func (t *galaASTTransformer) noteReentryCall(c callSrc, callee string) {
	if g := t.reentry; g != nil && g.decl != "" && callee != g.decl {
		g.calls[g.decl] = append(g.calls[g.decl], reentryCall{callee: callee, at: c.anchor()})
	}
}

// equalReentryGraph is what the declarations of a file call that
// checkIndirectEqualReentry follows: by declaration (a function's name, a
// method's "Type.Method"), the functions and methods each calls and the
// comparable type parameters its generic calls instantiate.
type equalReentryGraph struct {
	decl string // the declaration being lowered, "" outside one
	// recvParams is the type parameters decl's receiver declares: a call
	// that instantiates one is recorded, since an Equal of the same type
	// that calls decl binds them as it binds its own.
	recvParams map[string]bool
	calls      map[string][]reentryCall
	insts      map[string][]comparableInst
	equals     []equalDecl // the Equal methods of the file, in order
}

// newEqualReentryGraph is the graph a file of package pkg, whose types are
// types, records into; nil when no type of pkg declares an Equal: there is
// nothing to walk from, so nothing is recorded.
func newEqualReentryGraph(pkg string, types map[string]*transpiler.TypeMetadata) *equalReentryGraph {
	for _, meta := range types {
		if meta == nil || meta.Package != pkg {
			continue
		}
		if _, ok := meta.Methods["Equal"]; ok {
			return &equalReentryGraph{calls: map[string][]reentryCall{}, insts: map[string][]comparableInst{}}
		}
	}
	return nil
}

// reentryCall is a call of the function or method callee.
type reentryCall struct {
	callee string
	at     srcNode
}

// comparableInst is a call of the generic function or method callee whose
// type parameter tp, constrained by comparable, takes arg.
type comparableInst struct {
	callee, tp string
	arg        transpiler.Type
}

// equalDecl is the Equal method of owner, which == calls, declared as decl.
type equalDecl struct {
	decl  string
	owner *transpiler.TypeMetadata
}

// enterReentryDecl makes decl, a function or method declaration about to be
// lowered, the one calls are recorded for, and returns the function that
// restores the enclosing one. owner is the type whose Equal decl is, nil for
// any other declaration. It is called once the receiver's type parameters,
// and no others of decl, are bound.
func (t *galaASTTransformer) enterReentryDecl(decl string, owner *transpiler.TypeMetadata) func() {
	g := t.reentry
	if g == nil {
		return func() {}
	}
	if owner != nil {
		if _, usable := t.equalMethodOf(owner); usable {
			g.equals = append(g.equals, equalDecl{decl: decl, owner: owner})
		}
	}
	prevDecl, prevRecv := g.decl, g.recvParams
	g.decl, g.recvParams = decl, maps.Clone(t.activeTypeParams)
	return func() { g.decl, g.recvParams = prevDecl, prevRecv }
}

// comparableInstantiations is the type parameters of a call of name, among
// typeParams, constrained by comparable, with their type arguments in bound;
// one whose argument is not known is left out.
func (t *galaASTTransformer) comparableInstantiations(name string, typeParams []string, constraints map[string]transpiler.Type, bound typeSubstMap) []comparableInst {
	var insts []comparableInst
	for _, tp := range typeParams {
		arg := bound[tp]
		if arg == nil || transpiler.IsUnusableOrAny(arg) || !t.isComparableConstraint(constraints[tp]) {
			continue
		}
		insts = append(insts, comparableInst{callee: name, tp: tp, arg: arg})
	}
	return insts
}

// sameAtEveryCall is insts without those whose argument mentions a type
// parameter the declaration being lowered declares itself: that is a
// different type at each call of the declaration, which no caller's Equal
// is seen through. Its receiver's type parameters are kept: they are the
// ones an Equal of the same type is compiled with.
func (t *galaASTTransformer) sameAtEveryCall(insts []comparableInst) []comparableInst {
	if len(t.activeTypeParams) <= len(t.reentry.recvParams) {
		return insts
	}
	own := func(in comparableInst) bool {
		return typeNameMatches(in.arg, func(name string) bool { return t.isActiveTypeParam(name) && !t.reentry.recvParams[name] })
	}
	if !slices.ContainsFunc(insts, own) {
		return insts
	}
	return slices.DeleteFunc(slices.Clone(insts), own)
}

// holdsEqualOwner reports whether == on arg calls the Equal being
// transformed: arg is its type, or holds it by value.
func (t *galaASTTransformer) holdsEqualOwner(arg transpiler.Type) bool {
	return (&comparer{t: t, exhaustive: true, equalCalls: true}).walk(arg, nil, false).holdsOwner
}

// equalReentryError is the GALA-E0108 of in, made by Equal at at, through
// the calls via (none when Equal makes in itself).
func (t *galaASTTransformer) equalReentryError(at srcNode, via []string, in comparableInst) error {
	r := at.rng()
	owner := t.equalOwner.Name
	var path strings.Builder
	for _, callee := range via {
		// Keys spell a type of this package as the source does (reentryKey).
		path.WriteString(callee + ", which calls ")
	}
	return spanErrorAt(galaerr.CodeEqualReentry, r, r,
		fmt.Sprintf("%s's own Equal calls %s%s with %s = %s, a comparable type parameter: == on %s there calls %s's Equal, so the two call each other forever",
			owner, path.String(), in.callee, in.tp, displayType(in.arg), in.tp, owner),
		fmt.Sprintf("compare %s's fields instead", owner))
}

// checkIndirectEqualReentry rejects an Equal of the file that reaches,
// through the functions and methods of the file it calls, a call
// checkEqualReentry would reject in Equal itself: `eq(p, o)` for
// `func eq(a Point, b Point) bool = same(a, b)`. It is reported at the call
// in Equal that leads to it. Each declaration is visited once per Equal, so
// the walk ends on recursive helpers.
func (t *galaASTTransformer) checkIndirectEqualReentry() error {
	g := t.reentry
	if g == nil {
		return nil
	}
	for _, eq := range g.equals {
		prev := t.equalOwner
		t.equalOwner = eq.owner
		err := t.equalReentryVia(g, eq.decl)
		t.equalOwner = prev
		if err != nil {
			return err
		}
	}
	return nil
}

// equalReentryVia walks, depth first, from each call the Equal decl makes
// for one that reaches a rejected instantiation.
func (t *galaASTTransformer) equalReentryVia(g *equalReentryGraph, decl string) error {
	visited := map[string]bool{decl: true}
	var reach func(callee string, via []string) ([]string, *comparableInst)
	reach = func(callee string, via []string) ([]string, *comparableInst) {
		if visited[callee] {
			return nil, nil
		}
		visited[callee] = true
		via = append(via, callee)
		for _, in := range g.insts[callee] {
			if t.holdsEqualOwner(in.arg) {
				return via, &in
			}
		}
		for _, next := range g.calls[callee] {
			if path, in := reach(next.callee, via); in != nil {
				return path, in
			}
		}
		return nil, nil
	}
	for _, call := range g.calls[decl] {
		if via, in := reach(call.callee, nil); in != nil {
			return t.equalReentryError(call.at, via, *in)
		}
	}
	return nil
}
