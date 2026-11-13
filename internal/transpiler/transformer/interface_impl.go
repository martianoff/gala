package transformer

import (
	"cmp"
	"fmt"
	"go/ast"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Implementing an interface.
//
// A value of a concrete type fills an interface-typed slot (an argument, a
// declared val, a result, a field, an element, an assignment) only when the
// type implements the interface under Go's rules: each method the interface
// declares is in the type's method set with an identical signature. Go has no
// covariant results, so `Tail() Array[int]` does not implement
// `Tail() List[int]` of `Iterable[int, List[int]]`. GALA-E0100 reports the first method that fails,
// rather than leaving `go build` to report it against the generated code.
//
// The check judges only what it can read exactly. A value or slot type it
// cannot resolve, a type of a Go package, a method a .go file of the package
// declares, one the transformer generates (Equal, Copy, String on a sealed
// type, ...), and a signature with a type it cannot compare are left to Go.
// So everything it reports also fails in Go.

// ifaceMethod is a method an interface requires, instantiated with the slot's
// type arguments. exact is false when its signature could not be read
// exactly; only its presence is checked then.
type ifaceMethod struct {
	name     string
	params   []transpiler.Type
	results  []transpiler.Type
	variadic bool
	exact    bool
}

// implType is the concrete type of a value filling an interface slot: its
// metadata, its type arguments, and whether the value is a pointer to it
// (whose method set holds the pointer-receiver methods too).
type implType struct {
	meta    *transpiler.TypeMetadata
	args    []transpiler.Type
	pointer bool
}

// checkInterfaceSlot rejects expr, the lowered value written at at that fills
// a slot of type slotType, when slotType is an interface its concrete type
// does not implement (GALA-E0100).
func (t *galaASTTransformer) checkInterfaceSlot(expr ast.Expr, slotType transpiler.Type, at srcNode) error {
	if isNilIdent(expr) {
		return nil
	}
	return t.checkInterfaceSlotType(func() transpiler.Type { return t.probeExprType(expr) }, slotType, at)
}

// checkInterfaceSlotType is checkInterfaceSlot for a value of the type
// valType returns, read only when slotType is an interface with methods.
func (t *galaASTTransformer) checkInterfaceSlotType(valTypeOf func() transpiler.Type, slotType transpiler.Type, at srcNode) error {
	if !at.ok() || transpiler.IsUnusableOrAny(slotType) {
		return nil
	}
	slotType = t.followAliasChain(slotType)
	required, ok := t.requiredMethods(slotType)
	if !ok {
		return nil
	}
	valType := valTypeOf()
	impl, ok := t.implTypeOf(valType)
	if !ok {
		return nil
	}
	for _, m := range required() {
		why, hint := t.implMismatch(impl, m)
		if why == "" {
			continue
		}
		r := at.rng()
		return spanErrorAt(galaerr.CodeInterfaceNotImplemented, r, r,
			fmt.Sprintf("%s does not implement %s: %s", displayType(valType), displayType(slotType), why),
			hint)
	}
	return nil
}

// checkSlotFill is checkPtrSlot then checkInterfaceSlot, for a value that
// fills a slot of type typ outside lowerAgainst: a target of a multiple
// assignment, a tail call's argument, a clause of a bind group's Zip.
func (t *galaASTTransformer) checkSlotFill(expr ast.Expr, typ transpiler.Type, at srcNode) error {
	if err := t.checkPtrSlot(expr, typ, at); err != nil {
		return err
	}
	return t.checkInterfaceSlot(expr, typ, at)
}

// requiredMethods returns, to be listed once a value is known to need them,
// the methods slotType requires when it is an interface with methods whose
// signatures are known: `error`, a GALA interface (instantiated with the
// slot's type arguments), or a Go interface. ok is false for any other type,
// or an interface the check cannot read.
func (t *galaASTTransformer) requiredMethods(slotType transpiler.Type) (func() []ifaceMethod, bool) {
	// Only a named type can be an interface with methods.
	switch s := slotType.(type) {
	case transpiler.BasicType:
		if s.Name == "error" {
			return func() []ifaceMethod {
				return []ifaceMethod{{name: "Error", results: []transpiler.Type{transpiler.BasicType{Name: "string"}}, exact: true}}
			}, true
		}
		if transpiler.IsPrimitiveType(s.Name) {
			return nil, false
		}
	case transpiler.NamedType, transpiler.GenericType:
	default:
		return nil, false
	}
	// A GALA type is what its GALA declaration says, though a Go type may
	// share its key (GALA's `fs.FileInfo` struct beside io/fs's interface).
	nt, named := slotType.(transpiler.NamedType)
	meta := t.getTypeMeta(slotType.BaseName())
	goTyped := (named && t.isGoTyped(nt)) || goDeclared(meta)
	if meta != nil && !meta.IsInterface && !goTyped {
		return nil, false
	}
	if meta == nil || goTyped {
		if t.goTypeInfo == nil {
			return nil, false
		}
		if td := t.goTypeInfo.GetTypeData(t.goTypeLookupName(slotType)); td != nil && td.Kind == "interface" && len(td.TypeParams) == 0 && len(td.Methods) > 0 {
			return func() []ifaceMethod { return goInterfaceMethods(td) }, true
		}
		return nil, false
	}
	var typeArgs []transpiler.Type
	if g, ok := slotType.(transpiler.GenericType); ok {
		typeArgs = g.Params
	}
	if !meta.IsInterface || len(meta.Methods) == 0 || len(typeArgs) != len(meta.TypeParams) {
		return nil, false
	}
	for _, m := range meta.Methods {
		if len(m.TypeParams) > 0 {
			return nil, false
		}
	}
	return func() []ifaceMethod {
		methods := slices.Collect(maps.Values(meta.Methods))
		// The first mismatch reported is the first method the interface declares.
		slices.SortFunc(methods, func(a, b *transpiler.MethodMetadata) int {
			return cmp.Or(cmp.Compare(a.Pos.Line, b.Pos.Line), cmp.Compare(a.Pos.Column, b.Pos.Column), strings.Compare(a.Name, b.Name))
		})
		out := make([]ifaceMethod, len(methods))
		for i, m := range methods {
			out[i] = t.instantiateMethod(m, meta.TypeParams, typeArgs)
		}
		return out
	}, true
}

// goInterfaceMethods returns the methods of td, a non-generic Go interface,
// sorted by name. Its method set holds the methods of the interfaces it
// embeds.
func goInterfaceMethods(td *transpiler.GoTypeData) []ifaceMethod {
	out := make([]ifaceMethod, 0, len(td.Methods))
	for name, sig := range td.Methods {
		m := ifaceMethod{name: name, results: sig.Returns, variadic: sig.IsVariadic, exact: !sig.Inexact && !sig.IsVariadic && len(sig.TypeParams) == 0}
		for _, p := range sig.Params {
			m.params = append(m.params, p.Type)
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b ifaceMethod) int { return strings.Compare(a.name, b.name) })
	return out
}

// instantiateMethod is m's signature with typeParams bound to typeArgs. Its
// results are the Go results it declares (`(int, error)`), else its one
// result type, else none.
func (t *galaASTTransformer) instantiateMethod(m *transpiler.MethodMetadata, typeParams []string, typeArgs []transpiler.Type) ifaceMethod {
	sub := func(typ transpiler.Type) transpiler.Type {
		if len(typeParams) == 0 {
			return typ
		}
		return t.substituteConcreteTypes(typ, typeParams, typeArgs)
	}
	out := ifaceMethod{name: m.Name, variadic: m.Variadic, exact: m.ReturnType != nil}
	for _, p := range m.ParamTypes {
		out.params = append(out.params, sub(p))
	}
	switch {
	case m.GoResults != nil:
		for _, r := range m.GoResults {
			out.results = append(out.results, sub(r))
		}
	case transpiler.HasResult(m.ReturnType):
		out.results = []transpiler.Type{sub(m.ReturnType)}
	}
	return out
}

// implTypeOf returns the GALA type a value of type typ has, or that typ
// points to, when it is a concrete type with metadata: a struct, a sealed
// type, an opaque or other named type declared in GALA. ok is false for an
// interface, a type parameter, a Go type, a type of no known declaration, or
// a generic type whose type arguments are not all known.
func (t *galaASTTransformer) implTypeOf(typ transpiler.Type) (implType, bool) {
	if transpiler.IsUnusable(typ) {
		return implType{}, false
	}
	typ = t.followAliasChain(typ)
	var out implType
	if p, ok := typ.(transpiler.PointerType); ok {
		out.pointer = true
		typ = t.followAliasChain(p.Elem)
	}
	base := typ
	if g, ok := typ.(transpiler.GenericType); ok {
		base, out.args = g.Base, g.Params
	}
	// nominal also turns away Immutable and Sendable, which Go sees as
	// another type.
	if _, _, ok := nominal(base); !ok {
		return implType{}, false
	}
	if n, ok := base.(transpiler.NamedType); ok && t.isGoTyped(n) {
		return implType{}, false
	}
	out.meta = t.getTypeMeta(base.BaseName())
	if out.meta == nil || out.meta.IsInterface || goDeclared(out.meta) || len(out.args) != len(out.meta.TypeParams) || slices.ContainsFunc(out.args, transpiler.IsUnusable) {
		return implType{}, false
	}
	return out, true
}

// implMismatch says why impl does not implement the interface method want —
// it has no such method, has it on a pointer receiver only, or declares
// other parameter or result types — and how to fix it. "" when it does, or
// when that cannot be told exactly.
func (t *galaASTTransformer) implMismatch(impl implType, want ifaceMethod) (why, hint string) {
	signatureHint := "declare " + want.name + " with the interface's exact signature"
	m := impl.meta.Methods[want.name]
	if m == nil {
		// Without Go type info, methods .go files of the package declare are
		// not known.
		_, inGo := t.goMethodsOnGalaType(impl.meta)[want.name]
		if t.goTypeInfo == nil || inGo ||
			t.mayBeGeneratedMethod(impl.meta, want.name) || t.declaredOnAlias(impl.meta, want.name) {
			return "", ""
		}
		return "it has no method " + want.name, signatureHint
	}
	// A method with type parameters of its own is lowered to a function; Go
	// reports it as missing, but its spelling there is the transformer's.
	if len(m.TypeParams) > 0 || m.IsGeneric {
		return "", ""
	}
	if m.PointerReceiver && !impl.pointer {
		return fmt.Sprintf("method %s has a pointer receiver, so only a pointer to the value implements it", want.name),
			"pass a pointer to the value, or declare " + want.name + " on a value receiver"
	}
	if !want.exact {
		return "", ""
	}
	have := t.instantiateMethod(m, impl.meta.TypeParams, impl.args)
	if !have.exact {
		return "", ""
	}
	if why := t.signatureMismatch(have, want); why != "" {
		return why, signatureHint
	}
	return "", ""
}

// signatureMismatch says how the signature of have, a method, differs from
// want, the one an interface declares: "" when they are identical or that
// cannot be told.
func (t *galaASTTransformer) signatureMismatch(have, want ifaceMethod) string {
	if same, known := t.identicalTypeLists(have.params, want.params); known && (!same || have.variadic != want.variadic) {
		return fmt.Sprintf("method %s takes (%s), not (%s)", want.name, spellParams(have), spellParams(want))
	}
	if same, known := t.identicalTypeLists(have.results, want.results); known && !same {
		return fmt.Sprintf("method %s returns %s, not %s", want.name, spellResults(have.results), spellResults(want.results))
	}
	return ""
}

// mayBeGeneratedMethod reports whether name may be a method the transformer
// generates on meta's type rather than one declared on it: Copy, Equal and
// Unapply on a struct; Copy, Equal, String and the case predicates
// (`isCircle`, `IsCircle`) on a sealed type; Hash and Compare on an opaque
// type.
func (t *galaASTTransformer) mayBeGeneratedMethod(meta *transpiler.TypeMetadata, name string) bool {
	if meta.IsOpaque {
		return t.isSynthesizedOpaqueMethod(meta, name)
	}
	return transpiler.GeneratedMethodNames(meta)[name] || transpiler.SealedGoPredicateNames(meta)[name]
}

// declaredOnAlias reports whether a GALA alias of meta's type declares name
// (`type Coord Point; func (c Coord) Sum() int`): Go sees the alias as the
// type itself, so the method is the type's.
func (t *galaASTTransformer) declaredOnAlias(meta *transpiler.TypeMetadata, name string) bool {
	for alias, target := range t.typeAliases {
		if t.getTypeMeta(t.followAliasChain(target).BaseName()) != meta {
			continue
		}
		if am := t.getTypeMeta(alias); am != nil && am != meta && am.Methods[name] != nil {
			return true
		}
	}
	return false
}

// identicalTypeLists reports whether as and bs are pairwise identical types,
// and whether that is known: false when some pair cannot be compared exactly.
func (t *galaASTTransformer) identicalTypeLists(as, bs []transpiler.Type) (same, known bool) {
	if len(as) != len(bs) {
		return false, true
	}
	same = true
	for i := range as {
		s, k := t.identicalTypes(as[i], bs[i])
		if !k {
			return false, false
		}
		same = same && s
	}
	return same, true
}

// identicalTypes reports whether a and b are the same Go type, and whether
// that is known. Aliases are followed; a named type is told by its name
// alone (two types of one name in different packages count as the same, which
// leaves such a mismatch to Go). A channel, an anonymous struct or interface,
// an Immutable or a Sendable marker, or an unresolved type is not compared.
func (t *galaASTTransformer) identicalTypes(a, b transpiler.Type) (same, known bool) {
	if transpiler.IsUnusable(a) || transpiler.IsUnusable(b) {
		return false, false
	}
	a, b = t.followAliasChain(a), t.followAliasChain(b)
	switch x := a.(type) {
	case transpiler.PointerType:
		if y, ok := b.(transpiler.PointerType); ok {
			return t.identicalTypes(x.Elem, y.Elem)
		}
	case transpiler.ArrayType:
		if y, ok := b.(transpiler.ArrayType); ok {
			if x.Fixed != y.Fixed || x.Len != y.Len {
				return false, true
			}
			return t.identicalTypes(x.Elem, y.Elem)
		}
	case transpiler.MapType:
		if y, ok := b.(transpiler.MapType); ok {
			return t.identicalTypeLists([]transpiler.Type{x.Key, x.Elem}, []transpiler.Type{y.Key, y.Elem})
		}
	case transpiler.FuncType:
		if y, ok := b.(transpiler.FuncType); ok {
			sameParams, knownParams := t.identicalTypeLists(x.Params, y.Params)
			sameResults, knownResults := t.identicalTypeLists(x.Results, y.Results)
			return sameParams && sameResults, knownParams && knownResults
		}
	}
	nameA, argsA, okA := nominal(a)
	nameB, argsB, okB := nominal(b)
	if !okA || !okB {
		return false, false
	}
	if nameA != nameB {
		return false, true
	}
	return t.identicalTypeLists(argsA, argsB)
}

// nominal splits a type named by a name (with type arguments, for a generic
// type) into that name, without its package, and its type arguments. ok is
// false for any other type, and for a marker Go does not see as written
// (Immutable, Sendable).
func nominal(typ transpiler.Type) (name string, args []transpiler.Type, ok bool) {
	if g, isGeneric := typ.(transpiler.GenericType); isGeneric {
		typ, args = g.Base, g.Params
	}
	switch b := typ.(type) {
	case transpiler.BasicType:
		name = b.Name
	case transpiler.NamedType:
		name = b.Name
	default:
		return "", nil, false
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case transpiler.TypeImmutable, transpiler.TypeSendable:
		return "", nil, false
	case "interface{}":
		name = "any"
	}
	return canonicalScalar(name), args, true
}

// goDeclared reports whether meta is metadata the analyzer synthesized for a
// type of a Go package: its methods' results are recorded as GALA values
// (Try, Tuple), not as Go declares them.
func goDeclared(meta *transpiler.TypeMetadata) bool {
	if meta == nil {
		return false
	}
	for _, m := range meta.Methods {
		if m.GoDeclared {
			return true
		}
	}
	return false
}

// spellParams is m's parameter types as GALA spells them.
func spellParams(m ifaceMethod) string {
	n := len(m.params)
	if !m.variadic || n == 0 {
		return joinDisplayTypes(m.params)
	}
	last := "..." + displayType(m.params[n-1])
	if n == 1 {
		return last
	}
	return joinDisplayTypes(m.params[:n-1]) + ", " + last
}

// spellResults is a result list as GALA spells it: nothing, one type, or a
// parenthesized list.
func spellResults(results []transpiler.Type) string {
	switch len(results) {
	case 0:
		return "nothing"
	case 1:
		return displayType(results[0])
	}
	return "(" + joinDisplayTypes(results) + ")"
}
