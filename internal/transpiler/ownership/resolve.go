package ownership

import (
	"slices"
	"strings"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// goBuiltins are the names a call reaches Go's builtins or GALA's printing
// through.
var goBuiltins = map[string]bool{
	"Println": true, "Print": true, "panic": true, "len": true, "cap": true, "append": true, "copy": true,
	"delete": true, "new": true, "make": true, "print": true, "println": true, "min": true, "max": true,
	"close": true, "clear": true, "recover": true,
}

// collectionPackages are the packages whose functions and methods put
// their arguments in a collection.
var collectionPackages = map[string]bool{"collection_immutable": true, "collection_mutable": true}

// bareCallee resolves a call of the bare name `name` (not bound in the
// function): a function or type of the package or of a dot import, a
// builtin.
func (a *pkg) bareCallee(w *walker, name string) *callee {
	if d := a.funcs[qualified(a.name, name)]; d != nil {
		return ownCallee(d)
	}
	if goBuiltins[name] || valueBasics[name] {
		// A builtin, or a conversion to a basic type.
		return &callee{kind: calleeGo, name: name, noThunks: true}
	}
	prefixes := append([]string{a.name}, w.d.file.dots...)
	for _, p := range prefixes {
		key := qualified(p, name)
		if fm := a.rich.Functions[key]; fm != nil {
			return funcCallee(key, fm, collectionPackages[p])
		}
		if tm := a.rich.Types[key]; tm != nil && !tm.IsInterface {
			return constructorCallee(key, tm, collectionPackages[p])
		}
	}
	for _, p := range prefixes {
		if c := a.goFuncCallee(qualified(p, name)); c != nil {
			return c
		}
	}
	if _, ok := a.rich.CompanionObjects[name]; ok {
		return &callee{kind: calleeConstructor, name: name}
	}
	return &callee{kind: calleeUnknown, name: name}
}

// qualifiedCallee resolves `q.name`, q the key prefix of an imported
// package.
func (a *pkg) qualifiedCallee(q, name string) *callee {
	key := qualified(q, name)
	if fm := a.rich.Functions[key]; fm != nil {
		return funcCallee(key, fm, collectionPackages[q])
	}
	if tm := a.rich.Types[key]; tm != nil && !tm.IsInterface {
		return constructorCallee(key, tm, collectionPackages[q])
	}
	if c := a.goFuncCallee(key); c != nil {
		return c
	}
	if gi := a.rich.GoTypeInfo; gi != nil {
		if _, ok := gi.Types[key]; ok {
			return &callee{kind: calleeGo, name: key}
		}
	}
	if !a.galaPackage(q) {
		// A package without GALA metadata is Go, signature known or not.
		return &callee{kind: calleeGo, name: key}
	}
	return &callee{kind: calleeUnknown, name: key}
}

// galaPackage reports whether the metadata holds a GALA function or type of
// the package whose key prefix is q.
func (a *pkg) galaPackage(q string) bool {
	if a.galaPkgs == nil {
		a.galaPkgs = map[string]bool{}
		for k := range a.rich.Functions {
			if i := strings.LastIndex(k, "."); i > 0 {
				a.galaPkgs[k[:i]] = true
			}
		}
		for k := range a.rich.Types {
			if i := strings.LastIndex(k, "."); i > 0 {
				a.galaPkgs[k[:i]] = true
			}
		}
	}
	return a.galaPkgs[q]
}

// goFuncCallee is the Go function key ("pkg.Func"), nil when Go declares
// none: a Go package's, or one a hand-written .go file of a GALA package
// declares.
func (a *pkg) goFuncCallee(key string) *callee {
	gi := a.rich.GoTypeInfo
	if gi == nil {
		return nil
	}
	sig := gi.Functions[key]
	if sig == nil {
		return nil
	}
	return goSigCallee(key, sig)
}

func goSigCallee(name string, sig *transpiler.GoFuncSignature) *callee {
	c := &callee{kind: calleeGo, name: name, variadic: sig.IsVariadic}
	for _, p := range sig.Params {
		c.params = append(c.params, p.Type)
		c.pnames = append(c.pnames, p.Name)
	}
	if len(sig.Returns) >= 1 {
		c.result = sig.Returns[0]
	}
	return c
}

// goFieldType is the type of field name of a Go struct type t; nil when t
// is not one or has no such field.
func (a *pkg) goFieldType(t transpiler.Type, name string) transpiler.Type {
	gi := a.rich.GoTypeInfo
	if t == nil || gi == nil {
		return nil
	}
	if gt := gi.Types[a.typeKey(t)]; gt != nil {
		return gt.Fields[name]
	}
	return nil
}

// asValue records that callee c, one of the package's own functions or
// methods, is used as a function value: its pointer parameters and receiver
// may then come from anywhere, Go included.
func (a *pkg) asValue(c *callee) {
	if c != nil && c.own != nil {
		c.own.asValue = true
	}
}

// isGoType reports whether the composite literal type t names a Go type.
func (w *walker) isGoType(t native.TypeNode) bool {
	q := t.QualifiedIdentifier()
	if !q.Ok() {
		return false
	}
	ids := q.AllIdentifier()
	if len(ids) != 2 || w.local[ids[0].GetText()] {
		return false
	}
	pk, ok := w.d.file.quals[ids[0].GetText()]
	if !ok {
		return false
	}
	key := qualified(pk, ids[1].GetText())
	if w.a.rich.Types[key] != nil {
		return false
	}
	if gi := w.a.rich.GoTypeInfo; gi != nil && gi.Types[key] != nil {
		return true
	}
	return !w.a.galaPackage(pk)
}

// packageValType is the type of a package-level val of another package, nil
// when unknown.
func (a *pkg) packageValType(key string) transpiler.Type {
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return a.ownValType(key)
	}
	q, name := key[:i], key[i+1:]
	for path, vals := range a.rich.ImportedVals {
		if a.packageKey(path) == q {
			if t := knownType(vals[name]); t != nil {
				return t
			}
		}
	}
	return nil
}

