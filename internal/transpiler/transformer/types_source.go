package transformer

import (
	"go/ast"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// A type as its lowering reads it (lowerType). typeSrc is a type node of
// ANTLR's tree (ctx) or of galop's native one (node), and each of its readers
// has an ANTLR half and a native half that give the same value for the same
// type, so a type is lowered once, the same way, whichever tree it comes
// from. The native halves dispatch on the node's children and read them
// through the rule handles and NativeKids, never through Typed() or the All
// accessors (see declarations_native.go).

// typeSrc is a type as written; the zero typeSrc is no type.
type typeSrc struct {
	ctx  grammar.ITypeContext
	node native.TypeNode
}

// antlrType is the type ctx, on ANTLR's tree; nil gives no type.
func antlrType(ctx grammar.ITypeContext) typeSrc { return typeSrc{ctx: ctx} }

// nativeType is the type node n; no node gives no type.
func nativeType(n native.TypeNode) typeSrc { return typeSrc{node: n} }

// ok reports whether s is a type, not the zero typeSrc.
func (s typeSrc) ok() bool { return s.ctx != nil || s.node.Ok() }

// at is where s is written.
func (s typeSrc) at() srcNode {
	if s.node.Ok() {
		return srcNode{node: s.node.GalaNodeRef}
	}
	return srcNode{ctx: s.ctx}
}

// text is s's tokens run together (GetText).
func (s typeSrc) text() string {
	if s.node.Ok() {
		return s.node.GetText()
	}
	return s.ctx.GetText()
}

// typeText is TypeText of s.
func (s typeSrc) typeText() string {
	if s.node.Ok() {
		return galaparser.NativeTypeText(s.node)
	}
	return galaparser.TypeText(s.ctx)
}

// sourceText is the source s spans, whitespace included.
func (s typeSrc) sourceText() string {
	if s.node.Ok() {
		return s.node.SourceText()
	}
	return transpiler.SourceText(s.ctx)
}

// ptrElem is PtrElem of s: the type argument of `Ptr[T]`, or no type.
func (s typeSrc) ptrElem() typeSrc {
	if s.node.Ok() {
		return nativeType(galaparser.NativePtrElem(s.node))
	}
	if s.ctx == nil {
		return typeSrc{}
	}
	return antlrType(galaparser.PtrElem(s.ctx))
}

// pointerElem is PointerElem of s: the element type of `*T` or `Ptr[T]` and
// true, or (s, false).
func (s typeSrc) pointerElem() (typeSrc, bool) {
	if s.node.Ok() {
		elem, ok := galaparser.NativePointerElem(s.node)
		return nativeType(elem), ok
	}
	if s.ctx == nil {
		return s, false
	}
	elem, ok := galaparser.PointerElem(s.ctx)
	return antlrType(elem), ok
}

// named reports whether s is a named type, `pkg.Name[Args]`.
func (s typeSrc) named() bool {
	if s.node.Ok() {
		return s.node.QualifiedIdentifier().Ok()
	}
	return s.ctx.QualifiedIdentifier() != nil
}

// nameParts is the identifiers of a named type's name, in order.
func (s typeSrc) nameParts() []string {
	if s.node.Ok() {
		var parts []string
		for id := range galaparser.NativeKids(s.node.QualifiedIdentifier().GalaNodeRef, native.GalaKIND_identifier) {
			parts = append(parts, galaparser.NativeText(id))
		}
		return parts
	}
	ids := s.ctx.QualifiedIdentifier().(*grammar.QualifiedIdentifierContext).AllIdentifier()
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.GetText()
	}
	return parts
}

// nameText is a named type's name as written, its tokens run together.
func (s typeSrc) nameText() string {
	if s.node.Ok() {
		return s.node.QualifiedIdentifier().GetText()
	}
	return s.ctx.QualifiedIdentifier().GetText()
}

// typeArgs is a named type's type arguments and true when it is written
// with them (`Box[A, B]`), and false when it is not.
func (s typeSrc) typeArgs() ([]typeSrc, bool) {
	if s.node.Ok() {
		args := s.node.TypeArguments()
		if !args.Ok() {
			return nil, false
		}
		return nativeTypes(args.TypeList().GalaNodeRef), true
	}
	if s.ctx.TypeArguments() == nil {
		return nil, false
	}
	// A partial parse (an editor's `Box[]`) has no type list.
	list, ok := s.ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext)
	if !ok {
		return nil, true
	}
	return antlrTypes(list.AllType_()), true
}

// nativeTypes is the child types of n.
func nativeTypes(n native.GalaNodeRef) []typeSrc {
	var out []typeSrc
	for c := range galaparser.NativeKids(n, native.GalaKIND_type) {
		out = append(out, nativeType(native.TypeNode{GalaNodeRef: c}))
	}
	return out
}

// antlrTypes is the types all.
func antlrTypes(all []grammar.ITypeContext) []typeSrc {
	out := make([]typeSrc, len(all))
	for i, a := range all {
		out[i] = antlrType(a)
	}
	return out
}

// hasTypeArgs reports whether s is written with type arguments.
func (s typeSrc) hasTypeArgs() bool {
	if s.node.Ok() {
		return s.node.TypeArguments().Ok()
	}
	return s.ctx.TypeArguments() != nil
}

// typeShape is the form of a type that is not named.
type typeShape uint8

const (
	shapeOther   typeShape = iota // none of the below (parse-error recovery)
	shapePointer                  // *T
	shapeSlice                    // []T
	shapeMap                      // map[K]V
	shapeFunc                     // func(...) R
)

