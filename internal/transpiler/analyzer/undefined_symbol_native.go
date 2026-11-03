package analyzer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
	"martianoff/gala/internal/transpiler/scopewalk/nativewalk"
)

// The undefined-symbol check (GALA-E0023) on galop's native tree, the tree
// the file's declarations were collected on (see collect_native.go). The
// functions here read the tree; the decisions are the checker methods'
// (undefined_symbol.go).

// walkSourceFileNative drives the shared walker over root, a file's native
// tree. Only declarations that hold value-position expressions are entered: a
// `type` / `sealed type` / `embed` declaration contains types and string
// literals, never a value this check inspects.
func (c *undefChecker) walkSourceFileNative(root native.GalaNodeRef) {
	w := c.walker
	nw := nativewalk.New(w, bindFunctionDeclarationScopeNative)
	w.PushScope()
	defer w.PopScope()
	c.bindFileLevelNamesNative(root)

	for decl := range nativeTopLevelDecls(root) {
		switch decl.Kind() {
		case native.GalaKIND_functionDeclaration:
			nw.WalkFunctionDeclaration(native.FunctionDeclarationNode{GalaNodeRef: decl})
		case native.GalaKIND_valDeclaration:
			nw.Walk(native.ValDeclarationNode{GalaNodeRef: decl}.ExpressionList().GalaNodeRef)
		case native.GalaKIND_varDeclaration:
			nw.Walk(native.VarDeclarationNode{GalaNodeRef: decl}.ExpressionList().GalaNodeRef)
		case native.GalaKIND_structShorthandDeclaration:
			d := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			w.PushScope()
			bindTypeParametersNative(w, d.TypeParameters())
			nativewalk.BindParameters(w, d.Parameters())
			nw.WalkParameterDefaults(d.Parameters())
			w.PopScope()
		}
	}
}

// bindFileLevelNamesNative registers the tuple-pattern package vals and vars
// (`val (a, b) = ...`) of root, a file's native tree, so they bind whatever
// metadata the check runs against, and `embed val` directives, which no
// richAST metadata lists.
func (c *undefChecker) bindFileLevelNamesNative(root native.GalaNodeRef) {
	for _, d := range c.rich.EmbedDirectives {
		c.walker.Bind(d.VarName)
	}
	for decl := range nativeTopLevelDecls(root) {
		var tp native.TuplePatternNode
		switch decl.Kind() {
		case native.GalaKIND_valDeclaration:
			tp = native.ValDeclarationNode{GalaNodeRef: decl}.TuplePattern()
		case native.GalaKIND_varDeclaration:
			tp = native.VarDeclarationNode{GalaNodeRef: decl}.TuplePattern()
		case native.GalaKIND_embedDeclaration:
			nativewalk.BindID(c.walker, native.EmbedDeclarationNode{GalaNodeRef: decl}.Identifier())
		}
		nativewalk.BindIdentifierList(c.walker, tp.IdentifierList())
	}
}

// bindFunctionDeclarationScopeNative binds a function or method declaration's
// own type parameters, and, for a method, its receiver name together with any
// type parameters the receiver type introduces (`func (l *List[T]) …` brings
// both `l` and `T` into scope).
//
// The receiver's type arguments are the method's view of the type's
// parameters; they can sit behind a pointer or nest (`*List[T]`, `Pair[K,
// V]`), so every identifier in the receiver type is bound. Binding the type's
// own name alongside them is harmless — it is a declaration in this package
// either way.
func bindFunctionDeclarationScopeNative(w *scopewalk.Walker, fn native.FunctionDeclarationNode) {
	bindTypeParametersNative(w, fn.TypeParameters())
	recv := fn.Receiver()
	if !recv.Ok() {
		return
	}
	nativewalk.BindID(w, recv.Identifier())
	nativewalk.BindIdentifiersIn(w, recv.Type_().GalaNodeRef)
}

// bindTypeParametersNative binds the name of each type parameter tps
// declares.
func bindTypeParametersNative(w *scopewalk.Walker, tps native.TypeParametersNode) {
	for p := range galaparser.NativeKids(tps.TypeParameterList().GalaNodeRef, native.GalaKIND_typeParameter) {
		nativewalk.BindID(w, native.TypeParameterNode{GalaNodeRef: p}.Identifier(0))
	}
}