// ownValType is the type of a package-level val or var of the package.
func (a *pkg) ownValType(name string) transpiler.Type {
	return knownType(a.rich.PackageVals[name])
}

func knownType(pv *transpiler.PackageValMetadata) transpiler.Type {
	if pv == nil || pv.Type == nil || pv.Type.IsNil() {
		return nil
	}
	return pv.Type
}

func ownCallee(d *decl) *callee {
	return &callee{kind: calleeGala, name: d.name, params: d.params, pnames: d.pnames, variadic: d.variadic,
		result: d.result, own: d, method: d.mmeta != nil, recvPtr: d.recvPtr}
}

// constructorCallee is a call of type key, tm its metadata: a case's
// companion Apply, or a struct's fields in order.
func constructorCallee(key string, tm *transpiler.TypeMetadata, collection bool) *callee {
	c := &callee{kind: calleeConstructor, name: key, result: namedOfKey(key), collection: collection}
	if apply := tm.Methods["Apply"]; apply != nil && apply.OfTypeDecl {
		c.params, c.pnames, c.variadic = apply.ParamTypes, apply.ParamNames, apply.Variadic
		if apply.ReturnType != nil {
			c.result = apply.ReturnType
		}
		return c
	}
	for _, f := range tm.FieldNames {
		c.params = append(c.params, unwrapImmutable(tm.Fields[f]))
		c.pnames = append(c.pnames, f)
	}
	c.noThunks = len(c.params) == 0
	return c
}

func funcCallee(key string, fm *transpiler.FunctionMetadata, collection bool) *callee {
	return &callee{kind: calleeGala, name: key, params: fm.ParamTypes, pnames: fm.ParamNames, variadic: fm.Variadic,
		result: notTypeParam(fm.ReturnType, fm.TypeParams), meta: fm.Borrow, collection: collection}
}