// elems is the types s is written with directly: T of `*T` and `[]T`, K and
// V of `map[K]V`.
func (s typeSrc) elems() []typeSrc {
	if s.node.Ok() {
		return nativeTypes(s.node.GalaNodeRef)
	}
	return antlrTypes(s.ctx.AllType_())
}

// startText is the text of s's first token.
func (s typeSrc) startText() string {
	if s.node.Ok() {
		return galaparser.NativeStartText(s.node.GalaNodeRef)
	}
	return s.ctx.GetStart().GetText()
}

// hasSignature reports whether s is written with a signature, a function
// type's.
func (s typeSrc) hasSignature() bool {
	if s.node.Ok() {
		return s.node.Signature().Ok()
	}
	return s.ctx.Signature() != nil
}

// shape is the form of s, which is not named, and, for a pointer, slice or
// map type, its elems.
func (s typeSrc) shape() (typeShape, []typeSrc) {
	var shape typeShape
	minElems := 1
	switch s.startText() {
	case "*":
		shape = shapePointer
	case "[":
		shape = shapeSlice
	case "map":
		shape, minElems = shapeMap, 2
	}
	if shape != shapeOther {
		if elems := s.elems(); len(elems) >= minElems {
			return shape, elems
		}
	}
	if s.hasSignature() {
		return shapeFunc, nil
	}
	return shapeOther, nil
}

// funcTypeParam is a parameter of a function type: its type, or, written
// without one, the identifier that names it (`func(T) bool`), and whether it
// is variadic.
type funcTypeParam struct {
	typ      typeSrc
	name     string
	variadic bool
}

// funcSignature is the parameters and results of s, a function type.
func (s typeSrc) funcSignature() ([]funcTypeParam, []typeSrc) {
	if s.node.Ok() {
		sig := s.node.Signature()
		var params []funcTypeParam
		for p := range galaparser.NativeKids(sig.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			param := native.ParameterNode{GalaNodeRef: p}
			fp := funcTypeParam{variadic: param.ELLIPSIS().Ok()}
			if typ := param.Type_(); typ.Ok() {
				fp.typ = nativeType(typ)
			} else {
				fp.name = param.Identifier().GetText()
			}
			params = append(params, fp)
		}
		return params, nativeSigResults(sig)
	}
	sig := s.ctx.Signature().(*grammar.SignatureContext)
	var params []funcTypeParam
	if list := sig.Parameters().(*grammar.ParametersContext).ParameterList(); list != nil {
		for _, pCtx := range list.(*grammar.ParameterListContext).AllParameter() {
			param := pCtx.(*grammar.ParameterContext)
			fp := funcTypeParam{variadic: param.ELLIPSIS() != nil}
			if param.Type_() != nil {
				fp.typ = antlrType(param.Type_())
			} else {
				fp.name = param.Identifier().GetText()
			}
			params = append(params, fp)
		}
	}
	return params, antlrSigResults(sig)
}

// antlrSigResults is the result types of the signature ctx: its one result
// type, or each type of its Go result list (`(int, error)`).
func antlrSigResults(ctx *grammar.SignatureContext) []typeSrc {
	if ctx.Type_() != nil {
		return []typeSrc{antlrType(ctx.Type_())}
	}
	list, ok := ctx.GoResults().(*grammar.GoResultsContext)
	if !ok {
		return nil
	}
	return antlrTypes(list.AllType_())
}

// nativeSigResults is antlrSigResults of the signature node sig.
func nativeSigResults(sig native.SignatureNode) []typeSrc {
	if typ := sig.Type_(); typ.Ok() {
		return []typeSrc{nativeType(typ)}
	}
	return nativeTypes(sig.GoResults().GalaNodeRef)
}

// noteNativeType tells onNativeType of n when it is a node of the native tree.
func (t *galaASTTransformer) noteNativeType(n srcNode) {
	if t.onNativeType != nil && n.node.Ok() {
		t.onNativeType(int(n.node.Start().Line))
	}
}

// lowerFuncTypeSignature is the Go function type of s, a function type. A
// parameter written without a type is a type itself (`func(T) bool`).
func (t *galaASTTransformer) lowerFuncTypeSignature(s typeSrc) (*ast.FuncType, error) {
	params, resultTypes := s.funcSignature()
	fieldList := &ast.FieldList{}
	for _, p := range params {
		field := &ast.Field{}
		if p.typ.ok() {
			typ, err := t.lowerType(p.typ)
			if err != nil {
				return nil, err
			}
			field.Type = typ
		} else {
			field.Type = t.funcTypeParamName(p.name)
		}
		if p.variadic {
			field.Type = &ast.Ellipsis{Elt: field.Type}
		}
		fieldList.List = append(fieldList.List, field)
	}
	results, err := t.lowerSignatureResults(resultTypes)
	if err != nil {
		return nil, err
	}
	return &ast.FuncType{Params: fieldList, Results: results}, nil
}

// lowerSignatureResults lowers a signature's result types; nil when it has
// none.
func (t *galaASTTransformer) lowerSignatureResults(types []typeSrc) (*ast.FieldList, error) {
	if len(types) == 0 {
		return nil, nil
	}
	results := &ast.FieldList{}
	for _, typ := range types {
		retType, err := t.lowerType(typ)
		if err != nil {
			return nil, err
		}
		t.isImmutableType(t.astTypeToTranspilerType(retType))
		results.List = append(results.List, &ast.Field{Type: retType})
	}
	return results, nil
}