// checkTypeNamesNative checks the type names written in TYPE positions of
// root, a file's native tree. It reports a package qualifier that no import
// brings into scope, so `var sb strings.Builder` in a file that never imports
// `strings` is a GALA diagnostic rather than a Go one, and an unqualified type
// name that does not resolve.
//
// The shared scope walker is a VALUE-reference walker: it short-circuits type
// nodes by design, because a type name is not a value and leaking one into the
// reference stream would break the capture analysis built on the same walker.
// That left type positions unchecked. The call half of
//
//	var sb strings.Builder                 // unreported
//	sb.WriteString(strings.Repeat("-", n)) // GALA-E0023
//
// was caught and the type half was not; a file whose ONLY offending use was a
// type position got no GALA error at all and fell through to `go build`.
//
// It is a method rather than a free function so it shares the caller's
// qualifier set, error slice and `reported` map; an earlier revision built a
// second checker and reported a doubly-missing qualifier twice.
//
// Only the QUALIFIER is checked, never the member. `strings.Builder` asks
// whether `strings` is in scope, not whether it exports `Builder` — the
// qualifier is unambiguously a package name, while resolving the member would
// need the full type surface of every imported Go package and would misfire
// whenever that surface is incomplete (no Go SDK, an unanalyzed package).
//
// An unqualified type name is reported when nothing declares it (see
// typeNameExists), or when only GALA packages out of this file's scope do
// (see galaScope), unless the file binds it itself (see
// collectFileTypeBindersNative). It uses the value check's symbol table, so it
// stands down under the same conditions (see fileImportsFullyLoaded).
//
// In the signature of a method whose receiver type is declared in another
// file of the package, the packages that file dot-imports count as well — the
// allowance GALA-E0025 makes for method signatures. receiverImports returns
// them. The method body gets no allowance, for type and value names alike.
//
// Nested type nodes are visited too: `map[string]strings.Builder` and
// `[]pkg.T` carry their named type below an outer type.
func (c *undefChecker) checkTypeNamesNative(root native.GalaNodeRef) {
	c.inTypePass = true
	defer func() { c.inTypePass = false }()
	typeParams := collectFileTypeBindersNative(root)
	var walk func(n native.GalaNodeRef, extra map[string]bool)
	walk = func(n native.GalaNodeRef, extra map[string]bool) {
		if n.Kind() == native.GalaKIND_functionDeclaration {
			if recv := (native.FunctionDeclarationNode{GalaNodeRef: n}).Receiver(); recv.Ok() {
				var sigExtra map[string]bool
				if rt := recv.Type_(); rt.Ok() {
					sigExtra = c.receiverImports(receiverBaseTypeNameNative(rt))
				}
				for i := range n.ChildCount() {
					child := n.Child(i).Node()
					if !child.Ok() {
						continue
					}
					switch child.Kind() {
					case native.GalaKIND_block, native.GalaKIND_expression:
						walk(child, nil)
					default:
						walk(child, sigExtra)
					}
				}
				return
			}
		}
		if n.Kind() == native.GalaKIND_type {
			c.checkTypeNameNative(native.TypeNode{GalaNodeRef: n}, typeParams, extra)
		}
		c.checkBareTypePositionsNative(n, typeParams, extra)
		skip := tupleDestructureTypeNative(n)
		for i := range n.ChildCount() {
			if child := n.Child(i).Node(); child.Ok() && child != skip {
				walk(child, extra)
			}
		}
	}
	walk(root, nil)
}

// checkTypeNameNative checks the type name a single type node t spells, if
// any: a bare name against checkBareTypeName, a qualified one's qualifier
// against checkTypeQualifier.
func (c *undefChecker) checkTypeNameNative(t native.TypeNode, typeParams, extra map[string]bool) {
	qi := t.QualifiedIdentifier()
	if !qi.Ok() {
		return
	}
	name, at := nativewalk.IdentifierSite(qi.Identifier(0).GalaNodeRef)
	if !qi.Identifier(1).Ok() {
		c.checkBareTypeName(name, at, typeParams, extra)
		return
	}
	c.checkTypeQualifier(name, at)
}