// methodCallee resolves method name of a value of type t (or of the pointee
// of a pointer t); nil when t has no such method or is unknown.
func (a *pkg) methodCallee(t transpiler.Type, name string) *callee {
	if t == nil {
		return nil
	}
	tm, sub := a.typeMeta(t)
	key := a.typeKey(t)
	if tm != nil {
		if m := tm.Methods[name]; m != nil {
			c := &callee{kind: calleeGala, name: strings.TrimPrefix(key, a.name+".") + "." + name,
				params: substituteAll(m.ParamTypes, sub), pnames: m.ParamNames, variadic: m.Variadic,
				result: notTypeParam(substitute(m.ReturnType, sub), m.TypeParams), meta: m.Borrow, method: true, recvPtr: m.PointerReceiver,
				collection: collectionPackages[tm.Package]}
			if d := a.methods[key+"#"+name]; d != nil {
				c.own = d
			}
			if m.GoDeclared {
				c.kind = calleeGo
			}
			return c
		}
		if transpiler.GeneratedMethodNames(tm)[name] {
			c := &callee{kind: calleeGala, name: key + "." + name, method: true, meta: &transpiler.BorrowSummary{}}
			switch name {
			case "Copy":
				c.kind, c.result = calleeConstructor, t
			case "String":
				// Printed with fmt, field by field.
				c.kind = calleeGo
			case "Equal":
				c.kind = calleeEqual
			}
			return c
		}
	}
	if gi := a.rich.GoTypeInfo; gi != nil {
		// A Go type's method, or one a hand-written .go file declares on a
		// GALA type.
		for _, gt := range []*transpiler.GoTypeData{gi.Types[key], gi.GalaTypeMethods[key]} {
			if gt == nil {
				continue
			}
			if sig := gt.Methods[name]; sig != nil {
				c := goSigCallee(key+"."+name, sig)
				c.method = true
				// Go-declared: assume a pointer receiver unless known not.
				c.recvPtr = gt.PointerMethods == nil || gt.PointerMethods[name] || gt.Kind == transpiler.GoKindMethodsOnly
				return c
			}
		}
	}
	return nil
}

// notTypeParam is t, or nil when t is one of the type parameters params: a
// result whose type the call's type arguments decide.
func notTypeParam(t transpiler.Type, params []string) transpiler.Type {
	var name string
	switch tt := t.(type) {
	case transpiler.NamedType:
		if tt.Package != "" {
			return t
		}
		name = tt.Name
	case transpiler.BasicType:
		name = tt.Name
	default:
		return t
	}
	if slices.Contains(params, name) {
		return nil
	}
	return t
}

func substituteAll(ts []transpiler.Type, sub map[string]transpiler.Type) []transpiler.Type {
	if len(sub) == 0 {
		return ts
	}
	out := make([]transpiler.Type, len(ts))
	for i, t := range ts {
		out[i] = substitute(t, sub)
	}
	return out
}

// peekCallee resolves, without walking, the callee of p when p is a call of
// a bare name, a qualified name or a method of a typed name: `f(…)`,
// `pkg.f(…)`, `x.m(…)`. Anything else is unknown.
func (w *walker) peekCallee(p native.PostfixExprNode) *callee {
	unknown := &callee{kind: calleeUnknown}
	sufs := p.AllPostfixSuffix()
	if len(sufs) == 0 || !isCall(sufs[len(sufs)-1]) || p.MATCH().Ok() {
		return unknown
	}
	name, ok := primIdent(p.PrimaryExpr())
	if !ok {
		return unknown
	}
	switch len(sufs) {
	case 1:
		if w.local[name] || w.binds[name] != nil {
			return unknown
		}
		return w.a.bareCallee(w, name)
	case 2:
		if !sufs[0].Identifier().Ok() {
			return unknown
		}
		m := sufs[0].Identifier().GetText()
		switch b := w.binds[name]; {
		case b != nil:
			if c := w.a.methodCallee(b.pointee, m); c != nil {
				return c
			}
		case w.typed(name) != nil:
			if c := w.a.methodCallee(w.typed(name), m); c != nil {
				return c
			}
		case !w.local[name]:
			if q, ok := w.d.file.quals[name]; ok {
				return w.a.qualifiedCallee(q, m)
			}
		}
	}
	return unknown
}