// checkBareTypePositionsNative checks the bare-identifier type positions n
// holds directly — a type alias's identifier, a type parameter's constraint,
// and the untyped parameters of a function type's signature; see
// checkBareTypeName.
func (c *undefChecker) checkBareTypePositionsNative(n native.GalaNodeRef, typeParams, extra map[string]bool) {
	check := func(id native.GalaNodeRef) {
		name, at := nativewalk.IdentifierSite(id)
		c.checkBareTypeName(name, at, typeParams, extra)
	}
	switch n.Kind() {
	case native.GalaKIND_typeAlias:
		if id := (native.TypeAliasNode{GalaNodeRef: n}).Identifier(); id.Ok() {
			check(id.GalaNodeRef)
		}
	case native.GalaKIND_typeParameter:
		// The constraint, of a parameter of exactly two identifiers.
		tp := native.TypeParameterNode{GalaNodeRef: n}
		if second := tp.Identifier(1); second.Ok() && !tp.Identifier(2).Ok() {
			check(second.GalaNodeRef)
		}
	case native.GalaKIND_type:
		params := native.TypeNode{GalaNodeRef: n}.Signature().Parameters()
		for _, p := range nativeParams(params) {
			if id := p.Identifier(); id.Ok() && !p.Type_().Ok() {
				check(id.GalaNodeRef)
			}
		}
	}
}

// tupleDestructureTypeNative returns the type node a `val (a, b)` / `var (a,
// b)` declaration n was parsed with, or no node. A destructuring takes no
// type, and GALA-E0056 rejects one with the message that fits. The grammar is
// blind to newlines, so for `var (a, b)` the first identifier of the next line
// parses as this type: checking it as a type name would report that
// identifier instead.
func tupleDestructureTypeNative(n native.GalaNodeRef) native.GalaNodeRef {
	var tuple native.TuplePatternNode
	var typ native.TypeNode
	switch n.Kind() {
	case native.GalaKIND_valDeclaration:
		d := native.ValDeclarationNode{GalaNodeRef: n}
		tuple, typ = d.TuplePattern(), d.Type_()
	case native.GalaKIND_varDeclaration:
		d := native.VarDeclarationNode{GalaNodeRef: n}
		tuple, typ = d.TuplePattern(), d.Type_()
	default:
		return native.GalaNodeRef{}
	}
	if tuple.Ok() && typ.Ok() {
		return typ.GalaNodeRef
	}
	return native.GalaNodeRef{}
}

// receiverBaseTypeNameNative reduces a receiver's type t (`*Box[T]`,
// `Ptr[Box[T]]`) to the bare type name (`Box`).
func receiverBaseTypeNameNative(t native.TypeNode) string {
	elem, _ := galaparser.NativePointerElem(t)
	return receiverBaseTypeOf(galaparser.NativeTypeText(elem))
}

// collectReceiverTypeArgsNative adds to out the names the type arguments of a
// receiver type n bind: `Box[K, V]` and `*Box[T]` bind K, V and T.
func collectReceiverTypeArgsNative(n native.GalaNodeRef, out map[string]bool) {
	n.Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() != native.GalaKIND_typeArguments {
			return true
		}
		list := native.TypeArgumentsNode{GalaNodeRef: n}.TypeList()
		for t := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_type) {
			qi := native.TypeNode{GalaNodeRef: t}.QualifiedIdentifier()
			if id, ok := galaparser.OnlyNativeKid(qi.GalaNodeRef, native.GalaKIND_identifier); ok {
				out[galaparser.NativeText(id)] = true
			}
		}
		return false
	})
}

// collectFileTypeBindersNative returns every type name the file root binds
// itself: the type parameters it declares on any function, method or type —
// including the names a method receiver binds (`func (b Box[T]) ...` binds T)
// — and the types it declares at any depth, sealed variants included. The
// package-level types are in the metadata as well; one declared inside a
// function body is only here. Like the type parameters, they are recognised
// file-wide, which can only suppress a report.
func collectFileTypeBindersNative(root native.GalaNodeRef) map[string]bool {
	out := make(map[string]bool)
	bind := func(id native.IdentifierNode) {
		if id.Ok() {
			out[galaparser.NativeText(id.GalaNodeRef)] = true
		}
	}
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_typeParameter:
			bind(native.TypeParameterNode{GalaNodeRef: n}.Identifier(0))
			return false
		case native.GalaKIND_receiver:
			if rt := (native.ReceiverNode{GalaNodeRef: n}).Type_(); rt.Ok() {
				collectReceiverTypeArgsNative(rt.GalaNodeRef, out)
			}
			return false
		case native.GalaKIND_typeDeclaration:
			bind(native.TypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_structShorthandDeclaration:
			bind(native.StructShorthandDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_sealedTypeDeclaration:
			bind(native.SealedTypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_opaqueTypeDeclaration:
			bind(native.OpaqueTypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_sealedCase:
			bind(native.SealedCaseNode{GalaNodeRef: n}.Identifier())
		}
		return true
	})
	return out
}
